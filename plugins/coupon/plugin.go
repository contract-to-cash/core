package coupon

import (
	"context"
	"fmt"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// CouponConfig holds configuration for the CouponPlugin.
type CouponConfig struct {
	MaxCouponsPerInvoice int
	AllowStacking        bool
}

// CouponPlugin is a DiscountHook plugin that applies coupon-based discounts.
type CouponPlugin struct {
	repo     CouponRepository
	config   CouponConfig
	priority int
	clock    shared.Clock
}

// Compile-time interface check.
var _ plugin.DiscountHook = (*CouponPlugin)(nil)

// NewCouponPlugin creates a new CouponPlugin with the given repository and clock.
func NewCouponPlugin(repo CouponRepository, clock shared.Clock) *CouponPlugin {
	return &CouponPlugin{
		repo:     repo,
		priority: plugin.PriorityNormal,
		clock:    clock,
		config: CouponConfig{
			MaxCouponsPerInvoice: 1,
			AllowStacking:        false,
		},
	}
}

// Name returns the plugin name.
func (p *CouponPlugin) Name() string { return "coupon" }

// Version returns the plugin version.
func (p *CouponPlugin) Version() string { return "1.0.0" }

// Priority returns the execution priority.
func (p *CouponPlugin) Priority() int { return p.priority }

// Initialize initializes the plugin with the given configuration.
func (p *CouponPlugin) Initialize(_ context.Context, config plugin.Config) error {
	if v, ok := config["maxCouponsPerInvoice"]; ok {
		if n, ok := v.(int); ok {
			p.config.MaxCouponsPerInvoice = n
		}
	}
	if v, ok := config["allowStacking"]; ok {
		if b, ok := v.(bool); ok {
			p.config.AllowStacking = b
		}
	}
	if v, ok := config["priority"]; ok {
		if n, ok := v.(int); ok {
			p.priority = n
		}
	}
	return nil
}

// Shutdown gracefully shuts down the plugin.
func (p *CouponPlugin) Shutdown(_ context.Context) error { return nil }

// CalculateDiscount calculates the total discount from applicable coupons.
func (p *CouponPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
	currency := ctx.Subtotal().Currency()
	zero := shared.Zero(currency)

	// 1. Find applicable coupons
	coupons, err := p.repo.FindApplicable(ctx.Context(), ctx.ContractID(), p.clock.Now())
	if err != nil {
		return zero, fmt.Errorf("coupon: find applicable: %w", err)
	}

	if len(coupons) == 0 {
		return zero, nil
	}

	// 2. If stacking is not allowed, use only the first coupon
	if !p.config.AllowStacking {
		coupons = coupons[:1]
	}

	// 3. Apply MaxCouponsPerInvoice limit
	if len(coupons) > p.config.MaxCouponsPerInvoice {
		coupons = coupons[:p.config.MaxCouponsPerInvoice]
	}

	// 4. Calculate discount for each coupon
	subtotal := ctx.Subtotal()
	total := zero
	for _, c := range coupons {
		// Check minimum purchase amount
		if c.minAmount != nil && subtotal.Amount().Cmp(c.minAmount.Amount()) < 0 {
			continue
		}

		discount := c.CalculateDiscount(subtotal)

		// Cap individual discount at subtotal
		if discount.GreaterThan(subtotal) {
			discount = subtotal
		}

		// 5. Record usage
		if err := p.repo.RecordUsage(ctx.Context(), c.id, ctx.ContractID()); err != nil {
			return zero, fmt.Errorf("coupon: record usage: %w", err)
		}

		// 6. Record discount
		ctx.RecordDiscount(plugin.AppliedDiscount{
			PluginName: p.Name(),
			Code:       c.Code(),
			Amount:     discount,
		})

		sum, err := total.Add(discount)
		if err != nil {
			return zero, fmt.Errorf("coupon: sum discounts: %w", err)
		}
		total = sum
	}

	// 7. Return total discount
	return total, nil
}
