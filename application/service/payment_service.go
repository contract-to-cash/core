package service

import (
	"context"
	"fmt"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// ProcessPaymentInput holds the parameters for processing a payment.
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

// PaymentService orchestrates payment processing with plugin hooks.
type PaymentService struct {
	gateway     port.PaymentGateway
	paymentRepo payment.Repository
	invoiceRepo invoice.Repository
	eventStore  eventstore.Store
	registry    *plugin.Registry
	clock       shared.Clock
}

// NewPaymentService creates a new PaymentService.
func NewPaymentService(
	gateway port.PaymentGateway,
	paymentRepo payment.Repository,
	invoiceRepo invoice.Repository,
	eventStore eventstore.Store,
	registry *plugin.Registry,
	clock shared.Clock,
) *PaymentService {
	return &PaymentService{
		gateway:     gateway,
		paymentRepo: paymentRepo,
		invoiceRepo: invoiceRepo,
		eventStore:  eventStore,
		registry:    registry,
		clock:       clock,
	}
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

	// Execute BeforeCharge hooks
	pluginCtx := plugin.NewContext(ctx)
	for _, hook := range s.registry.GetBeforeChargeHooks() {
		err = hook.BeforeCharge(pluginCtx, amount)
		if err != nil {
			return nil, fmt.Errorf("BeforeCharge hook error: %w", err)
		}
	}

	// Charge via gateway
	pmID := input.PaymentMethodID
	chargeResp, err := s.gateway.Charge(ctx, &port.ChargeRequest{
		Amount:          amount,
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
		// Execute OnPaymentFailed hooks
		for _, hook := range s.registry.GetOnPaymentFailedHooks() {
			_ = hook.OnPaymentFailed(pluginCtx, failedPayment, err)
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

	// Execute AfterCharge hooks (errors are non-fatal)
	for _, hook := range s.registry.GetAfterChargeHooks() {
		if hookErr := hook.AfterCharge(pluginCtx, p); hookErr != nil {
			// TODO: inject logger and log hookErr
			_ = hookErr
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

	// Execute OnRefund hooks
	pluginCtx := plugin.NewContext(ctx)
	for _, hook := range s.registry.GetOnRefundHooks() {
		if err := hook.OnRefund(pluginCtx, p, refundAmount); err != nil {
			_ = err // log but don't fail
		}
	}

	return nil
}
