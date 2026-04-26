package inmemory

import (
	"context"
	"fmt"
	"sync"

	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
)

// Compile-time interface check.
var _ payment.Repository = (*InMemoryPaymentRepository)(nil)

// InMemoryPaymentRepository is an in-memory implementation of payment.Repository.
type InMemoryPaymentRepository struct {
	mu       sync.RWMutex
	payments map[shared.PaymentID]*payment.Payment
}

// NewInMemoryPaymentRepository creates a new InMemoryPaymentRepository.
func NewInMemoryPaymentRepository() *InMemoryPaymentRepository {
	return &InMemoryPaymentRepository{
		payments: make(map[shared.PaymentID]*payment.Payment),
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

	r.payments[p.ID()] = p
	return nil
}

// FindByID loads a payment by its ID.
func (r *InMemoryPaymentRepository) FindByID(_ context.Context, id shared.PaymentID) (*payment.Payment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.payments[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("payment %s not found", id))
	}
	return p, nil
}

// FindByInvoiceID returns all payments for an invoice.
func (r *InMemoryPaymentRepository) FindByInvoiceID(_ context.Context, invoiceID shared.InvoiceID) ([]*payment.Payment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*payment.Payment
	for _, p := range r.payments {
		if p.InvoiceID() == invoiceID {
			result = append(result, p)
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
			return p, nil
		}
	}
	return nil, nil
}
