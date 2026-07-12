package tax

import (
	"context"
	"fmt"
	"math"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// TaxPlugin is a TaxHook plugin that calculates tax on invoice subtotals.
type TaxPlugin struct {
	calculator TaxCalculator
	priority   int
}

// Compile-time interface check.
var _ plugin.TaxHook = (*TaxPlugin)(nil)

// NewTaxPlugin creates a new TaxPlugin with the given tax calculator.
func NewTaxPlugin(calculator TaxCalculator) *TaxPlugin {
	return &TaxPlugin{
		calculator: calculator,
		priority:   plugin.PriorityLow,
	}
}

// Name returns the plugin name.
func (p *TaxPlugin) Name() string { return "tax" }

// Version returns the plugin version.
func (p *TaxPlugin) Version() string { return "1.0.0" }

// Priority returns the execution priority.
func (p *TaxPlugin) Priority() int { return p.priority }

// configInt reads an optional integer key from config. It accepts both a Go int
// and a JSON-decoded float64 with an integral value (encoding/json decodes all
// numbers into float64 — issue #239). A key that is present but has the wrong
// type (or a non-integral float) returns a descriptive error instead of being
// silently ignored. Unknown/absent keys return present=false.
func configInt(config plugin.Config, key string) (value int, present bool, err error) {
	v, ok := config[key]
	if !ok {
		return 0, false, nil
	}
	switch n := v.(type) {
	case int:
		return n, true, nil
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return 0, false, fmt.Errorf("config %q must be an integer, got %v", key, n)
		}
		return int(n), true, nil
	default:
		return 0, false, fmt.Errorf("config %q must be an integer, got %T (%v)", key, v, v)
	}
}

// Initialize initializes the plugin with the given configuration.
//
// A present-but-mistyped "priority" is a configuration error and is returned
// rather than silently ignored (issue #239); JSON-decoded numbers (float64 with
// an integral value) are accepted. Unknown keys are ignored.
func (p *TaxPlugin) Initialize(_ context.Context, config plugin.Config) error {
	if n, ok, err := configInt(config, "priority"); err != nil {
		return fmt.Errorf("tax: %w", err)
	} else if ok {
		p.priority = n
	}
	return nil
}

// Shutdown gracefully shuts down the plugin.
func (p *TaxPlugin) Shutdown(_ context.Context) error { return nil }

// CalculateTax calculates tax on the subtotal after discounts.
func (p *TaxPlugin) CalculateTax(ctx *plugin.CalculationContext) (shared.Money, error) {
	afterDiscount := ctx.SubtotalAfterDiscount()
	taxRate := p.calculator.GetTaxRate(ctx.Context())
	tax := afterDiscount.Multiply(taxRate)
	return tax, nil
}
