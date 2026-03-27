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
func (r *InMemoryProductRepository) Save(_ context.Context, p *product.Product) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.products[p.ID()] = p
	return nil
}

// FindByID returns a product by its ID.
func (r *InMemoryProductRepository) FindByID(_ context.Context, id shared.ProductID) (*product.Product, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.products[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("product %s not found", id))
	}
	return p, nil
}
