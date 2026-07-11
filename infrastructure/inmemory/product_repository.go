package inmemory

import (
	"context"
	"fmt"
	"sync"

	"github.com/contract-to-cash/core/domain/product"
	"github.com/contract-to-cash/core/domain/shared"
)

// Compile-time interface check.
var _ product.Repository = (*InMemoryProductRepository)(nil)

// InMemoryProductRepository is an in-memory implementation of product.Repository.
type InMemoryProductRepository struct {
	mu       sync.RWMutex
	products map[shared.ProductID]*product.Product
}

// NewInMemoryProductRepository creates a new InMemoryProductRepository.
func NewInMemoryProductRepository() *InMemoryProductRepository {
	return &InMemoryProductRepository{
		products: make(map[shared.ProductID]*product.Product),
	}
}

// Save persists a product.
//
// Stores an ISOLATED copy (snapshot round-trip), not the caller's pointer, so a
// later mutation of the passed *product.Product cannot leak into the repository
// or into concurrent readers (issue #152 discipline, applied here for #197).
// Product is a mutable Entity (unlike the immutable pricing.Price), so sharing
// the raw pointer would let a caller silently rewrite persisted state.
func (r *InMemoryProductRepository) Save(_ context.Context, p *product.Product) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, err := cloneProduct(p)
	if err != nil {
		return err
	}
	r.products[p.ID()] = stored
	return nil
}

// FindByID returns a product by its ID.
//
// Returns an ISOLATED copy (snapshot round-trip) so callers that mutate the
// returned product never affect the stored state or concurrent readers
// (issue #152 discipline / #197).
func (r *InMemoryProductRepository) FindByID(_ context.Context, id shared.ProductID) (*product.Product, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.products[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("product %s not found", id))
	}
	return cloneProduct(p)
}

// cloneProduct returns an isolated deep copy of p via the snapshot round-trip.
// FromSnapshot honours the caller-supplied ID so the clone keeps the same ID.
func cloneProduct(p *product.Product) (*product.Product, error) {
	return product.FromSnapshot(p.ToSnapshot())
}
