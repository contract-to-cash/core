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
func (p *CouponPlugin) Version() string { return "1.1.0" }

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

	contr := ctx.Contract()
	var accountID shared.AccountID
	var productID shared.ProductID
	if contr != nil {
		accountID = contr.AccountID()
		productID = ctx.ProductID()
	}

	// 1. Find applicable coupons with full query context
	coupons, err := p.repo.FindApplicable(ctx.Context(), CouponQuery{
		ContractID: ctx.ContractID(),
		AccountID:  accountID,
		ProductID:  productID,
		At:         p.clock.Now(),
	})
	if err != nil {
		return zero, fmt.Errorf("coupon: find applicable: %w", err)
	}

	if len(coupons) == 0 {
		return zero, nil
	}

	// 2. Filter coupons by product, contract type, and account restrictions.
	// This is applied defensively in the plugin even though the repository may also filter,
	// because the repository filtering is optional (depends on implementation).
	var filtered []*Coupon
	for _, c := range coupons {
		if !c.IsApplicableToProduct(productID) {
			continue
		}
		// Contract type check is skipped when contract is nil (e.g., standalone coupon validation).
		if contr != nil && !c.IsApplicableToContractType(contr.GetContractType()) {
			continue
		}
		if !c.IsAccountAllowed(accountID) {
			continue
		}
		filtered = append(filtered, c)
	}
	coupons = filtered

	if len(coupons) == 0 {
		return zero, nil
	}

	// 3. If stacking is not allowed, use only the first coupon.
	// Note: AllowStacking=false takes precedence over MaxCouponsPerInvoice.
	if !p.config.AllowStacking {
		coupons = coupons[:1]
	}

	// 4. Apply MaxCouponsPerInvoice limit
	if len(coupons) > p.config.MaxCouponsPerInvoice {
		coupons = coupons[:p.config.MaxCouponsPerInvoice]
	}

	// 5. Calculate discount for each coupon
	subtotal := ctx.Subtotal()
	total := zero
	now := p.clock.Now()
	for _, c := range coupons {
		// Check minimum purchase amount
		if c.minAmount != nil && subtotal.Amount().Cmp(c.minAmount.Amount()) < 0 {
			continue
		}

		// Check per-account usage limit
		if c.perAccountUsageLimit != nil && accountID != "" {
			used, err := p.repo.FindUsageByAccount(ctx.Context(), c.id, accountID)
			if err != nil {
				return zero, fmt.Errorf("coupon: find account usage: %w", err)
			}
			if used >= *c.perAccountUsageLimit {
				continue
			}
		}

		discount := c.CalculateDiscount(subtotal)

		// Cap individual discount at subtotal
		if discount.GreaterThan(subtotal) {
			discount = subtotal
		}

		// 6. Record redemption first (audit trail), then usage counter.
		// This ordering is intentional: if redemption save fails, usage count
		// is not incremented, avoiding phantom usage without an audit record.
		redemption := NewRedemption(
			RedemptionID(shared.GenerateID()),
			c.id,
			c.Code(),
			c.codeType,
			accountID,
			ctx.ContractID(),
			now,
		)
		if err := p.repo.SaveRedemption(ctx.Context(), redemption); err != nil {
			return zero, fmt.Errorf("coupon: save redemption: %w", err)
		}

		// 7. Record usage (increment global counter)
		if err := p.repo.RecordUsage(ctx.Context(), c.id, ctx.ContractID()); err != nil {
			return zero, fmt.Errorf("coupon: record usage: %w", err)
		}

		// 8. Record discount in calculation context
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

	// 9. Update subtotal after discount for downstream hooks.
	// Each coupon's discount is calculated against the original subtotal (parallel application),
	// not the cumulative reduced amount.
	if !total.IsZero() {
		afterDiscount, err := ctx.Subtotal().Subtract(total)
		if err != nil {
			return zero, fmt.Errorf("coupon: update subtotal after discount: %w", err)
		}
		ctx.SetSubtotalAfterDiscount(afterDiscount)
	}

	return total, nil
}
