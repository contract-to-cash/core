package payment

import (
	"context"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Repository defines the persistence interface for payments.
//
// Concurrency contract (issue #97): implementations MUST enforce a unique
// constraint on idempotency_key so that concurrent ProcessPayment calls
// with the same key cannot persist two distinct payment records. The
// application-layer FindByIdempotencyKey / Save pair cannot serialize
// on its own — the guarantee has to come from the storage backend.
//
// Recommended implementations:
//   - Postgres / MySQL: a UNIQUE INDEX on idempotency_key (or a
//     SELECT ... FOR UPDATE inside the TxManager.RunInTx closure).
//   - DynamoDB: a ConditionExpression that rejects the put when the key
//     already exists.
//   - Any other backend: an equivalent compare-and-swap guarantee.
//
// When the constraint fires, Save MUST return an error that satisfies
// errors.Is(err, [ErrDuplicateIdempotencyKey]) — typically a
// [*DuplicateIdempotencyKeyError]. PaymentService catches the sentinel
// as a concurrent-success race-loss signal and converges on the
// winner's record via FindByIdempotencyKey rather than firing saga
// compensation.
//
// Migration note for consumer adapters: raw driver errors (e.g.
// `pgconn.PgError{Code: "23505"}`, `*mysql.MySQLError{Number: 1062}`,
// DynamoDB `ConditionalCheckFailedException`) MUST be translated to
// the sentinel before returning to the application layer. A consumer
// adapter that returns a raw driver error will route the race loser
// through saga compensation, refunding the winner's legitimate
// gateway charge (see CHANGELOG entry for v-BREAKING-97).
type Repository interface {
	// Save persists a payment.
	//
	// If the payment's IdempotencyKey collides with a DIFFERENT persisted
	// payment (same non-empty key, different ID), Save MUST return an
	// error matching errors.Is(err, [ErrDuplicateIdempotencyKey]).
	// Implementations are encouraged to return a
	// [*DuplicateIdempotencyKeyError] so callers can extract the
	// offending key and involved PaymentIDs via errors.As.
	//
	// Same-ID updates (e.g. the 3DS Pending → Completed upgrade path)
	// MUST be allowed because they target the existing record.
	//
	// Optimistic-locking concurrency contract (issue #190): implementations MUST
	// protect the load → state-check → save sequence performed by callers such as
	// PaymentService.Refund (and the payment status transitions) against lost
	// updates. Satisfy this in ONE of two ways:
	//
	//   1. Optimistic locking (recommended): compare the payment's
	//      LoadedVersion() against the stored version and return an error that
	//      tx.IsVersionConflict recognizes — either the tx.ErrVersionConflict
	//      sentinel or a *shared.DomainError with Code ==
	//      shared.ErrCodeVersionConflict — when they differ. On success, persist
	//      Version() as the new stored version. This lets the caller's
	//      RetryOnConflict re-read the winner's already-recorded state, where
	//      RecordRefund rejects the retry with an over-refund /
	//      invalid_state_transition domain error.
	//   2. Read serialization: take a row lock in FindByID (SELECT ... FOR
	//      UPDATE) or run at SERIALIZABLE isolation so a concurrent transition
	//      blocks until the winner commits and then observes the fresh state.
	//
	// An implementation that does neither (unconditional last-writer-wins upsert)
	// allows two concurrent RecordRefund calls on the same completed payment to
	// both succeed — booking one refund while the gateway moved money twice — and
	// a concurrent Pending→Completed (3DS) vs Pending→Failed (webhook) pair to
	// silently lose one transition. The first save of a given ID has no prior
	// version to compare, so fresh payments (LoadedVersion 0) are unaffected. See
	// infrastructure/inmemory for a reference optimistic-locking implementation.
	Save(ctx context.Context, payment *Payment) error

	// FindByID loads a payment by its ID.
	//
	// Not-found convention (issue #197): implementations MUST return an error for
	// a missing payment — a shared.DomainError with code shared.ErrCodeNotFound —
	// and MUST NOT return (nil, nil). Callers (e.g. PaymentService.Refund) treat a
	// nil result defensively as not-found regardless, but returning a typed error
	// is the contract so business errors and technical errors stay distinguishable.
	// The infrastructure/inmemory implementation is the reference.
	FindByID(ctx context.Context, id shared.PaymentID) (*Payment, error)
	FindByInvoiceID(ctx context.Context, invoiceID shared.InvoiceID) ([]*Payment, error)
	// FindByIdempotencyKey returns a payment with the given idempotency key,
	// or nil if not found. Used to prevent duplicate payment records on retry.
	FindByIdempotencyKey(ctx context.Context, key string) (*Payment, error)

	// FindStalePending returns payments that are still in Pending status and
	// whose ProcessedAt() is strictly before olderThan. ProcessedAt is the
	// staleness timestamp by contract: it is the charge-attempt time stamped at
	// construction (NewPayment receives clock.Now()) and never advances — the
	// Payment entity carries no separate createdAt/updatedAt — so "processed
	// before olderThan and still Pending" is exactly the "nothing settled or
	// failed this record for the whole window" predicate the stale-pending
	// cleanup needs.
	//
	// This is the scan feeding batch.StalePendingPaymentProcessor (issue #98),
	// the counterpart of balance.Repository.FindExpired for payments: it
	// selects candidate ORPHANS — Pending records whose gateway transaction may
	// have been reversed by saga compensation (compensation-after-3DS leaves
	// the 3DS pending record permanently dangling) — for the integrator's
	// PendingPaymentReconciler to classify. The repository does NOT decide
	// whether a candidate is genuinely orphaned; it only applies the
	// status+age filter.
	//
	// Contract:
	//   - Only PaymentStatusPending rows are returned. Every other status is
	//     excluded regardless of age.
	//   - The cutoff is strict: ProcessedAt().Before(olderThan). A payment
	//     processed exactly AT olderThan is not stale.
	//   - Results are ordered by ProcessedAt ascending (oldest first) for
	//     deterministic batch processing; the in-memory implementation breaks
	//     ties by PaymentID ascending.
	//   - limit bounds the number of rows returned (mirroring
	//     balance.Repository.FindExpired): a positive limit returns at most
	//     that many (oldest first, so repeated batch runs drain the backlog
	//     deterministically); 0 or negative means "no limit". The cleanup
	//     batch threads BatchOptions.Limit here.
	//   - No matches is a non-error: return an empty (or nil) slice and a nil
	//     error.
	FindStalePending(ctx context.Context, olderThan time.Time, limit int) ([]*Payment, error)
}
