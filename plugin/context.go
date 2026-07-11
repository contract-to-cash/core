package plugin

import (
	"context"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
)

// AppliedDiscount represents a discount that has been applied by a plugin.
type AppliedDiscount struct {
	PluginName string
	Code       string
	Amount     shared.Money
}

// CalculationContext provides type-safe context for billing calculation hooks.
type CalculationContext struct {
	ctx                   context.Context
	contract              *contract.ContractAggregate
	invoice               *invoice.Invoice
	productID             shared.ProductID
	billingPeriod         shared.DateRange
	subtotal              shared.Money
	subtotalAfterDiscount shared.Money
	appliedDiscounts      []AppliedDiscount
}

// NewCalculationContext creates a new CalculationContext.
func NewCalculationContext(ctx context.Context, c *contract.ContractAggregate, subtotal shared.Money) *CalculationContext {
	return &CalculationContext{
		ctx:                   ctx,
		contract:              c,
		subtotal:              subtotal,
		subtotalAfterDiscount: subtotal,
		appliedDiscounts:      nil,
	}
}

// Context returns the underlying context.Context.
func (cc *CalculationContext) Context() context.Context { return cc.ctx }

// Contract returns the contract aggregate.
//
// READ-ONLY: plugins must treat the returned aggregate as read-only. It is the
// live aggregate the core is billing, not a copy — calling a state-transition
// method on it (Activate, Suspend, RenewWithInterval, …) raises an uncommitted
// event that the core's next Save would silently persist, corrupting the event
// stream. Use the getters (ContractID, Status, …) only (issue #162 P3).
func (cc *CalculationContext) Contract() *contract.ContractAggregate { return cc.contract }

// Subtotal returns the current subtotal.
func (cc *CalculationContext) Subtotal() shared.Money { return cc.subtotal }

// SubtotalAfterDiscount returns the subtotal after discounts have been applied.
func (cc *CalculationContext) SubtotalAfterDiscount() shared.Money {
	return cc.subtotalAfterDiscount
}

// AppliedDiscounts returns a copy of the applied discounts slice.
func (cc *CalculationContext) AppliedDiscounts() []AppliedDiscount {
	if cc.appliedDiscounts == nil {
		return nil
	}
	cp := make([]AppliedDiscount, len(cc.appliedDiscounts))
	copy(cp, cc.appliedDiscounts)
	return cp
}

// SetSubtotal sets the subtotal.
func (cc *CalculationContext) SetSubtotal(m shared.Money) { cc.subtotal = m }

// SetSubtotalAfterDiscount sets the subtotal after discount.
func (cc *CalculationContext) SetSubtotalAfterDiscount(m shared.Money) {
	cc.subtotalAfterDiscount = m
}

// RecordDiscount records an applied discount.
func (cc *CalculationContext) RecordDiscount(d AppliedDiscount) {
	cc.appliedDiscounts = append(cc.appliedDiscounts, d)
}

// SetInvoice sets the invoice on the context.
func (cc *CalculationContext) SetInvoice(inv *invoice.Invoice) { cc.invoice = inv }

// Invoice returns the invoice, if set.
func (cc *CalculationContext) Invoice() *invoice.Invoice { return cc.invoice }

// ContractID returns the contract ID without requiring direct access to the contract aggregate.
func (cc *CalculationContext) ContractID() shared.ContractID {
	if cc.contract == nil {
		return ""
	}
	return cc.contract.ContractID()
}

// ProductID returns the product ID associated with this billing calculation.
func (cc *CalculationContext) ProductID() shared.ProductID { return cc.productID }

// SetProductID sets the product ID on the context.
func (cc *CalculationContext) SetProductID(id shared.ProductID) { cc.productID = id }

// BillingPeriod returns the billing period this invoice is being generated for.
//
// The core sets it before any calculation hook runs, so DiscountHook plugins can
// read it (e.g. to key an idempotent coupon redemption by billing period). It is
// the same period carried on the resulting invoice (Invoice.BillingPeriod()). It
// may be the zero DateRange when a CalculationContext is constructed outside the
// billing pipeline (e.g. in a standalone unit test).
func (cc *CalculationContext) BillingPeriod() shared.DateRange { return cc.billingPeriod }

// SetBillingPeriod sets the billing period on the context (called by the core).
func (cc *CalculationContext) SetBillingPeriod(p shared.DateRange) { cc.billingPeriod = p }

// Context provides a generic context for non-calculation hooks.
//
// Concurrency: Context is NOT safe for concurrent use. The core fires each hook
// sequentially and owns the Context for the duration of a single hook
// invocation, so the metadata map is expected to be accessed by one goroutine
// at a time. A hook that stashes the Context and mutates its metadata from a
// spawned goroutine (or shares it across concurrent hook chains) races on the
// map and must provide its own synchronization (issue #162 P2).
type Context struct {
	ctx      context.Context
	metadata map[string]interface{}
}

// NewContext creates a new generic plugin Context.
func NewContext(ctx context.Context) *Context {
	return &Context{
		ctx:      ctx,
		metadata: make(map[string]interface{}),
	}
}

// Context returns the underlying context.Context.
func (c *Context) Context() context.Context { return c.ctx }

// SetMetadata sets a metadata key-value pair.
func (c *Context) SetMetadata(key string, value interface{}) {
	c.metadata[key] = value
}

// GetMetadata retrieves a metadata value by key.
// Returns the value and true if found, nil and false otherwise.
func (c *Context) GetMetadata(key string) (interface{}, bool) {
	v, ok := c.metadata[key]
	return v, ok
}
