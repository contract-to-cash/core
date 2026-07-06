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

	// 3. Determine how many VALIDATED (applied) coupons to accept.
	//
	// The limit is enforced against coupons that pass ALL per-coupon checks in
	// the loop below (validity window, minAmount, currency, per-account limit),
	// NOT against the raw scanned slice. An earlier version truncated the slice
	// up front (coupons[:1] / coupons[:max]) BEFORE these checks ran, so a
	// leading invalid coupon (e.g. expired) could crowd out a valid coupon
	// behind it and zero the discount for a customer who actually holds a valid
	// coupon (#158). Counting validated coupons instead makes the selection
	// "first valid wins".
	//
	// Semantics of effectiveLimit (0 == unlimited):
	//   - AllowStacking=false: apply at most ONE coupon — the first that passes
	//     every check. This takes precedence over MaxCouponsPerInvoice.
	//   - AllowStacking=true: apply up to MaxCouponsPerInvoice validated coupons.
	//     A value of 0 means "no limit" (matches the documented reference
	//     implementation's `> 0` sentinel; guards W1 so that 0 does not silently
	//     zero out all discounts).
	effectiveLimit := 0
	if !p.config.AllowStacking {
		effectiveLimit = 1
	} else if p.config.MaxCouponsPerInvoice > 0 {
		effectiveLimit = p.config.MaxCouponsPerInvoice
	}

	// 4. Calculate discount for each coupon that passes validation.
	subtotal := ctx.Subtotal()
	total := zero
	now := p.clock.Now()
	applied := 0 // count of coupons that passed all checks and were applied
	for _, c := range coupons {
		// Stop once the maximum number of validated coupons has been applied.
		if effectiveLimit > 0 && applied >= effectiveLimit {
			break
		}

		// Defensive validity check: even though the repository is given `At` and
		// is expected to filter, repo filtering is optional — re-check the
		// validity window and global usage limit here (review M4).
		if !c.IsValid(now) {
			continue
		}

		// Check minimum purchase amount. minAmount must be denominated in the
		// invoice currency: comparing raw big.Rat amounts across currencies is
		// meaningless (Money exposes no cross-currency comparison). A coupon whose
		// minAmount is misconfigured in a foreign currency is skipped rather than
		// aborting the whole calculation — consistent with the foreign-currency
		// fixed-discount handling below (review W6 / issue #148).
		if c.minAmount != nil {
			if c.minAmount.Currency() != currency {
				continue
			}
			if subtotal.Amount().Cmp(c.minAmount.Amount()) < 0 {
				continue
			}
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

		discount, err := c.CalculateDiscount(subtotal)
		if err != nil {
			return zero, fmt.Errorf("coupon: calculate discount: %w", err)
		}

		// Skip fixed-amount coupons denominated in a different currency from the
		// invoice rather than aborting the entire calculation downstream when
		// total.Add hits a currency mismatch (review W6). Percentage discounts
		// are derived from the subtotal and always match.
		if discount.Currency() != currency {
			continue
		}

		// Cap individual discount at subtotal
		if discount.GreaterThan(subtotal) {
			discount = subtotal
		}

		// The coupon has passed every validity check; it counts toward the
		// applied limit ("first valid wins" when stacking is disabled).
		applied++

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
