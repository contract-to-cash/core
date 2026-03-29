package payment

import (
	"fmt"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// PaymentStatus represents the lifecycle status of a payment.
type PaymentStatus string

const (
	PaymentStatusPending           PaymentStatus = "pending"
	PaymentStatusCompleted         PaymentStatus = "completed"
	PaymentStatusFailed            PaymentStatus = "failed"
	PaymentStatusPartiallyRefunded PaymentStatus = "partially_refunded"
	PaymentStatusRefunded          PaymentStatus = "refunded"
	PaymentStatusChargedBack       PaymentStatus = "charged_back"
)

// PaymentMethod represents a payment method type.
type PaymentMethod string

const (
	PaymentMethodCreditCard   PaymentMethod = "credit_card"
	PaymentMethodBankTransfer PaymentMethod = "bank_transfer"
	PaymentMethodDirectDebit  PaymentMethod = "direct_debit"
	PaymentMethodConvenience  PaymentMethod = "convenience_store"
	PaymentMethodCarrier      PaymentMethod = "carrier"
)

// Payment represents a payment entity.
type Payment struct {
	id                   shared.PaymentID
	invoiceID            shared.InvoiceID
	amount               shared.Money
	refundedAmount       shared.Money // cumulative total of all refunds
	method               PaymentMethod
	status               PaymentStatus
	gatewayTransactionID string
	idempotencyKey       string // for deduplication on retry
	failureReason        *string
	processedAt          time.Time
	metadata             map[string]string
}

// NewPayment creates a new Payment.
func NewPayment(
	id shared.PaymentID,
	invoiceID shared.InvoiceID,
	amount shared.Money,
	method PaymentMethod,
	gatewayTransactionID string,
	processedAt time.Time,
) *Payment {
	return &Payment{
		id:                   id,
		invoiceID:            invoiceID,
		amount:               amount,
		refundedAmount:       shared.Zero(amount.Currency()),
		method:               method,
		status:               PaymentStatusPending,
		gatewayTransactionID: gatewayTransactionID,
		processedAt:          processedAt,
		metadata:             make(map[string]string),
	}
}

// --- Getters ---

func (p *Payment) ID() shared.PaymentID         { return p.id }
func (p *Payment) InvoiceID() shared.InvoiceID  { return p.invoiceID }
func (p *Payment) Amount() shared.Money         { return p.amount }
func (p *Payment) Method() PaymentMethod        { return p.method }
func (p *Payment) Status() PaymentStatus        { return p.status }
func (p *Payment) GatewayTransactionID() string { return p.gatewayTransactionID }
func (p *Payment) IdempotencyKey() string        { return p.idempotencyKey }
func (p *Payment) RefundedAmount() shared.Money  { return p.refundedAmount }
func (p *Payment) FailureReason() *string        { return p.failureReason }
func (p *Payment) ProcessedAt() time.Time        { return p.processedAt }

// SetIdempotencyKey sets the idempotency key for deduplication.
func (p *Payment) SetIdempotencyKey(key string) { p.idempotencyKey = key }

// Complete marks the payment as completed. Only valid from pending.
func (p *Payment) Complete() error {
	if p.status != PaymentStatusPending {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot complete payment: current status is %s", p.status))
	}
	p.status = PaymentStatusCompleted
	return nil
}

// Fail marks the payment as failed with a reason. Only valid from pending.
func (p *Payment) Fail(reason string) error {
	if p.status != PaymentStatusPending {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot fail payment: current status is %s", p.status))
	}
	p.status = PaymentStatusFailed
	p.failureReason = &reason
	return nil
}

// MarkRefunded marks the payment as fully refunded. Only valid from completed or partially_refunded.
func (p *Payment) MarkRefunded() error {
	if p.status != PaymentStatusCompleted && p.status != PaymentStatusPartiallyRefunded {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot refund payment: current status is %s", p.status))
	}
	p.status = PaymentStatusRefunded
	return nil
}

// MarkPartiallyRefunded marks the payment as partially refunded. Only valid from completed.
func (p *Payment) MarkPartiallyRefunded() error {
	if p.status != PaymentStatusCompleted {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot partially refund payment: current status is %s", p.status))
	}
	p.status = PaymentStatusPartiallyRefunded
	return nil
}

// MarkChargedBack marks the payment as charged back. Only valid from completed.
func (p *Payment) MarkChargedBack() error {
	if p.status != PaymentStatusCompleted {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot charge back payment: current status is %s", p.status))
	}
	p.status = PaymentStatusChargedBack
	return nil
}

// RecordRefund records a refund of the given amount and updates status.
// Only valid from completed or partially_refunded. Validates that cumulative
// refunded amount does not exceed the original payment amount.
// This replaces MarkRefunded/MarkPartiallyRefunded for new code — the old
// methods are retained for backward compatibility but RecordRefund is preferred.
func (p *Payment) RecordRefund(amount shared.Money) error {
	if p.status != PaymentStatusCompleted && p.status != PaymentStatusPartiallyRefunded {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot refund payment: current status is %s", p.status))
	}
	newTotal, err := p.refundedAmount.Add(amount)
	if err != nil {
		return fmt.Errorf("failed to calculate refund total: %w", err)
	}
	if newTotal.Amount().Cmp(p.amount.Amount()) > 0 {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			"refund amount exceeds payment amount")
	}
	p.refundedAmount = newTotal
	// Status derived from cumulative total
	if newTotal.Amount().Cmp(p.amount.Amount()) == 0 {
		p.status = PaymentStatusRefunded
	} else {
		p.status = PaymentStatusPartiallyRefunded
	}
	return nil
}

func (p *Payment) Metadata() map[string]string {
	cp := make(map[string]string, len(p.metadata))
	for k, v := range p.metadata {
		cp[k] = v
	}
	return cp
}
