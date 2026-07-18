package plugin

import (
	"github.com/contract-to-cash/core/domain/shared"
)

// BeforeChargeHook is called before a charge is attempted.
type BeforeChargeHook interface {
	Plugin
	BeforeCharge(ctx *PaymentContext, amount shared.Money) error
}

// AfterChargeHook is called after a charge has been successfully processed.
type AfterChargeHook interface {
	Plugin
	AfterCharge(ctx *PaymentContext) error
}

// OnPaymentFailedHook is called when a payment fails.
type OnPaymentFailedHook interface {
	Plugin
	OnPaymentFailed(ctx *PaymentContext, err error) error
}

// OnRefundHook is called when a refund is processed.
type OnRefundHook interface {
	Plugin
	OnRefund(ctx *PaymentContext, refundAmount shared.Money) error
}

// CompensationMethod identifies which reversal actually undid a gateway
// charge during saga compensation (issue #257).
type CompensationMethod string

const (
	// CompensationMethodVoid — the charge was reversed pre-settlement via Void.
	CompensationMethodVoid CompensationMethod = "void"
	// CompensationMethodRefund — Void failed (charge already captured/settled)
	// and the Refund fallback reversed it.
	CompensationMethodRefund CompensationMethod = "refund"
	// CompensationMethodNone — both Void and the Refund fallback failed. The
	// gateway charge is still standing and manual reconciliation is required
	// (CompensationResult.CompensationErr carries the combined failure).
	CompensationMethodNone CompensationMethod = "none"
)

// CompensationReason identifies why saga compensation ran (issue #257).
type CompensationReason string

const (
	// CompensationReasonLocalSaveFailed — the local bookkeeping transaction
	// (payment/invoice Save) failed after a successful gateway charge.
	CompensationReasonLocalSaveFailed CompensationReason = "local_save_failed"
	// CompensationReasonOutboxVeto — the PaymentOutboxWriter vetoed the record
	// inside the bookkeeping transaction (issue #248), rolling it back.
	CompensationReasonOutboxVeto CompensationReason = "outbox_veto"
	// CompensationReasonIdempotencyConflict — the in-transaction idempotency
	// check found the effective key colliding with an existing payment in the
	// Failed terminal state (a race between the pre-charge lookup and a
	// concurrent writer). The charge the gateway just captured is real and
	// backed by no local record (a Failed record captured nothing), so the
	// transaction is abandoned and the charge reversed — but no local Save was
	// ever attempted, which is why this is distinct from
	// CompensationReasonLocalSaveFailed (issue #234 review).
	CompensationReasonIdempotencyConflict CompensationReason = "idempotency_conflict"
)

// CompensationResult carries the outcome of a saga compensation (charge
// reversal) attempt for OnCompensationExecutedHook (issue #257).
type CompensationResult struct {
	// TransactionID is the gateway transaction ID of the original charge.
	TransactionID string
	// Amount is the original charge amount.
	Amount shared.Money
	// Method is the reversal that actually took effect (void / refund), or
	// CompensationMethodNone when the compensation itself failed. Partial
	// success is impossible: the only failure mode is Void failing AND the
	// Refund fallback failing.
	Method CompensationMethod
	// Reason is why the compensation ran.
	Reason CompensationReason
	// CompensationErr is nil when the compensation succeeded. Non-nil means
	// BOTH Void and Refund failed — the charge stands un-reversed and the
	// payment is in a MANUAL RECONCILIATION state.
	CompensationErr error
	// MarkCompensatedErr reports a failure to record the compensation marker
	// in the IdempotencyStore (issue #87). It is only ever non-nil when the
	// compensation itself succeeded (the marker write is attempted only then);
	// a non-nil value means a subsequent retry with the same original key may
	// race (operators should monitor store health).
	MarkCompensatedErr error
}

// OnCompensationExecutedHook is called after PaymentService.ProcessPayment has
// attempted saga compensation of a successful gateway charge ("charge succeeded
// → local transaction failed → reverse the charge", issue #257).
//
// The hook is NON-FATAL and fires on BOTH outcomes: a successful reversal
// (Method = void or refund, CompensationErr = nil) and a failed one (Method =
// none, CompensationErr != nil — the MANUAL RECONCILIATION state). A returned
// error or panic is logged and never alters the outcome of ProcessPayment.
// ctx.Payment() is the local payment record that failed to persist (its
// in-memory state was never committed) and ctx.Invoice() the invoice being
// paid; use CompensationResult for the authoritative gateway-side facts.
type OnCompensationExecutedHook interface {
	Plugin
	OnCompensationExecuted(ctx *PaymentContext, result CompensationResult) error
}
