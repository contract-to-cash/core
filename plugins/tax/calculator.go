package tax

import (
	"context"
	"math/big"
)

// TaxCalculator computes tax rates.
type TaxCalculator interface {
	// GetTaxRate returns the applicable tax rate.
	GetTaxRate(ctx context.Context) *big.Rat
}

// JapaneseTaxCalculator implements the standard Japanese consumption tax (10%).
type JapaneseTaxCalculator struct{}

// GetTaxRate returns the Japanese consumption tax rate of 10%.
func (c *JapaneseTaxCalculator) GetTaxRate(_ context.Context) *big.Rat {
	return big.NewRat(10, 100)
}
