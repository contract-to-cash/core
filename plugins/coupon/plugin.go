package coupon

import (
	"context"
	"errors"
	"fmt"

	"github.com/contract-to-cash/core/domain/invoice"
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

// Compile-time interface checks.
//
// The plugin implements DiscountHook (compute the discount) AND
// InvoiceLifecycleHook (confirm the redemption in AfterCalculation, inside the
// billing transaction, after the invoice is created). Splitting "compute" from
// "confirm" is what fixes issue #185: CalculateDiscount has NO persistence side
// effects, so a rolled-back pipeline never burns a coupon use, and the
// idempotent redemption keyed by (coupon, contract, period) makes retries and
// RegenerateInvoice for the same period consume exactly one use.
var (
	_ plugin.DiscountHook         = (*CouponPlugin)(nil)
	_ plugin.InvoiceLifecycleHook = (*CouponPlugin)(nil)
)

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
func (p *CouponPlugin) Version() string { return "1.2.0" }

// Priority returns the execution priority.
func (p *CouponPlugin) Priority() int { return p.priority }

// Initialize initializes the plugin with the given configuration.
//
// A present-but-mistyped value is a configuration error and is returned rather
// than silently ignored (issue #239); JSON-decoded numbers (float64 with an
// integral value) are accepted for the integer keys via plugin.Config.Int.
// Unknown keys are ignored.
func (p *CouponPlugin) Initialize(_ context.Context, config plugin.Config) error {
	if n, ok, err := config.Int("maxCouponsPerInvoice"); err != nil {
		return fmt.Errorf("coupon: %w", err)
	} else if ok {
		p.config.MaxCouponsPerInvoice = n
	}
	if b, ok, err := config.Bool("allowStacking"); err != nil {
		return fmt.Errorf("coupon: %w", err)
	} else if ok {
		p.config.AllowStacking = b
	}
	if n, ok, err := config.Int("priority"); err != nil {
		return fmt.Errorf("coupon: %w", err)
	} else if ok {
		p.priority = n
	}
	return nil
}

// Shutdown gracefully shuts down the plugin.
func (p *CouponPlugin) Shutdown(_ context.Context) error { return nil }

// CalculateDiscount calculates the total discount from applicable coupons.
//
// This method has NO persistence side effects (issue #185): it only reads
// (applicable coupons, existing redemptions for usage limits) and records the
// applied discounts on the CalculationContext. Redemptions are confirmed later,
// idempotently, in AfterCalculation — which runs inside the billing transaction
// after the invoice has been created. Because nothing is written here, a
// pipeline that rolls back after this hook (e.g. a failed invoice save) never
// consumes a coupon use.
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

		// Usage limits are reconciled from redemption rows (issue #185), NOT from
		// a side-channel counter. Both the global and per-account checks EXCLUDE
		// the current (contract, billing period): the redemption this billing run
		// is about to (re)confirm is the same logical use, so a retry after a
		// rolled-back invoice — whose redemption is already persisted — must not
		// count itself out of its own limit.

		// Global usage limit: baseline UsedCount() (e.g. migrated historical
		// usage) plus distinct redemptions since, excluding the current use.
		if c.usageLimit != nil {
			globalUsed, err := p.countRedemptions(ctx.Context(), c.id, nil, ctx.ContractID(), ctx.BillingPeriod())
			if err != nil {
				return zero, fmt.Errorf("coupon: count redemptions: %w", err)
			}
			if c.usedCount+globalUsed >= *c.usageLimit {
				continue
			}
		}

		// Per-account usage limit.
		if c.perAccountUsageLimit != nil && accountID != "" {
			used, err := p.countRedemptions(ctx.Context(), c.id, &accountID, ctx.ContractID(), ctx.BillingPeriod())
			if err != nil {
				return zero, fmt.Errorf("coupon: count account redemptions: %w", err)
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

		// Record the applied discount on the context. The redemption is NOT
		// written here — it is confirmed idempotently in AfterCalculation, inside
		// the billing transaction, once the invoice (and thus the billing period
		// the redemption is keyed by) exists (issue #185).
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

	// NOTE: The plugin deliberately does NOT call ctx.SetSubtotalAfterDiscount —
	// that field is owned by the core billing pipeline, which sets it after
	// summing ALL DiscountHook results, rounding, and applying the cap guard
	// (§5.1 steps 3-4). Each coupon's discount is calculated against the
	// original subtotal (parallel application), not a cumulative reduced amount.
	return total, nil
}

// countRedemptions counts confirmed redemptions of a coupon (optionally filtered
// to a single account) EXCLUDING the in-flight (contract, billing period) the
// current billing run is about to (re)confirm. Excluding the current use makes
// usage-limit enforcement retry-safe: a retry after a rolled-back invoice — whose
// redemption is already persisted (the plugin's repository is not part of the
// billing transaction) — does not count itself out of its own limit (issue #185).
func (p *CouponPlugin) countRedemptions(
	ctx context.Context,
	couponID CouponID,
	accountID *shared.AccountID,
	excludeContractID shared.ContractID,
	excludePeriod shared.DateRange,
) (int, error) {
	redemptions, err := p.repo.FindRedemptions(ctx, couponID, accountID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, r := range redemptions {
		if r.ContractID() == excludeContractID && r.BillingPeriod().Equals(excludePeriod) {
			continue
		}
		count++
	}
	return count, nil
}

// BeforeCalculation is a no-op for the coupon plugin; coupons only act during
// discount calculation and redemption confirmation. It exists to satisfy
// plugin.InvoiceLifecycleHook (issue #185).
func (p *CouponPlugin) BeforeCalculation(_ *plugin.CalculationContext) error { return nil }

// AfterCalculation idempotently confirms a redemption for every coupon this
// plugin applied during CalculateDiscount, enforcing usage limits atomically.
//
// This runs inside the billing transaction, AFTER the invoice has been created
// (so the billing period and invoice ID the redemption is keyed by are known)
// and BEFORE the invoice is saved. Combined with SaveRedemption's idempotency on
// (coupon, contract, billing period), this yields the issue #185 invariants:
//
//   - Pipeline failure does not permanently consume a use: if the invoice save
//     fails and the transaction rolls back, a retry re-confirms the SAME key and
//     reuses the redemption rather than adding a second one.
//   - Retry / RegenerateInvoice for the same period consumes exactly one use.
//   - Concurrent confirmations of the same key collapse to one redemption
//     (repository idempotency contract).
//
// Usage-limit atomicity (issue #195): the limits are passed to SaveRedemption,
// which enforces them as part of the same atomic insert. CalculateDiscount's
// usage-limit checks are only an advisory read that two concurrent billing runs
// for DIFFERENT contracts can both pass; SaveRedemption is the authoritative gate
// that rejects the losing confirmation with ErrUsageLimitReached. When that
// happens this method returns a descriptive error wrapping the sentinel, which
// rolls back the billing transaction so the invoice is NOT persisted with a
// discount whose use could not be recorded. A retry then recalculates: because
// CalculateDiscount recounts redemptions and now sees the winner's committed row
// (the plugin's repository is not part of the billing transaction, so the
// winner's redemption survives), it skips the exhausted coupon and the retry
// converges on an invoice without the discount.
//
// Any confirmation error is fatal (returned) so the invoice rolls back rather
// than being persisted without its coupon use recorded.
func (p *CouponPlugin) AfterCalculation(ctx *plugin.CalculationContext, inv *invoice.Invoice) error {
	if inv == nil {
		return nil
	}
	for _, d := range ctx.AppliedDiscounts() {
		if d.PluginName != p.Name() {
			continue
		}
		c, err := p.repo.FindByCode(ctx.Context(), d.Code)
		if err != nil {
			return fmt.Errorf("coupon: resolve coupon %q for redemption: %w", d.Code, err)
		}
		if c == nil {
			// The coupon vanished between calculation and confirmation. The
			// discount already applied to this invoice; there is nothing to
			// record. Skip rather than fail the computed invoice.
			continue
		}
		redemption := NewRedemption(
			RedemptionID(shared.GenerateID()),
			c.ID(),
			c.Code(),
			c.CodeType(),
			inv.AccountID(),
			inv.ContractID(),
			inv.BillingPeriod(),
			inv.ID(),
			p.clock.Now(),
		)
		limits := RedemptionLimits{
			GlobalLimit:     c.UsageLimit(),
			GlobalBaseline:  c.UsedCount(),
			PerAccountLimit: c.PerAccountUsageLimit(),
		}
		if err := p.repo.SaveRedemption(ctx.Context(), redemption, limits); err != nil {
			if errors.Is(err, ErrUsageLimitReached) {
				// Lost the atomic race for the last use(s) of this coupon (or the
				// limit was reached between calculation and confirmation). Abort
				// the pipeline so the invoice is not persisted with an unrecordable
				// discount; a retry recalculates without this coupon (#195).
				return fmt.Errorf("coupon: usage limit reached confirming %q; invoice not persisted with unrecordable discount: %w", d.Code, err)
			}
			return fmt.Errorf("coupon: confirm redemption for %q: %w", d.Code, err)
		}
	}
	return nil
}
