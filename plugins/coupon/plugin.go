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

// Compile-time interface checks.
// The plugin computes discounts (DiscountHook, side-effect-free) and persists
// redemptions/usage inside the billing transaction (TransactionalDiscountHook).
var (
	_ plugin.DiscountHook              = (*CouponPlugin)(nil)
	_ plugin.TransactionalDiscountHook = (*CouponPlugin)(nil)
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

	// 4. Apply MaxCouponsPerInvoice limit.
	// A value of 0 means "no limit" (matches the documented reference
	// implementation's `> 0` sentinel); without this guard, 0 would truncate
	// coupons to an empty slice and silently zero out all discounts (W1).
	if p.config.MaxCouponsPerInvoice > 0 && len(coupons) > p.config.MaxCouponsPerInvoice {
		coupons = coupons[:p.config.MaxCouponsPerInvoice]
	}

	// 5. Calculate discount for each coupon
	subtotal := ctx.Subtotal()
	total := zero
	now := p.clock.Now()
	for _, c := range coupons {
		// Defensive validity check: even though the repository is given `At` and
		// is expected to filter, repo filtering is optional — re-check the
		// validity window and global usage limit here (review M4).
		if !c.IsValid(now) {
			continue
		}

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

		// 6. Record the applied discount (intent only — NO durable side effects).
		//
		// The redemption and usage-counter writes were moved out of this
		// calculation phase (issue #123): they used to run through a repo that is
		// not part of the billing transaction, so a later billing failure (tax,
		// credit application, invoice save, ...) left a phantom redemption and an
		// inflated usage counter that were never rolled back. CalculateDiscount is
		// now pure; the durable writes happen in CommitDiscounts, which the core
		// invokes inside the billing transaction after the invoice is saved.
		//
		// Reference carries the coupon ID so CommitDiscounts can reconstruct exactly
		// which coupon to persist without holding mutable per-request state on this
		// shared plugin instance. Together with ContractID it is the idempotency key.
		ctx.RecordDiscount(plugin.AppliedDiscount{
			PluginName: p.Name(),
			Code:       c.Code(),
			Amount:     discount,
			AccountID:  accountID,
			ContractID: ctx.ContractID(),
			Reference:  string(c.id),
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

// CommitDiscounts persists the durable side effects of the coupon discounts that
// CalculateDiscount recorded: a redemption audit record plus the global usage
// counter increment for each applied coupon.
//
// The core calls this from inside the billing transaction, after the invoice has
// been saved, so these writes commit or roll back atomically with the invoice
// (issue #123): if any earlier billing step failed, the transaction never reaches
// this method and no phantom redemption is left behind.
//
// Idempotency: the writes are keyed by (couponID, contractID). SaveRedemption and
// RecordUsage are required to be no-ops when a record already exists for that pair
// (see CouponRepository), so a retried or replayed commit — e.g. an optimistic-lock
// retry of the surrounding transaction — does not double-count. Redemption is
// written before usage (same ordering rationale as before): the audit record
// precedes the counter increment.
func (p *CouponPlugin) CommitDiscounts(txCtx context.Context, applied []plugin.AppliedDiscount) error {
	now := p.clock.Now()
	for _, d := range applied {
		if d.PluginName != p.Name() {
			continue
		}
		couponID := CouponID(d.Reference)
		if couponID == "" {
			// No coupon reference to persist against — skip defensively rather
			// than fabricate a redemption for an unidentifiable coupon.
			continue
		}

		// codeType is descriptive metadata on the redemption record; look it up
		// best-effort. A missing/errored lookup must not block the commit, so fall
		// back to the shared default. The (couponID, contractID) idempotency key
		// does not depend on it.
		codeType := CodeTypeShared
		if c, err := p.repo.FindByCode(txCtx, d.Code); err == nil && c != nil {
			codeType = c.CodeType()
		}

		redemption := NewRedemption(
			RedemptionID(shared.GenerateID()),
			couponID,
			d.Code,
			codeType,
			d.AccountID,
			d.ContractID,
			now,
		)
		if err := p.repo.SaveRedemption(txCtx, redemption); err != nil {
			return fmt.Errorf("coupon: save redemption: %w", err)
		}
		if err := p.repo.RecordUsage(txCtx, couponID, d.ContractID); err != nil {
			return fmt.Errorf("coupon: record usage: %w", err)
		}
	}
	return nil
}
