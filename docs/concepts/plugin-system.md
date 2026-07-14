---
sidebar_position: 3
---

# Plugin System

The plugin system lets you extend billing logic through well-defined hooks. Each hook type serves a specific purpose, and you only implement the interfaces you need (ISP — Interface Segregation Principle).

:::note Canonical reference
This page is an English summary. The canonical, in-depth specification is
[`docs/internals/plugin-system.md`](../internals/plugin-system.md) (see the
[Documentation Map](../README.md)). When details differ, the internals spec and the code win.
:::

## Design Principles

1. **Loose coupling** — Core logic and plugins are cleanly separated
2. **Type safety** — Contracts defined through Go interfaces
3. **ISP compliance** — Implement only the hooks you need; no empty method stubs
4. **Structural ordering** — The core guarantees accounting-correct calculation order; plugin Priority only controls execution within the same hook type

## Hook Categories

The plugin system provides **23 hook interfaces** across 6 categories:

| Category | Hooks | Purpose |
|----------|-------|---------|
| **Billing Calculation** | `DiscountHook`, `TaxHook`, `InvoiceLifecycleHook` | Discounts, tax, pre/post calculation |
| **Contract Lifecycle** | `OnContractCreate/Activate/Suspend/Resume/Cancel/CancelScheduled/CancelUnscheduled/Renew/TrialEndHook` | React to contract state changes |
| **Payment** | `BeforeChargeHook`, `AfterChargeHook`, `OnPaymentFailedHook`, `OnRefundHook`, `OnCompensationExecutedHook` | Hook into payment flow (incl. saga compensation of a gateway charge) |
| **Metrics** | `OnContractChangeHook`, `OnInvoiceIssuedHook`, `OnPaymentProcessedHook` | KPI collection |
| **Invoice Generation** | `InvoiceGenerationHook` | PDF rendering and delivery |
| **Credit Note** | `OnCreditNoteIssuedHook`, `OnInvoiceRevisedHook` | Credit note and invoice revision events |

> For complete interface definitions, see [Plugin Hooks Reference](../api/plugin-hooks.md).

## Invoice Generation Pipeline

The core guarantees this execution order structurally — it does **not** depend on Priority values:

```
1. InvoiceLifecycleHook.BeforeCalculation()   ← ctx.Subtotal() is ZERO here
2. Subtotal populated on context (core, by contract type)
                                              ← ctx.Subtotal() now returns the base price
3. DiscountHook.CalculateDiscount()           ← all DiscountHooks, priority-ordered
   → Discount cap guard (discount ≤ subtotal)
4. Subtotal after discount (core)
5. TaxHook.CalculateTax()                     ← on ctx.SubtotalAfterDiscount()
6. Total (core: afterDiscount + tax)
7. Credit ledger consumption (core, FIFO, in tx)
8. Invoice created as draft (in tx)
9. InvoiceLifecycleHook.AfterCalculation()    ← fired BEFORE Save (invoice not yet persisted)
10. Save (core, in tx)
```

This means a `TaxHook` can never run before `DiscountHook`, regardless of Priority settings.

:::warning Plugin-observable subtleties
- During `BeforeCalculation`, `ctx.Subtotal()` returns **zero** — the core only sets the
  subtotal on the context *after* this hook. `ctx.ProductID()` is already available. Do
  base-price-dependent work in `DiscountHook` or later, not in `BeforeCalculation`.
- `AfterCalculation` fires **before** the invoice is saved. For persistence-dependent
  work use `OnInvoiceIssuedHook`, which fires after the save in `FinalizeInvoice`.
:::

## Priority

Priority controls execution order **within the same hook type**. Lower number = higher priority.

| Constant | Value | Typical Use |
|----------|-------|-------------|
| `PriorityHighest` | 0 | Audit logging, validation |
| `PriorityHigh` | 100 | Core business logic |
| `PriorityNormal` | 500 | Default plugins |
| `PriorityLow` | 900 | Post-processing (tax) |
| `PriorityLowest` | 1000 | Cleanup |

For example, if you have two `DiscountHook` plugins — a volume discount at priority 100 and a coupon at priority 500 — the volume discount runs first.

## Plugin Registry

Plugins are auto-classified by the interfaces they implement:

```go
registry := plugin.NewRegistry()

registry.Register(myDiscountPlugin)  // auto-registered as DiscountHook
registry.Register(myTaxPlugin)       // auto-registered as TaxHook
registry.Register(myAuditPlugin)     // auto-registered as InvoiceLifecycleHook

// Initialize all with config
registry.InitializeAll(ctx, map[string]plugin.Config{
    "my-discount": {"percentage": 10},
})

// Retrieve hooks by type (priority-ordered)
discountHooks := registry.GetDiscountHooks()
```

A single plugin can implement multiple hook interfaces. For example, a billing plugin implementing both `DiscountHook` and `TaxHook` will be registered in both categories.

## Transactional Outbox

Separately from the 23 hooks, the core can call two *integrator ports* —
`PaymentOutboxWriter` and `InvoiceOutboxWriter` — *inside* the payment/invoice
transaction so you can write a durable notification row atomically with the
write (issue #248). See [`docs/internals/plugin-system.md`](../internals/plugin-system.md) §11 for the full design.

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
- Stacking control

```go
couponPlugin := coupon.NewCouponPlugin(couponRepo, clock)
```

### InvoiceCleanup Plugin

Handles cleanup of draft and stale invoices:

```go
cleanupPlugin := invoicecleanup.NewInvoiceCleanupPlugin(invoiceRepo)
```

## Next Steps

- [Plugin Hooks Reference](../api/plugin-hooks.md) — Complete interface definitions and context APIs
- [Creating Custom Plugins](../guides/custom-plugin.md) — Step-by-step implementation guide
- [Plugin Pipeline Example](../examples/plugin-pipeline.md) — See multiple plugins composing together
