package tax

import (
	"context"
	"errors"
	"math/big"
	"strings"
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

func TestTaxPlugin_Initialize_RejectsNonIntPriority(t *testing.T) {
	p := NewTaxPlugin(&JapaneseTaxCalculator{})
	// A present-but-mistyped value is a configuration error (issue #239),
	// not silently ignored.
	if err := p.Initialize(context.Background(), plugin.Config{"priority": "high"}); err == nil {
		t.Fatal("expected error for string priority, got nil")
	}
	if p.Priority() != plugin.PriorityLow {
		t.Errorf("expected default priority %d to be untouched, got %d", plugin.PriorityLow, p.Priority())
	}
}

// TestTaxPlugin_Initialize_AcceptsJSONFloat64 verifies a JSON-decoded config
// (encoding/json turns all numbers into float64) works (issue #239).
func TestTaxPlugin_Initialize_AcceptsJSONFloat64(t *testing.T) {
	p := NewTaxPlugin(&JapaneseTaxCalculator{})
	if err := p.Initialize(context.Background(), plugin.Config{"priority": float64(42)}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Priority() != 42 {
		t.Errorf("expected priority 42 after float64 Initialize, got %d", p.Priority())
	}
}

// TestTaxPlugin_Initialize_RejectsNonIntegralFloat verifies a fractional float
// is rejected rather than silently truncated (issue #239).
func TestTaxPlugin_Initialize_RejectsNonIntegralFloat(t *testing.T) {
	p := NewTaxPlugin(&JapaneseTaxCalculator{})
	if err := p.Initialize(context.Background(), plugin.Config{"priority": 42.5}); err == nil {
		t.Fatal("expected error for non-integral float priority, got nil")
	}
	if p.Priority() != plugin.PriorityLow {
		t.Errorf("expected default priority %d to be untouched, got %d", plugin.PriorityLow, p.Priority())
	}
}

// TestTaxPlugin_Initialize_IgnoresUnknownKeys verifies unknown keys are still
// ignored (issue #239 only rejects known keys with wrong types).
func TestTaxPlugin_Initialize_IgnoresUnknownKeys(t *testing.T) {
	p := NewTaxPlugin(&JapaneseTaxCalculator{})
	if err := p.Initialize(context.Background(), plugin.Config{"unknownKey": "whatever"}); err != nil {
		t.Fatalf("unexpected error for unknown key: %v", err)
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

// nilRateCalculator violates the TaxCalculator contract by returning nil.
type nilRateCalculator struct{}

func (c *nilRateCalculator) GetTaxRate(_ context.Context) *big.Rat { return nil }

// TestTaxPlugin_NilRateFromCalculator_CleanErrorNoPanic guards the regression
// where a nil rate reached Money.Multiply (which panics on a nil factor): the
// plugin must convert the contract violation into a descriptive
// ErrCodeBusinessRule DomainError instead of panicking, so a misconfigured
// custom calculator produces an actionable error rather than a
// PluginPanicError (with stack trace) vetoing every invoice generation.
func TestTaxPlugin_NilRateFromCalculator_CleanErrorNoPanic(t *testing.T) {
	p := NewTaxPlugin(&nilRateCalculator{})

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := plugin.NewCalculationContext(context.Background(), nil, subtotal)

	// A panic here would fail the test on its own; assert the error shape too.
	_, err := p.CalculateTax(ctx)
	if err == nil {
		t.Fatal("expected error for nil tax rate, got nil")
	}
	var de *shared.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected *shared.DomainError, got %T: %v", err, err)
	}
	if de.Code != shared.ErrCodeBusinessRule {
		t.Errorf("expected code %s, got %s", shared.ErrCodeBusinessRule, de.Code)
	}
	if !strings.Contains(err.Error(), "TaxCalculator") {
		t.Errorf("error should name the TaxCalculator contract violation, got: %v", err)
	}
}

func TestJapaneseTaxCalculator_GetTaxRate(t *testing.T) {
	c := &JapaneseTaxCalculator{}
	rate := c.GetTaxRate(context.Background())
	if rate.Cmp(big.NewRat(10, 100)) != 0 {
		t.Errorf("expected 10%% rate, got %s", rate.RatString())
	}
}
