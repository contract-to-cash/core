package pricing

import (
	"github.com/contract-to-cash/core/domain/shared"
)

// FlatPrice is a pricing model that charges a fixed price regardless of usage.
type FlatPrice struct {
	Price shared.Money
}

// CalculatePrice returns the flat price regardless of usage.
func (p FlatPrice) CalculatePrice(usage int64) shared.Money {
	return p.Price
}
