// Package payment — errors.go defines payment-specific sentinel errors
// and typed error structures.
package payment

import (
	"errors"
	"fmt"

	"github.com/contract-to-cash/core/domain/shared"
)

// ErrDuplicateIdempotencyKey is the sentinel returned (or wrapped by a
// [DuplicateIdempotencyKeyError]) when [Repository.Save] detects a
// non-empty IdempotencyKey collision with a DIFFERENT existing
// PaymentID.
//
// Use errors.Is(err, ErrDuplicateIdempotencyKey) to detect the condition;
// use errors.As(err, &*DuplicateIdempotencyKeyError) to extract the
// offending key and involved PaymentIDs.
//
// PaymentService.ProcessPayment catches this sentinel inside its
// RunInTx closure and converges on the winner's record via
// FindByIdempotencyKey rather than firing saga compensation — see
// application/service/payment_service.go for details.
//
// WHY A PAYMENT-SCOPED SENTINEL (not shared.ErrCodeDuplicateRequest):
// the shared DomainError code is used for other duplicate-request
// conditions across the codebase (usage records, etc.). If a consumer
// Postgres adapter ever returned ErrCodeDuplicateRequest for an
// unrelated UNIQUE INDEX (e.g. (invoice_id, created_at)), the
// PaymentService would mistakenly route it through the winner-
// convergence path and mask a legitimate save failure. Scoping the
// sentinel to this package keeps the contract precise.
var ErrDuplicateIdempotencyKey = errors.New("duplicate idempotency key")

// DuplicateIdempotencyKeyError is the typed error returned by
// [Repository.Save] on idempotency-key collisions. It implements
// Is(target) so errors.Is(err, ErrDuplicateIdempotencyKey) returns
// true and callers can uniformly detect the condition regardless of
// whether the repository returns the sentinel directly or a wrapped
// typed error.
//
// Consumer Postgres/MySQL/DynamoDB adapters SHOULD return this typed
// error when their unique-constraint check rejects a write so
// PaymentService can log the full context (attempted vs. existing
// PaymentID) for operational debugging.
type DuplicateIdempotencyKeyError struct {
	// Key is the IdempotencyKey that collided (required).
	Key string
	// ExistingID is the PaymentID of the record already persisted
	// under Key. Optional — adapters that cannot efficiently look up
	// the winner may leave this zero.
	ExistingID shared.PaymentID
	// AttemptedID is the PaymentID of the record the caller tried to
	// persist and that was rejected.
	AttemptedID shared.PaymentID
}

// Error implements the error interface.
func (e *DuplicateIdempotencyKeyError) Error() string {
	if e.ExistingID != "" {
		return fmt.Sprintf("duplicate idempotency key %q (existing=%s, attempted=%s)",
			e.Key, e.ExistingID, e.AttemptedID)
	}
	return fmt.Sprintf("duplicate idempotency key %q (attempted=%s)",
		e.Key, e.AttemptedID)
}

// Is enables errors.Is(err, ErrDuplicateIdempotencyKey) matching
// regardless of which error form the repository returned.
func (e *DuplicateIdempotencyKeyError) Is(target error) bool {
	return target == ErrDuplicateIdempotencyKey
}
