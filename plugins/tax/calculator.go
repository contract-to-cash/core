package tax

import (
	"context"
	"math/big"
)

// TaxCalculator computes tax rates.
type TaxCalculator interface {
	// GetTaxRate returns the applicable tax rate.
	//
	// Contract: the returned rate must be NON-NIL. For "no tax" return an
	// explicit zero rate (big.NewRat(0, 1)), never nil — shared.Money.Multiply
	// panics on a nil factor, so TaxPlugin.CalculateTax rejects a nil rate
	// with an ErrCodeBusinessRule DomainError naming this contract violation,
	// which aborts (vetoes) the invoice being calculated.
	GetTaxRate(ctx context.Context) *big.Rat
}

// JapaneseTaxCalculator implements the standard Japanese consumption tax (10%).
type JapaneseTaxCalculator struct{}

// GetTaxRate returns the Japanese consumption tax rate of 10%.
func (c *JapaneseTaxCalculator) GetTaxRate(_ context.Context) *big.Rat {
	return big.NewRat(10, 100)
}
