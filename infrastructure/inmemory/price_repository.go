package inmemory

import (
	"context"
	"fmt"
	"sync"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
)

// Compile-time interface check.
var _ pricing.PriceRepository = (*InMemoryPriceRepository)(nil)

// InMemoryPriceRepository is an in-memory implementation of pricing.PriceRepository.
type InMemoryPriceRepository struct {
	mu     sync.RWMutex
	prices map[shared.PriceID]*pricing.Price
}

// NewInMemoryPriceRepository creates a new InMemoryPriceRepository.
func NewInMemoryPriceRepository() *InMemoryPriceRepository {
	return &InMemoryPriceRepository{
		prices: make(map[shared.PriceID]*pricing.Price),
	}
}

// Save persists a price.
func (r *InMemoryPriceRepository) Save(_ context.Context, p *pricing.Price) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.prices[p.ID()] = p
	return nil
}

// FindByID returns a price by its ID.
func (r *InMemoryPriceRepository) FindByID(_ context.Context, id shared.PriceID) (*pricing.Price, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.prices[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("price %s not found", id))
	}
	return p, nil
}

// FindByProductID returns all prices for a product.
func (r *InMemoryPriceRepository) FindByProductID(_ context.Context, productID shared.ProductID) ([]*pricing.Price, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*pricing.Price
	for _, p := range r.prices {
		if p.ProductID() == productID {
			result = append(result, p)
		}
	}
	return result, nil
}

// FindActiveByProductID returns active prices for a product.
func (r *InMemoryPriceRepository) FindActiveByProductID(_ context.Context, productID shared.ProductID) ([]*pricing.Price, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*pricing.Price
	for _, p := range r.prices {
		if p.ProductID() == productID && p.Status() == pricing.PriceStatusActive {
			result = append(result, p)
		}
	}
	return result, nil
}
