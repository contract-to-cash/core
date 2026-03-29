package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// ProcessPaymentInput holds the parameters for processing a payment.
// PaymentMethodID is optional — if empty, the service resolves it via the
// hierarchical fallback chain: Invoice → Contract → Customer.
type ProcessPaymentInput struct {
	PaymentMethodID string
	Amount          shared.Money
	Currency        shared.Currency
	IdempotencyKey  string
	Metadata        map[string]string
}

// RefundInput holds the parameters for issuing a refund.
type RefundInput struct {
	Amount *shared.Money
	Reason port.RefundReason
}

// PaymentServiceOption configures optional dependencies of PaymentService.
type PaymentServiceOption func(*PaymentService)

// WithPaymentLogger sets a structured logger for the PaymentService.
// If not provided, slog.Default() is used.
func WithPaymentLogger(l *slog.Logger) PaymentServiceOption {
	return func(s *PaymentService) {
		s.logger = l
	}
}

// WithCustomerGateway sets the customer gateway used for payment method resolution.
func WithCustomerGateway(gw port.CustomerGateway) PaymentServiceOption {
	return func(s *PaymentService) {
		s.customerGateway = gw
	}
}

// WithPaymentTxManager sets the transaction manager for the PaymentService.
// If not provided, a NoopTxManager is used (no transaction wrapping).
func WithPaymentTxManager(tm tx.TxManager) PaymentServiceOption {
	return func(s *PaymentService) {
		s.txManager = tm
	}
}

// PaymentService orchestrates payment processing with plugin hooks.
type PaymentService struct {
	gateway         port.PaymentGateway
	paymentRepo     payment.Repository
	invoiceRepo     invoice.Repository
	contractRepo    contract.Repository
	customerGateway port.CustomerGateway
	eventStore      eventstore.Store
	registry        *plugin.Registry
	clock           shared.Clock
	logger          *slog.Logger
	txManager       tx.TxManager
}

// NewPaymentService creates a new PaymentService.
// Required dependencies are positional arguments; optional dependencies
// (logger, customer gateway) are provided via PaymentServiceOption.
func NewPaymentService(
	gateway port.PaymentGateway,
	paymentRepo payment.Repository,
	invoiceRepo invoice.Repository,
	contractRepo contract.Repository,
	eventStore eventstore.Store,
	registry *plugin.Registry,
	clock shared.Clock,
	opts ...PaymentServiceOption,
) *PaymentService {
	s := &PaymentService{
		gateway:      gateway,
		paymentRepo:  paymentRepo,
		invoiceRepo:  invoiceRepo,
		contractRepo: contractRepo,
		eventStore:   eventStore,
		registry:     registry,
		clock:        clock,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.txManager == nil {
		s.txManager = tx.NewNoopTxManager(tx.Repos{
			Payments: paymentRepo,
			Invoices: invoiceRepo,
		})
	}
	return s
}

// ProcessPayment charges an invoice and records the payment.
func (s *PaymentService) ProcessPayment(ctx context.Context, invoiceID shared.InvoiceID, input ProcessPaymentInput) (*payment.Payment, error) {
	// Load invoice
	inv, err := s.invoiceRepo.FindByID(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load invoice: %w", err)
	}

	amount := input.Amount
	if amount.IsZero() {
		amount = inv.AmountDue()
	}

	// Validate payment amount against invoice BEFORE charging the gateway.
	// ValidatePayment is side-effect-free, so the invoice state is not modified.
	err = inv.ValidatePayment(amount)
	if err != nil {
		return nil, fmt.Errorf("payment validation failed: %w", err)
	}

	// Resolve payment method via fallback chain if not explicitly provided
	pmID := input.PaymentMethodID
	if pmID == "" {
		resolved, resolveErr := s.ResolvePaymentMethod(ctx, inv)
		if resolveErr != nil {
			return nil, fmt.Errorf("failed to resolve payment method: %w", resolveErr)
		}
		pmID = resolved
	}

	// Build PaymentContext with invoice (payment is nil at this stage for BeforeCharge)
	payCtx := plugin.NewPaymentContext(ctx, nil, inv)

	// Execute BeforeCharge hooks
	for _, hook := range s.registry.GetBeforeChargeHooks() {
		err = hook.BeforeCharge(payCtx, amount)
		if err != nil {
			return nil, fmt.Errorf("BeforeCharge hook error: %w", err)
		}
	}

	// Charge via gateway
	chargeResp, err := s.gateway.Charge(ctx, &port.ChargeRequest{
		Amount:          amount,
		CustomerID:      string(inv.AccountID()),
		PaymentMethodID: &pmID,
		Description:     fmt.Sprintf("Invoice %s", invoiceID),
		Metadata:        input.Metadata,
		IdempotencyKey:  input.IdempotencyKey,
	})

	if err != nil {
		// Create and persist a failed payment record for tracking
		failedPayment := payment.NewPayment(
			shared.NewPaymentID(),
			invoiceID,
			amount,
			payment.PaymentMethodCreditCard,
			"",
			s.clock.Now(),
		)
		if failErr := failedPayment.Fail(err.Error()); failErr == nil {
			// Best-effort save of failed payment record
			_ = s.paymentRepo.Save(ctx, failedPayment)
		}
		// Execute OnPaymentFailed hooks with PaymentContext
		failCtx := plugin.NewPaymentContext(ctx, failedPayment, inv)
		for _, hook := range s.registry.GetOnPaymentFailedHooks() {
			if hookErr := hook.OnPaymentFailed(failCtx, err); hookErr != nil {
				s.logger.Warn("OnPaymentFailed hook failed",
					"paymentID", failedPayment.ID(),
					"invoiceID", invoiceID,
					"error", hookErr,
				)
			}
		}
		return nil, fmt.Errorf("gateway charge failed: %w", err)
	}

	// Phase 2: Saga compensation for gateway charge
	saga := tx.NewSaga()
	saga.AddCompensation(func(compCtx context.Context) error {
		_, voidErr := s.gateway.Void(compCtx, &port.VoidRequest{
			AuthorizationID: chargeResp.TransactionID,
		})
		return voidErr
	})

	// Create payment record
	p := payment.NewPayment(
		shared.NewPaymentID(),
		invoiceID,
		amount,
		payment.PaymentMethodCreditCard,
		chargeResp.TransactionID,
		s.clock.Now(),
	)
	if input.IdempotencyKey != "" {
		p.SetIdempotencyKey(input.IdempotencyKey)
	}
	if err := p.Complete(); err != nil {
		return nil, fmt.Errorf("failed to complete payment: %w", err)
	}

	// Record payment on invoice (state mutation after successful charge)
	if err := inv.RecordPayment(amount, s.clock.Now()); err != nil {
		return nil, fmt.Errorf("failed to record payment on invoice: %w", err)
	}

	// Phase 3: All local writes are atomic within a transaction.
	err = s.txManager.RunInTx(ctx, func(txCtx context.Context, repos tx.Repos) error {
		// Idempotency check: if a payment with this key already exists, skip
		if input.IdempotencyKey != "" {
			existing, findErr := repos.Payments.FindByIdempotencyKey(txCtx, input.IdempotencyKey)
			if findErr != nil {
				return fmt.Errorf("idempotency check failed: %w", findErr)
			}
			if existing != nil {
				p = existing
				return nil
			}
		}

		if saveErr := repos.Payments.Save(txCtx, p); saveErr != nil {
			return fmt.Errorf("failed to save payment: %w", saveErr)
		}
		if saveErr := repos.Invoices.Save(txCtx, inv); saveErr != nil {
			return fmt.Errorf("failed to save invoice after payment: %w", saveErr)
		}
		return nil
	})
	if err != nil {
		// Local save failed — compensate by voiding the gateway charge
		if compErr := saga.Compensate(ctx); compErr != nil {
			s.logger.Error("local save failed and compensation also failed (MANUAL RECONCILIATION REQUIRED)",
				"paymentID", p.ID(),
				"invoiceID", invoiceID,
				"saveError", err,
				"compensationError", compErr,
			)
			return nil, fmt.Errorf("local save failed: %w; compensation also failed: %v", err, compErr)
		}
		return nil, fmt.Errorf("local save failed (gateway charge voided): %w", err)
	}

	// Phase 4: AfterCharge hooks (non-fatal, outside transaction)
	successCtx := plugin.NewPaymentContext(ctx, p, inv)
	for _, hook := range s.registry.GetAfterChargeHooks() {
		if hookErr := hook.AfterCharge(successCtx); hookErr != nil {
			s.logger.Warn("AfterCharge hook failed",
				"paymentID", p.ID(),
				"invoiceID", invoiceID,
				"error", hookErr,
			)
		}
	}

	return p, nil
}

// Refund processes a refund for a payment.
func (s *PaymentService) Refund(ctx context.Context, paymentID shared.PaymentID, input RefundInput) error {
	// Load payment
	p, err := s.paymentRepo.FindByID(ctx, paymentID)
	if err != nil {
		return fmt.Errorf("failed to load payment: %w", err)
	}

	refundReq := &port.RefundRequest{
		TransactionID: p.GatewayTransactionID(),
		Amount:        input.Amount,
		Reason:        input.Reason,
	}

	_, err = s.gateway.Refund(ctx, refundReq)
	if err != nil {
		return fmt.Errorf("gateway refund failed: %w", err)
	}

	// Determine refund amount
	refundAmount := p.Amount()
	if input.Amount != nil {
		refundAmount = *input.Amount
	}

	// Phase 3: local save in transaction
	err = s.txManager.RunInTx(ctx, func(txCtx context.Context, repos tx.Repos) error {
		if refundErr := p.RecordRefund(refundAmount); refundErr != nil {
			return refundErr
		}
		return repos.Payments.Save(txCtx, p)
	})
	if err != nil {
		// Gateway refund succeeded but local save failed — this requires manual reconciliation.
		// Refunds cannot be reversed, so we log at Error level.
		s.logger.Error("local save failed after gateway refund (MANUAL RECONCILIATION REQUIRED)",
			"paymentID", paymentID,
			"refundAmount", refundAmount,
			"error", err,
		)
		return fmt.Errorf("local save failed after gateway refund (MANUAL RECONCILIATION REQUIRED): %w", err)
	}

	// Phase 4: post-commit hooks (non-fatal)
	inv, invErr := s.invoiceRepo.FindByID(ctx, p.InvoiceID())
	if invErr != nil {
		s.logger.Warn("invoice lookup failed on refund",
			"paymentID", paymentID,
			"invoiceID", p.InvoiceID(),
			"error", invErr,
		)
		inv = nil
	}

	refundCtx := plugin.NewPaymentContext(ctx, p, inv)
	for _, hook := range s.registry.GetOnRefundHooks() {
		if hookErr := hook.OnRefund(refundCtx, refundAmount); hookErr != nil {
			s.logger.Warn("OnRefund hook failed",
				"paymentID", paymentID,
				"error", hookErr,
			)
		}
	}

	return nil
}

// ResolvePaymentMethod walks the hierarchical fallback chain to determine
// which payment method to charge: Invoice → Contract → Customer.
func (s *PaymentService) ResolvePaymentMethod(ctx context.Context, inv *invoice.Invoice) (string, error) {
	// Level 1: Invoice-level override
	if inv.PaymentMethodID() != nil && *inv.PaymentMethodID() != "" {
		return *inv.PaymentMethodID(), nil
	}

	// Level 2: Contract-level default
	if s.contractRepo != nil {
		agg, err := s.contractRepo.FindByID(ctx, inv.ContractID())
		if err != nil {
			return "", fmt.Errorf("failed to load contract for payment method resolution: %w", err)
		}
		if agg.PaymentMethodID() != nil && *agg.PaymentMethodID() != "" {
			return *agg.PaymentMethodID(), nil
		}

		// Level 3: Customer-level default (via gateway)
		if s.customerGateway != nil {
			customer, err := s.customerGateway.GetCustomer(ctx, string(agg.AccountID()))
			if err != nil {
				return "", fmt.Errorf("failed to load customer for payment method resolution: %w", err)
			}
			if customer.DefaultPaymentMethodID != nil && *customer.DefaultPaymentMethodID != "" {
				return *customer.DefaultPaymentMethodID, nil
			}
		}
	}

	return "", shared.NewDomainError(shared.ErrCodeBusinessRule,
		fmt.Sprintf("no payment method found for invoice %s: checked invoice, contract %s, and customer default",
			inv.ID(), inv.ContractID()))
}
