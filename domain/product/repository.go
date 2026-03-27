package product

import (
	"context"

	"github.com/contract-to-cash/core/domain/shared"
)

// Repository provides access to products.
type Repository interface {
	FindByID(ctx context.Context, id shared.ProductID) (*Product, error)
	Save(ctx context.Context, product *Product) error
}
