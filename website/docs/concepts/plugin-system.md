---
sidebar_position: 3
---

# Plugin System

The plugin system lets you extend billing logic through well-defined hooks. Each hook type serves a specific purpose, and you only implement the interfaces you need.

## Plugin Interface

Every plugin implements the base interface:

```go
type Plugin interface {
    Name() string
    Version() string
    Initialize(ctx context.Context, config Config) error
    Shutdown(ctx context.Context) error
    Priority() int  // Lower number = higher priority
}
```

### Priority Constants

```go
const (
    PriorityHighest = 0
    PriorityHigh    = 100
    PriorityNormal  = 500
    PriorityLow     = 900
    PriorityLowest  = 1000
)
```

Plugins are executed in priority order. For example, audit logging at priority 0 runs before discount calculation at priority 500.

## Hook Categories

### Billing Calculation Hooks

These hooks participate in the invoice generation pipeline:

**DiscountHook** — Calculate discounts (coupons, loyalty, volume):

```go
type DiscountHook interface {
    Plugin
    CalculateDiscount(ctx *CalculationContext) (shared.Money, error)
}
```

**TaxHook** — Calculate tax on post-discount amount:

```go
type TaxHook interface {
    Plugin
    CalculateTax(ctx *CalculationContext) (shared.Money, error)
}
```

**InvoiceLifecycleHook** — Before/after invoice calculation:

```go
type InvoiceLifecycleHook interface {
    Plugin
    BeforeCalculation(ctx *CalculationContext) error
    AfterCalculation(ctx *CalculationContext, invoice *invoice.Invoice) error
}
```

### Contract Lifecycle Hooks

React to contract state changes:

```go
type OnContractCreateHook interface {
    Plugin
    OnContractCreate(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractActivateHook interface {
    Plugin
    OnContractActivate(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractSuspendHook interface { ... }
type OnContractResumeHook interface { ... }
type OnContractCancelHook interface { ... }
type OnContractRenewHook interface { ... }
type OnContractTrialEndHook interface { ... }
```

### Payment Hooks

Hook into the payment processing flow:

```go
type BeforeChargeHook interface {
    Plugin
    BeforeCharge(ctx *PaymentContext, amount shared.Money) error
}

type AfterChargeHook interface {
    Plugin
    AfterCharge(ctx *PaymentContext) error
}

type OnPaymentFailedHook interface {
    Plugin
    OnPaymentFailed(ctx *PaymentContext, err error) error
}

type OnRefundHook interface {
    Plugin
    OnRefund(ctx *PaymentContext, refundAmount shared.Money) error
}
```

### Metrics Hooks

Collect KPIs and business metrics:

```go
type OnContractChangeHook interface {
    Plugin
    OnContractChange(ctx *Context, event ContractChangeEvent) error
}

type OnInvoiceIssuedHook interface {
    Plugin
    OnInvoiceIssued(ctx *Context, invoice *invoice.Invoice) error
}

type OnPaymentProcessedHook interface {
    Plugin
    OnPaymentProcessed(ctx *Context, payment *payment.Payment) error
}
```

### Invoice Generation Hooks

Custom invoice rendering and delivery:

```go
type InvoiceGenerationHook interface {
    Plugin
    BuildDocument(ctx *Context, invoice *invoice.Invoice, doc *InvoiceDocument) error
    AfterRender(ctx *Context, doc *InvoiceDocument, rendered []byte) error
    AfterDelivery(ctx *Context, doc *InvoiceDocument, result *DeliveryResult) error
}
```

## Calculation Context

Billing hooks receive a `CalculationContext` with access to:

```go
ctx.Contract()              // The contract aggregate
ctx.Subtotal()              // Current subtotal
ctx.SubtotalAfterDiscount() // Subtotal minus all discounts (for tax)
ctx.AppliedDiscounts()      // List of applied discounts
ctx.Invoice()               // The invoice being generated
ctx.ContractID()            // Shortcut to contract ID
```

## Plugin Registry

Register, initialize, and manage plugins:

```go
registry := plugin.NewRegistry()

// Register plugins (auto-classified by interface)
registry.Register(myDiscountPlugin)
registry.Register(myTaxPlugin)
registry.Register(myAuditPlugin)

// Initialize all with config
configs := map[string]plugin.Config{
    "my-discount": {"percentage": 10},
    "my-tax":      {"priority": plugin.PriorityLow},
}
registry.InitializeAll(ctx, configs)

// Retrieve hooks by type (priority-ordered)
discountHooks := registry.GetDiscountHooks()
taxHooks := registry.GetTaxHooks()
```

## Official Plugins

### Tax Plugin

Calculates tax using a pluggable `TaxCalculator` interface:

```go
taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{}) // 10%
```

You can implement custom `TaxCalculator` for other tax regimes.

### Coupon Plugin

Manages coupon-based discounts with:
- Percentage and fixed amount discounts
- Usage limits (global and per-account)
- Min purchase / max discount caps
- Plan-level restrictions via `applicableTo`
- Stacking control

:::note
Plan-level restrictions via `applicableTo` currently use `PlanID` for matching. This will be migrated to `ProductID`-based matching in a future release as part of the Product/Price separation.
:::

```go
couponPlugin := coupon.NewCouponPlugin(couponRepo, clock)
```
