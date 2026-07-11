package inmemory

import (
	"context"
	"fmt"
	"sync"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
)

// Compile-time interface check.
var _ payment.Repository = (*InMemoryPaymentRepository)(nil)

// InMemoryPaymentRepository is an in-memory implementation of payment.Repository.
type InMemoryPaymentRepository struct {
	mu       sync.RWMutex
	payments map[shared.PaymentID]*payment.Payment
	versions map[shared.PaymentID]int // stored optimistic-locking version per payment
}

// NewInMemoryPaymentRepository creates a new InMemoryPaymentRepository.
func NewInMemoryPaymentRepository() *InMemoryPaymentRepository {
	return &InMemoryPaymentRepository{
		payments: make(map[shared.PaymentID]*payment.Payment),
		versions: make(map[shared.PaymentID]int),
	}
}

// Save persists a payment.
//
// Concurrent-success guard (issue #97): if the payment has a non-empty
// idempotency key, Save rejects writes that would introduce a SECOND
// record under the same key (i.e. different PaymentID, same
// idempotency_key) by returning a [*payment.DuplicateIdempotencyKeyError]
// (matched by errors.Is(err, payment.ErrDuplicateIdempotencyKey)). This
// simulates the UNIQUE INDEX on idempotency_key that production
// PaymentRepository implementations are expected to enforce (Postgres:
// unique constraint or SELECT ... FOR UPDATE inside TxManager;
// DynamoDB: condition expression; etc.).
//
// Same-ID re-saves (e.g. the 3DS Pending → Completed upgrade path) are
// allowed because they target the existing record, not a duplicate.
//
// PaymentService catches the sentinel and converges on the winner's
// record via FindByIdempotencyKey rather than firing saga compensation
// — see application/service/payment_service.go.
//
// Optimistic-locking guard (issue #190): for a same-ID re-save, Save compares
// the payment's LoadedVersion against the stored version and rejects the write
// with tx.ErrVersionConflict when they differ, so a concurrent
// RecordRefund-vs-RecordRefund (or Complete-vs-Fail) on the same loaded payment
// cannot both persist last-writer-wins. The first save of a given ID has no
// stored version to compare against and always succeeds, so callers that
// construct a fresh payment (LoadedVersion 0) are unaffected.
func (r *InMemoryPaymentRepository) Save(_ context.Context, p *payment.Payment) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if key := p.IdempotencyKey(); key != "" {
		for existingID, existing := range r.payments {
			if existingID == p.ID() {
				continue
			}
			if existing.IdempotencyKey() == key {
				return &payment.DuplicateIdempotencyKeyError{
					Key:         key,
					ExistingID:  existingID,
					AttemptedID: p.ID(),
				}
			}
		}
	}

	if storedVersion, ok := r.versions[p.ID()]; ok {
		if p.LoadedVersion() != storedVersion {
			return tx.ErrVersionConflict
		}
	}

	// Store an ISOLATED copy (snapshot round-trip), not the caller's pointer, so
	// the caller's later mutations to p cannot leak into the repository or into
	// concurrent readers (issue #152). FromSnapshot restores version and
	// loadedVersion together, matching the just-persisted baseline.
	stored, err := clonePayment(p)
	if err != nil {
		return err
	}
	r.payments[p.ID()] = stored
	r.versions[p.ID()] = p.Version()
	// Sync loadedVersion so subsequent saves from the same pointer (the common
	// non-isolated in-memory case) compare against the just-persisted version.
	p.SetVersion(p.Version())
	return nil
}

// clonePayment returns an isolated deep copy of p via the snapshot round-trip.
// FromSnapshot restores version and loadedVersion from the single stored version
// field, so the clone carries the same optimistic-locking baseline as the
// original (issue #190).
func clonePayment(p *payment.Payment) (*payment.Payment, error) {
	return payment.FromSnapshot(p.ToSnapshot())
}

// FindByID loads a payment by its ID.
//
// Returns an ISOLATED copy (snapshot round-trip) so concurrent load-modify
// callers never share a live pointer (issue #152).
func (r *InMemoryPaymentRepository) FindByID(_ context.Context, id shared.PaymentID) (*payment.Payment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.payments[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("payment %s not found", id))
	}
	return clonePayment(p)
}

// FindByInvoiceID returns all payments for an invoice.
func (r *InMemoryPaymentRepository) FindByInvoiceID(_ context.Context, invoiceID shared.InvoiceID) ([]*payment.Payment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*payment.Payment
	for _, p := range r.payments {
		if p.InvoiceID() == invoiceID {
			clone, err := clonePayment(p)
			if err != nil {
				return nil, err
			}
			result = append(result, clone)
		}
	}
	return result, nil
}

// FindByIdempotencyKey returns a payment with the given idempotency key, or nil if not found.
func (r *InMemoryPaymentRepository) FindByIdempotencyKey(_ context.Context, key string) (*payment.Payment, error) {
	if key == "" {
		return nil, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.payments {
		if p.IdempotencyKey() == key {
			return clonePayment(p)
		}
	}
	return nil, nil
}
