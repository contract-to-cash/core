package pricing

import (
	"math/big"

	"github.com/contract-to-cash/core/domain/shared"
)

// UsagePrice is a pricing model that charges per unit of usage with optional min/max clamps.
type UsagePrice struct {
	UnitPrice shared.Money
	Minimum   *shared.Money
	Maximum   *shared.Money
}

// CalculatePrice calculates usage * UnitPrice, clamped by Minimum and Maximum.
// Negative usage is treated as zero.
func (p UsagePrice) CalculatePrice(usage int64) shared.Money {
	if usage <= 0 {
		return shared.Zero(p.UnitPrice.Currency())
	}
	factor := new(big.Rat).SetInt64(usage)
	result := p.UnitPrice.Multiply(factor)

	if p.Minimum != nil && p.Minimum.GreaterThan(result) {
		result = *p.Minimum
	}
	if p.Maximum != nil && result.GreaterThan(*p.Maximum) {
		result = *p.Maximum
	}

	return result
}
