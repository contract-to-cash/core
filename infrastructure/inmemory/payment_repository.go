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
func (r *InMemoryPaymentRepository) Save(_ context.Context, p *payment.Payment) error {
	r.mu.Lock()
	defer r.mu.Unlock()

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
