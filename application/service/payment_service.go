package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/contract-to-cash/core/application/port"
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
		gateway:     gateway,
		paymentRepo: paymentRepo,
		invoiceRepo: invoiceRepo,
		contractRepo: contractRepo,
		eventStore:  eventStore,
		registry:    registry,
		clock:       clock,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.logger == nil {
		s.logger = slog.Default()
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
					"invoiceID", invoiceID,
					"error", hookErr,
				)
			}
		}
		return nil, fmt.Errorf("gateway charge failed: %w", err)
	}

	// Create payment record
	p := payment.NewPayment(
		shared.NewPaymentID(),
		invoiceID,
		amount,
		payment.PaymentMethodCreditCard,
		chargeResp.TransactionID,
		s.clock.Now(),
	)
	if err := p.Complete(); err != nil {
		return nil, fmt.Errorf("failed to complete payment: %w", err)
	}

	// Record payment on invoice (state mutation after successful charge)
	if err := inv.RecordPayment(amount, s.clock.Now()); err != nil {
		return nil, fmt.Errorf("failed to record payment on invoice: %w", err)
	}

	// Save payment and invoice together — if either fails, return error
	// so the caller can compensate (e.g. void the gateway charge)
	if err := s.paymentRepo.Save(ctx, p); err != nil {
		return nil, fmt.Errorf("failed to save payment: %w", err)
	}
	if err := s.invoiceRepo.Save(ctx, inv); err != nil {
		return nil, fmt.Errorf("failed to save invoice after payment: %w", err)
	}

	// Execute AfterCharge hooks with full PaymentContext (errors are non-fatal)
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

	// Update payment status
	if input.Amount != nil && refundAmount.Amount().Cmp(p.Amount().Amount()) < 0 {
		if err := p.MarkPartiallyRefunded(); err != nil {
			return fmt.Errorf("failed to mark payment partially refunded: %w", err)
		}
	} else {
		if err := p.MarkRefunded(); err != nil {
			return fmt.Errorf("failed to mark payment refunded: %w", err)
		}
	}
	if err := s.paymentRepo.Save(ctx, p); err != nil {
		return fmt.Errorf("failed to save payment after refund: %w", err)
	}

	// Load invoice for PaymentContext (best-effort; hooks still fire with nil invoice)
	inv, invErr := s.invoiceRepo.FindByID(ctx, p.InvoiceID())
	if invErr != nil {
		s.logger.Warn("invoice lookup failed on refund",
			"paymentID", paymentID,
			"invoiceID", p.InvoiceID(),
			"error", invErr,
		)
		inv = nil
	}

	// Execute OnRefund hooks with PaymentContext
	refundCtx := plugin.NewPaymentContext(ctx, p, inv)
	for _, hook := range s.registry.GetOnRefundHooks() {
		if err := hook.OnRefund(refundCtx, refundAmount); err != nil {
			s.logger.Warn("OnRefund hook failed",
				"paymentID", paymentID,
				"error", err,
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
