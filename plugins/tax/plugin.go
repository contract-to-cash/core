package tax

import (
	"context"

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

// Initialize initializes the plugin with the given configuration.
func (p *TaxPlugin) Initialize(_ context.Context, config plugin.Config) error {
	if v, ok := config["priority"]; ok {
		if n, ok := v.(int); ok {
			p.priority = n
		}
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
