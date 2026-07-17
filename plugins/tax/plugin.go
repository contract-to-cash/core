package tax

import (
	"context"
	"fmt"

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
//
// A present-but-mistyped "priority" is a configuration error and is returned
// rather than silently ignored (issue #239); JSON-decoded numbers (float64 with
// an integral value) are accepted via plugin.Config.Int. Unknown keys are
// ignored.
func (p *TaxPlugin) Initialize(_ context.Context, config plugin.Config) error {
	if n, ok, err := config.Int("priority"); err != nil {
		return fmt.Errorf("tax: %w", err)
	} else if ok {
		p.priority = n
	}
	return nil
}

// Shutdown gracefully shuts down the plugin.
func (p *TaxPlugin) Shutdown(_ context.Context) error { return nil }

// CalculateTax calculates tax on the subtotal after discounts.
//
// A nil rate from the TaxCalculator is rejected with a descriptive
// ErrCodeBusinessRule DomainError rather than being passed into
// Money.Multiply, which panics on a nil factor. Without this guard a custom
// calculator returning nil would surface as a *PluginPanicError that vetoes
// every invoice generation with a stack trace instead of an actionable error
// (the TaxCalculator contract requires a non-nil rate — see calculator.go).
func (p *TaxPlugin) CalculateTax(ctx *plugin.CalculationContext) (shared.Money, error) {
	afterDiscount := ctx.SubtotalAfterDiscount()
	taxRate := p.calculator.GetTaxRate(ctx.Context())
	if taxRate == nil {
		return shared.Zero(afterDiscount.Currency()), shared.NewDomainError(shared.ErrCodeBusinessRule,
			"tax: TaxCalculator.GetTaxRate returned a nil rate, violating the TaxCalculator contract — return big.NewRat(0, 1) for \"no tax\", never nil")
	}
	tax := afterDiscount.Multiply(taxRate)
	return tax, nil
}
