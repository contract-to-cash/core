package payment

import (
	"context"

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
	FindByID(ctx context.Context, id shared.PaymentID) (*Payment, error)
	FindByInvoiceID(ctx context.Context, invoiceID shared.InvoiceID) ([]*Payment, error)
	// FindByIdempotencyKey returns a payment with the given idempotency key,
	// or nil if not found. Used to prevent duplicate payment records on retry.
	FindByIdempotencyKey(ctx context.Context, key string) (*Payment, error)
}
