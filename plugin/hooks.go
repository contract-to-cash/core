package plugin

import (
	"context"

	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
)

// DiscountHook is implemented by plugins that calculate discounts during billing.
//
// CalculateDiscount MUST be side-effect-free: it computes the discount amount and
// records intent via CalculationContext.RecordDiscount, but must not perform any
// durable writes. Durable side effects here escape the billing transaction and
// are not rolled back when a later billing step fails (see issue #123). A plugin
// that needs to persist redemption/usage records must additionally implement
// TransactionalDiscountHook and do those writes in CommitDiscounts.
type DiscountHook interface {
	Plugin
	CalculateDiscount(ctx *CalculationContext) (shared.Money, error)
}

// TransactionalDiscountHook is an optional extension of DiscountHook for plugins
// that need to persist durable records (e.g. coupon redemptions, usage counters)
// as a consequence of a discount being applied.
//
// The core calls CommitDiscounts from inside the billing transaction's tx.Run
// closure, AFTER the invoice has been saved and every other billing step has
// succeeded. txCtx carries the active transaction (real database adapters join it
// via the connection/querier stored on the context; the in-memory manager runs
// in-process), so these writes commit or roll back atomically with the invoice.
//
// applied contains every discount recorded across all discount plugins for this
// invoice; an implementation must select its own entries (e.g. by matching
// AppliedDiscount.PluginName). Because idempotent replay may invoke this more than
// once for the same logical operation, implementations MUST make the writes
// idempotent on a stable key (the coupon plugin uses (couponID, contractID)).
//
// This is a purely additive contract: DiscountHooks that do not implement it keep
// working unchanged and simply have no post-save commit step.
type TransactionalDiscountHook interface {
	Plugin
	CommitDiscounts(txCtx context.Context, applied []AppliedDiscount) error
}

// TaxHook is implemented by plugins that calculate tax during billing.
type TaxHook interface {
	Plugin
	CalculateTax(ctx *CalculationContext) (shared.Money, error)
}

// InvoiceLifecycleHook is implemented by plugins that hook into invoice calculation lifecycle.
type InvoiceLifecycleHook interface {
	Plugin
	BeforeCalculation(ctx *CalculationContext) error
	AfterCalculation(ctx *CalculationContext, invoice *invoice.Invoice) error
}
