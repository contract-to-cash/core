package pricing

import (
	"github.com/contract-to-cash/core/domain/shared"
)

// FlatPrice is a pricing model that charges a fixed price regardless of usage.
type FlatPrice struct {
	Price shared.Money
}

// CalculatePrice returns the flat price regardless of usage quantity. Usage is
// otherwise unused, but negative usage still panics so the non-negative
// PricingModel contract holds uniformly across all implementations (see
// assertNonNegativeUsage).
func (p FlatPrice) CalculatePrice(usage int64) shared.Money {
	assertNonNegativeUsage("FlatPrice", usage)
	return p.Price
}
