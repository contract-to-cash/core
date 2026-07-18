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
	PaymentMethodDebitCard    PaymentMethod = "debit_card"
	PaymentMethodBankTransfer PaymentMethod = "bank_transfer"
	PaymentMethodDirectDebit  PaymentMethod = "direct_debit"
	PaymentMethodConvenience  PaymentMethod = "convenience_store"
	PaymentMethodQRCode       PaymentMethod = "qr_code"
	PaymentMethodCarrier      PaymentMethod = "carrier"
	PaymentMethodPostpay      PaymentMethod = "postpay"
)

// Reserved metadata keys under which PaymentService persists the
// customer-facing payment instructions (port.PaymentInstructions) returned by
// a gateway for asynchronous / requires-action charge outcomes. Integrators
// read these from Payment.Metadata() instead of hardcoding the strings.
// Only keys whose corresponding instruction field is non-empty are set.
const (
	// MetadataKeyInstructionsKind classifies the instruction,
	// e.g. "hosted_page", "konbini_voucher", "bank_transfer".
	MetadataKeyInstructionsKind = "instructions_kind"
	// MetadataKeyInstructionsURL is the customer-facing URL for completing
	// the payment.
	MetadataKeyInstructionsURL = "instructions_url"
	// MetadataKeyInstructionsReference is an optional payment reference
	// (payment code, masked virtual-account summary, ...).
	MetadataKeyInstructionsReference = "instructions_reference"
	// MetadataKeyInstructionsExpiresAt is the optional payment-window
	// deadline, formatted as RFC3339 in UTC.
	MetadataKeyInstructionsExpiresAt = "instructions_expires_at"
)

// RefundEntry records one applied refund: the gateway idempotency key the
// refund was executed under and its amount (issue #235 follow-up).
//
// The per-refund key ledger is what lets PaymentService.Refund decide, exactly,
// whether a concurrent refund consumed THIS invocation's gateway idempotency
// key (the gateway call was a no-movement replay → must not be recorded) or
// used a different key (this invocation's movement was real → must be
// recorded). The cumulative refundedAmount alone cannot make that call once
// three or more writers race the same payment: two concurrent partials summing
// to another invocation's amount are indistinguishable from a consumed key
// slot, which previously allowed a phantom ledger record.
//
// IdempotencyKey may be empty for refunds recorded through the legacy keyless
// RecordRefund (integrator-side bookkeeping) — such entries make the key
// ledger incomplete and force the service back to a conservative conflict on
// concurrent advances (see RefundKeysComplete).
type RefundEntry struct {
	IdempotencyKey string
	Amount         shared.Money
}

// Payment represents a payment entity.
type Payment struct {
	id                   shared.PaymentID
	invoiceID            shared.InvoiceID
	amount               shared.Money
	refundedAmount       shared.Money  // cumulative total of all refunds
	refunds              []RefundEntry // per-refund ledger (gateway key + amount)
	method               PaymentMethod
	status               PaymentStatus
	gatewayTransactionID string
	idempotencyKey       string // for deduplication on retry
	failureReason        *string
	processedAt          time.Time
	metadata             map[string]string

	// Optimistic-locking support (mirrors invoice.Invoice / invoice.CreditNote,
	// issue #190). version is bumped by EVERY state transition that changes
	// persisted state (Complete, Fail, MarkRefunded, MarkPartiallyRefunded,
	// MarkChargedBack, RecordRefund). Without it, a completed payment loaded by
	// two operators could each RecordRefund a partial amount and both Save
	// last-writer-wins — booking one refund while the gateway moved money twice.
	// A concurrent Pending→Completed (3DS) vs Pending→Failed (webhook) pair would
	// likewise silently lose one transition. loadedVersion records the version
	// observed at load time; a repository honoring the concurrency contract (see
	// Repository.Save) compares it against the stored version on Save and rejects
	// a mismatch with tx.ErrVersionConflict. A brand-new payment starts at
	// version 0 / loadedVersion 0, so adapters that never populate these fields
	// keep working unchanged.
	version       int
	loadedVersion int
}

// NewPayment creates a new Payment.
//
// The amount must not be negative: a negative payment would corrupt refund and
// invoice-balance math downstream (RecordRefund derives status from cumulative
// totals, and RecordPayment would inflate the balance). Zero is permitted: a
// zero-amount invoice (e.g. one fully covered by a discount or account credit)
// is settled by a zero-amount payment, which is exactly what
// PaymentService.ProcessPayment constructs when it defaults the charge amount to
// the invoice's AmountDue(). Persistence adapters rebuild payments via
// FromSnapshot, which bypasses this guard so replay of historically valid
// payments is never blocked (issue #148).
func NewPayment(
	id shared.PaymentID,
	invoiceID shared.InvoiceID,
	amount shared.Money,
	method PaymentMethod,
	gatewayTransactionID string,
	processedAt time.Time,
) (*Payment, error) {
	if amount.IsNegative() {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"payment amount must not be negative")
	}
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
	}, nil
}

// --- Getters ---

func (p *Payment) ID() shared.PaymentID         { return p.id }
func (p *Payment) InvoiceID() shared.InvoiceID  { return p.invoiceID }
func (p *Payment) Amount() shared.Money         { return p.amount }
func (p *Payment) Method() PaymentMethod        { return p.method }
func (p *Payment) Status() PaymentStatus        { return p.status }
func (p *Payment) GatewayTransactionID() string { return p.gatewayTransactionID }
func (p *Payment) IdempotencyKey() string       { return p.idempotencyKey }
func (p *Payment) RefundedAmount() shared.Money { return p.refundedAmount }

// Refunds returns a defensive copy of the per-refund ledger (gateway
// idempotency key + amount per applied refund), in recording order.
func (p *Payment) Refunds() []RefundEntry {
	if len(p.refunds) == 0 {
		return nil
	}
	cp := make([]RefundEntry, len(p.refunds))
	copy(cp, p.refunds)
	return cp
}

// HasRefundWithIdempotencyKey reports whether a recorded refund was executed
// under the given gateway idempotency key. An empty key never matches (keyless
// legacy entries are not addressable).
func (p *Payment) HasRefundWithIdempotencyKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range p.refunds {
		if r.IdempotencyKey == key {
			return true
		}
	}
	return false
}

// RefundKeysComplete reports whether the per-refund ledger fully accounts for
// the cumulative refunded total AND every entry carries a non-empty gateway
// idempotency key. Only when this holds can the absence of a key from the
// ledger prove that no concurrent refund consumed it (see RefundEntry).
// It returns false for payments whose refund history predates key tracking
// (cumulative total > sum of entries) or contains keyless RecordRefund entries.
func (p *Payment) RefundKeysComplete() bool {
	sum := shared.Zero(p.refundedAmount.Currency())
	for _, r := range p.refunds {
		if r.IdempotencyKey == "" {
			return false
		}
		next, err := sum.Add(r.Amount)
		if err != nil {
			// A currency-mismatched entry can only come from a corrupted
			// snapshot; treat the ledger as unusable rather than guessing.
			return false
		}
		sum = next
	}
	return sum.Amount().Cmp(p.refundedAmount.Amount()) == 0
}

// FailureReason returns a defensive copy of the failure reason pointer so
// callers cannot mutate the payment's internal state (see issue #96).
func (p *Payment) FailureReason() *string {
	if p.failureReason == nil {
		return nil
	}
	v := *p.failureReason
	return &v
}
func (p *Payment) ProcessedAt() time.Time { return p.processedAt }

// SetIdempotencyKey sets the idempotency key for deduplication.
func (p *Payment) SetIdempotencyKey(key string) { p.idempotencyKey = key }

// Complete marks the payment as completed. Only valid from pending.
func (p *Payment) Complete() error {
	if p.status != PaymentStatusPending {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot complete payment: current status is %s", p.status))
	}
	p.status = PaymentStatusCompleted
	p.version++
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
	p.version++
	return nil
}

// MarkRefunded marks the payment as fully refunded. Only valid from completed or partially_refunded.
func (p *Payment) MarkRefunded() error {
	if p.status != PaymentStatusCompleted && p.status != PaymentStatusPartiallyRefunded {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot refund payment: current status is %s", p.status))
	}
	p.status = PaymentStatusRefunded
	p.version++
	return nil
}

// MarkPartiallyRefunded marks the payment as partially refunded. Only valid from completed.
func (p *Payment) MarkPartiallyRefunded() error {
	if p.status != PaymentStatusCompleted {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot partially refund payment: current status is %s", p.status))
	}
	p.status = PaymentStatusPartiallyRefunded
	p.version++
	return nil
}

// MarkChargedBack marks the payment as charged back. Valid from completed or
// partially_refunded.
//
// partially_refunded is allowed because real-world chargebacks routinely land
// after a partial refund has been issued (a customer disputes the remaining
// charge even though part was already returned). A chargeback is a terminal
// gateway-initiated reversal that supersedes the refund bookkeeping, so it wins
// over the partially_refunded state (issue #162 L-9). Fully refunded payments
// are excluded — there is nothing left to charge back.
func (p *Payment) MarkChargedBack() error {
	if p.status != PaymentStatusCompleted && p.status != PaymentStatusPartiallyRefunded {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot charge back payment: current status is %s", p.status))
	}
	p.status = PaymentStatusChargedBack
	p.version++
	return nil
}

// RecordRefund records a refund of the given amount and updates status.
// Only valid from completed or partially_refunded. Validates that cumulative
// refunded amount does not exceed the original payment amount.
// This replaces MarkRefunded/MarkPartiallyRefunded for new code — the old
// methods are retained for backward compatibility but RecordRefund is preferred.
// ValidateRefund checks whether a refund of the given amount is permitted
// WITHOUT mutating the payment. Callers that perform an irreversible side effect
// before recording the refund (e.g. invoking a payment gateway) must run this
// pre-flight check first, so that a preventable error (non-refundable state,
// non-positive amount, currency mismatch, or over-refund) is surfaced before any
// money moves. RecordRefund applies the same checks transactionally.
func (p *Payment) ValidateRefund(amount shared.Money) error {
	if p.status != PaymentStatusCompleted && p.status != PaymentStatusPartiallyRefunded {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot refund payment: current status is %s", p.status))
	}
	// Guard the financial invariant: a non-positive refund would decrease the
	// cumulative refunded total and could flip status back to partially_refunded.
	if amount.IsNegative() || amount.IsZero() {
		return shared.NewDomainError(shared.ErrCodeValidation,
			"refund amount must be positive")
	}
	newTotal, err := p.refundedAmount.Add(amount)
	if err != nil {
		return fmt.Errorf("failed to calculate refund total: %w", err)
	}
	if newTotal.Amount().Cmp(p.amount.Amount()) > 0 {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			"refund amount exceeds payment amount")
	}
	return nil
}

// RecordRefund records a keyless refund. Prefer RecordRefundWithKey when the
// refund was executed at a payment gateway under an idempotency key: keyless
// entries leave the per-refund key ledger incomplete (RefundKeysComplete
// returns false), which downgrades PaymentService.Refund's concurrent-advance
// classification to a conservative conflict for this payment.
func (p *Payment) RecordRefund(amount shared.Money) error {
	return p.RecordRefundWithKey(amount, "")
}

// RecordRefundWithKey records a refund of the given amount together with the
// gateway idempotency key it was executed under, and updates the status.
// See RecordRefund for the state rules and RefundEntry for why the key is
// stored per refund.
func (p *Payment) RecordRefundWithKey(amount shared.Money, gatewayIdempotencyKey string) error {
	if err := p.ValidateRefund(amount); err != nil {
		return err
	}
	// Safe after ValidateRefund: currencies match and the total is within bounds.
	newTotal, err := p.refundedAmount.Add(amount)
	if err != nil {
		return fmt.Errorf("failed to calculate refund total: %w", err)
	}
	p.refundedAmount = newTotal
	p.refunds = append(p.refunds, RefundEntry{
		IdempotencyKey: gatewayIdempotencyKey,
		Amount:         amount,
	})
	// Status derived from cumulative total
	if newTotal.Amount().Cmp(p.amount.Amount()) == 0 {
		p.status = PaymentStatusRefunded
	} else {
		p.status = PaymentStatusPartiallyRefunded
	}
	// Bump the optimistic-locking version (issue #190): two operators that both
	// load a completed payment and each RecordRefund a partial amount must not
	// both persist last-writer-wins. A compliant repository rejects the loser's
	// Save with tx.ErrVersionConflict; on retry RecordRefund re-validates against
	// the winner's already-recorded total and rejects the over-refund.
	p.version++
	return nil
}

// SetMetadata sets a metadata key-value pair. Like SetIdempotencyKey it is an
// initialization-time setter (no optimistic-locking version bump): callers set
// metadata on a freshly constructed payment before its first Save (e.g.
// PaymentService storing gateway payment instructions on a Pending payment
// under the MetadataKeyInstructions* keys).
//
// ⚠️ Do NOT call this on a payment loaded from a repository: because the
// version is not bumped, a concurrent writer cannot detect the change and a
// later Save can silently lose it (or lose the concurrent update). Metadata
// on an already-persisted payment is immutable by convention; if a mutation
// path is ever needed it must go through a version-bumping method.
func (p *Payment) SetMetadata(key, value string) {
	p.metadata[key] = value
}

func (p *Payment) Metadata() map[string]string {
	cp := make(map[string]string, len(p.metadata))
	for k, v := range p.metadata {
		cp[k] = v
	}
	return cp
}

// Version returns the current optimistic-locking version. It is incremented by
// every state transition that changes persisted state — Complete, Fail,
// MarkRefunded, MarkPartiallyRefunded, MarkChargedBack, and RecordRefund. See
// issue #190.
func (p *Payment) Version() int { return p.version }

// LoadedVersion returns the version observed when this payment was loaded from
// persistence. Repository implementations compare it against the stored version
// on Save to detect a concurrent modification (issue #190).
func (p *Payment) LoadedVersion() int { return p.loadedVersion }

// SetVersion sets the version and records it as the loaded version.
// Repository implementations call this after a successful Save so that
// subsequent saves from the same pointer compare against the just-persisted
// version instead of a stale baseline.
//
// For initial reconstitution from persistence, prefer FromSnapshot, which
// restores version and loadedVersion atomically alongside all other fields.
func (p *Payment) SetVersion(v int) {
	p.version = v
	p.loadedVersion = v
}
