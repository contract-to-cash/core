package tax

import (
	"context"
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

func TestTaxPlugin_JapaneseTax(t *testing.T) {
	calc := &JapaneseTaxCalculator{}
	p := NewTaxPlugin(calc)

	subtotal := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	ctx := plugin.NewCalculationContext(context.Background(), nil, subtotal)
	// SubtotalAfterDiscount defaults to subtotal

	tax, err := p.CalculateTax(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := big.NewRat(100, 1)
	if tax.Amount().Cmp(expected) != 0 {
		t.Errorf("expected tax 100, got %s", tax.Amount().RatString())
	}
}

// zeroTaxCalculator returns a 0% tax rate.
type zeroTaxCalculator struct{}

func (c *zeroTaxCalculator) GetTaxRate(_ context.Context) *big.Rat {
	return big.NewRat(0, 1)
}

func TestTaxPlugin_ZeroTax(t *testing.T) {
	calc := &zeroTaxCalculator{}
	p := NewTaxPlugin(calc)

	subtotal := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	ctx := plugin.NewCalculationContext(context.Background(), nil, subtotal)

	tax, err := p.CalculateTax(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !tax.IsZero() {
		t.Errorf("expected zero tax, got %s", tax.Amount().RatString())
	}
}
