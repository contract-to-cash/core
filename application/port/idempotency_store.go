package port

import "context"

// IdempotencyStore tracks idempotency keys that have been "burned" by a prior
// saga compensation (e.g. a compensating Refund after a local save failure),
// and the replacement "effective" key to use on subsequent retries.
//
// # Why this exists
//
// When [service.PaymentService.ProcessPayment] charges the gateway but then
// fails to persist locally, the saga compensation issues a Refund against the
// gateway. The original IdempotencyKey is now tied to a refunded transaction
// at the gateway: most gateways (Stripe, Adyen, PayPal, GMO) will, on retry
// with the same key, return the cached original Charge response — which still
// says "succeeded" even though the transaction has since been refunded. Without
// intervention, the caller's retry would record a completed payment in the
// local database while the gateway holds no money. See issue #87.
//
// The IdempotencyStore lets PaymentService atomically map the "burned"
// original key to a fresh effective key. On retry, the service detects the
// mapping and re-charges the gateway with the effective key, forcing a
// genuinely new charge rather than an idempotent replay of the refunded one.
// When the mapping is persisted successfully, subsequent retries with the
// same original key deterministically resolve to the same effective key,
// preserving end-to-end idempotency across multiple retry attempts.
//
// # Known limitation: MarkCompensated failures
//
// If MarkCompensated itself fails (e.g. database unreachable at the moment
// compensation fires), PaymentService logs the failure and returns the
// primary save error to the caller — it does NOT surface the marker error.
// The consequence is that the next retry with the same original key finds
// no mapping and proceeds with the original key, which replays the
// already-refunded charge from the gateway and can recreate the Issue #87
// race. Operators must monitor the "failed to mark idempotency key as
// compensated" error log and investigate store health promptly.
// Alternative designs that make MarkCompensated failure fatal would leave
// the gateway authoritatively refunded with no local record, which is
// strictly worse: the caller cannot correlate the failure to a known state.
//
// # Industry alignment
//
// This pattern mirrors the consensus across major PSPs: an idempotency key
// represents a single logical operation. Once that operation has been
// compensated, retries belong to a new logical operation with a new key.
// See docs/research/2026-04-10-payment-idempotency-patterns.md for details.
//
// # Implementation notes
//
// Implementations must be safe for concurrent use. The store should be
// durable: an in-memory implementation is acceptable for tests and demos but
// production deployments should back the store with the same database as the
// payment repository to keep marker writes transactional with the saga
// compensation (see [service.WithIdempotencyStore]).
//
// MarkCompensated and ResolveEffectiveKey must together guarantee that every
// retry of the same original key observes the SAME effective key, even under
// concurrent calls. Implementations typically achieve this with a unique
// constraint on the original key column and an INSERT … ON CONFLICT pattern.
//
// # Effective key length
//
// The PaymentService derives effective keys as `originalKey + "-" + ULID`
// (27 bytes of suffix). Callers must ensure that their original
// IdempotencyKey stays short enough that the derived effective key fits
// within every gateway they target:
//
//   - Stripe: 255 bytes (originalKey up to 228 bytes)
//   - PayPal: 255 bytes (originalKey up to 228 bytes)
//   - GMO PG: 255 bytes (originalKey up to 228 bytes)
//   - Adyen:   64 bytes (originalKey up to  37 bytes) ← strictest
//
// Consumers targeting Adyen or routing across multiple gateways should
// constrain their keys accordingly; the library does not truncate or
// validate key length because limits are gateway-specific.
//
// An in-memory reference implementation is provided at
// infrastructure/inmemory.NewInMemoryIdempotencyStore.
type IdempotencyStore interface {
	// MarkCompensated records that the given original key has been
	// compensated and stores the effective replacement key to use on
	// subsequent retries of the same logical operation.
	//
	// Must be idempotent: calling twice with the same original key must
	// NOT overwrite the stored effective key. Implementations should use
	// INSERT … ON CONFLICT DO NOTHING semantics (or equivalent) so that
	// the first call wins and subsequent calls are no-ops, even under
	// concurrent access.
	//
	// An empty original key must be rejected with a non-nil error.
	MarkCompensated(ctx context.Context, originalKey, effectiveKey string) error

	// ResolveEffectiveKey returns the stored effective key for the given
	// original key. The second return value is false if no mapping exists
	// (i.e. the original key has not been compensated).
	//
	// Implementations must return a non-nil error only for genuine storage
	// failures; "not found" is expressed via the ok boolean, not an error.
	ResolveEffectiveKey(ctx context.Context, originalKey string) (effectiveKey string, ok bool, err error)
}
