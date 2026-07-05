package tax

import (
	"context"
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

func TestTaxPlugin_Metadata(t *testing.T) {
	p := NewTaxPlugin(&JapaneseTaxCalculator{})
	if p.Name() != "tax" {
		t.Errorf("Name() = %q, want %q", p.Name(), "tax")
	}
	if p.Version() != "1.0.0" {
		t.Errorf("Version() = %q, want %q", p.Version(), "1.0.0")
	}
	if p.Priority() != plugin.PriorityLow {
		t.Errorf("Priority() = %d, want %d (default PriorityLow)", p.Priority(), plugin.PriorityLow)
	}
}

func TestTaxPlugin_Initialize_PriorityOverride(t *testing.T) {
	p := NewTaxPlugin(&JapaneseTaxCalculator{})
	if err := p.Initialize(context.Background(), plugin.Config{"priority": 42}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Priority() != 42 {
		t.Errorf("expected priority 42 after Initialize, got %d", p.Priority())
	}
}

func TestTaxPlugin_Initialize_IgnoresNonIntPriority(t *testing.T) {
	p := NewTaxPlugin(&JapaneseTaxCalculator{})
	// Non-int value must be ignored, leaving the default priority intact.
	if err := p.Initialize(context.Background(), plugin.Config{"priority": "high"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Priority() != plugin.PriorityLow {
		t.Errorf("expected default priority %d, got %d", plugin.PriorityLow, p.Priority())
	}
}

func TestTaxPlugin_Initialize_EmptyConfig(t *testing.T) {
	p := NewTaxPlugin(&JapaneseTaxCalculator{})
	if err := p.Initialize(context.Background(), plugin.Config{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Priority() != plugin.PriorityLow {
		t.Errorf("expected default priority %d, got %d", plugin.PriorityLow, p.Priority())
	}
}

func TestTaxPlugin_Shutdown(t *testing.T) {
	p := NewTaxPlugin(&JapaneseTaxCalculator{})
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("unexpected error from Shutdown: %v", err)
	}
}

// TestTaxPlugin_UsesSubtotalAfterDiscount verifies tax is computed on the
// post-discount amount, not the raw subtotal.
func TestTaxPlugin_UsesSubtotalAfterDiscount(t *testing.T) {
	p := NewTaxPlugin(&JapaneseTaxCalculator{})

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := plugin.NewCalculationContext(context.Background(), nil, subtotal)

	// Apply a discount: post-discount amount becomes 8000.
	afterDiscount := shared.NewMoney(big.NewRat(8000, 1), shared.CurrencyJPY)
	ctx.SetSubtotalAfterDiscount(afterDiscount)

	tax, err := p.CalculateTax(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 10% of 8000 = 800, NOT 10% of 10000 = 1000.
	expected := big.NewRat(800, 1)
	if tax.Amount().Cmp(expected) != 0 {
		t.Errorf("expected tax 800 (10%% of post-discount 8000), got %s", tax.Amount().RatString())
	}
}

func TestJapaneseTaxCalculator_GetTaxRate(t *testing.T) {
	c := &JapaneseTaxCalculator{}
	rate := c.GetTaxRate(context.Background())
	if rate.Cmp(big.NewRat(10, 100)) != 0 {
		t.Errorf("expected 10%% rate, got %s", rate.RatString())
	}
}
