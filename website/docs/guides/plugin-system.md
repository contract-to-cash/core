---
sidebar_position: 5
---

# Plugin System

The plugin system lets you extend billing logic through well-defined hooks. Each hook type serves a specific purpose, and you only implement the interfaces you need. For the complete hook interface definitions, see the [Plugin Hooks API Reference](../api/plugin-hooks).

## Overview

Every plugin implements the base `Plugin` interface (Name, Version, Initialize, Shutdown, Priority), plus one or more hook interfaces. Plugins are executed in priority order (lower number = higher priority).

| Priority Constant | Value | Use Case |
|----------|-------|----------|
| `PriorityHighest` | 0 | Audit logging, validation |
| `PriorityHigh` | 100 | Pre-processing, core business logic |
| `PriorityNormal` | 500 | Default plugins |
| `PriorityLow` | 900 | Post-processing (tax) |
| `PriorityLowest` | 1000 | Last resort, cleanup |

## Hook Categories

- **Billing Calculation** — `DiscountHook`, `TaxHook`, `InvoiceLifecycleHook` — participate in the invoice generation pipeline
- **Contract Lifecycle** — `OnContractCreateHook`, `OnContractActivateHook`, `OnContractSuspendHook`, `OnContractResumeHook`, `OnContractCancelHook`, `OnContractRenewHook`, `OnContractTrialEndHook` — react to contract state changes
- **Payment** — `BeforeChargeHook`, `AfterChargeHook`, `OnPaymentFailedHook`, `OnRefundHook` — hook into payment processing
- **Metrics** — `OnContractChangeHook`, `OnInvoiceIssuedHook`, `OnPaymentProcessedHook` — collect KPIs and business metrics
- **Invoice Generation** — `InvoiceGenerationHook` — custom invoice rendering and delivery
- **Credit Note** — `OnCreditNoteIssuedHook`, `OnInvoiceRevisedHook` — react to credit note and invoice revision events

## Building a Custom Plugin

### Basic Structure

```go
package myplugin

import (
    "context"
    "github.com/contract-to-cash/core/plugin"
)

type MyPlugin struct {
    priority int
}

func (p *MyPlugin) Name() string    { return "my-plugin" }
func (p *MyPlugin) Version() string { return "1.0.0" }
func (p *MyPlugin) Priority() int   { return p.priority }

func (p *MyPlugin) Initialize(_ context.Context, config plugin.Config) error {
    if v, ok := config["priority"]; ok {
        if n, ok := v.(int); ok {
            p.priority = n
        }
    }
    return nil
}

func (p *MyPlugin) Shutdown(_ context.Context) error { return nil }
```

### Example: Loyalty Discount Plugin

A plugin that gives a 5% discount to all invoices:

```go
type LoyaltyDiscountPlugin struct {
    priority int
    rate     *big.Rat
}

func NewLoyaltyDiscountPlugin(discountPercent int) *LoyaltyDiscountPlugin {
    return &LoyaltyDiscountPlugin{
        rate: new(big.Rat).SetFrac64(int64(discountPercent), 100),
    }
}

func (p *LoyaltyDiscountPlugin) Name() string    { return "loyalty-discount" }
func (p *LoyaltyDiscountPlugin) Version() string { return "1.0.0" }
func (p *LoyaltyDiscountPlugin) Priority() int   { return p.priority }

func (p *LoyaltyDiscountPlugin) Initialize(_ context.Context, config plugin.Config) error {
    if v, ok := config["priority"]; ok {
        if n, ok := v.(int); ok {
            p.priority = n
        }
    }
    return nil
}

func (p *LoyaltyDiscountPlugin) Shutdown(_ context.Context) error { return nil }

// Implement DiscountHook
func (p *LoyaltyDiscountPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
    discount := ctx.Subtotal().Multiply(p.rate)
    ctx.RecordDiscount(plugin.AppliedDiscount{
        PluginName: "loyalty-discount",
        Code:       "LOYALTY",
        Amount:     discount,
    })
    return discount, nil
}
```

### Example: Audit Log Plugin

A plugin that logs the invoice calculation lifecycle:

```go
type AuditLogPlugin struct {
    priority int
    logger   *slog.Logger
}

// Implement InvoiceLifecycleHook
func (p *AuditLogPlugin) BeforeCalculation(ctx *plugin.CalculationContext) error {
    p.logger.Info("invoice calculation started",
        "contract_id", ctx.ContractID(),
        "subtotal", ctx.Subtotal().Amount().RatString(),
    )
    return nil
}

func (p *AuditLogPlugin) AfterCalculation(ctx *plugin.CalculationContext, inv *invoice.Invoice) error {
    p.logger.Info("invoice calculation completed",
        "contract_id", ctx.ContractID(),
        "total", inv.Total().Amount().RatString(),
        "discounts_applied", len(ctx.AppliedDiscounts()),
    )
    return nil
}
```

### Example: Hosting Provisioning Plugin

A plugin that provisions/deprovisions hosting resources on contract lifecycle events:

```go
type HostingPlugin struct {
    priority           int
    provisioningClient ProvisioningClient
}

// Implement OnContractActivateHook
func (p *HostingPlugin) OnContractActivate(ctx *plugin.Context, c *contract.ContractAggregate) error {
    return p.provisioningClient.CreateServer(ctx.Context(), c.ContractID(), c.PlanID())
}

// Implement OnContractSuspendHook
func (p *HostingPlugin) OnContractSuspend(ctx *plugin.Context, c *contract.ContractAggregate) error {
    return p.provisioningClient.StopServer(ctx.Context(), c.ContractID())
}

// Implement OnContractResumeHook
func (p *HostingPlugin) OnContractResume(ctx *plugin.Context, c *contract.ContractAggregate) error {
    return p.provisioningClient.StartServer(ctx.Context(), c.ContractID())
}

// Implement OnContractCancelHook
func (p *HostingPlugin) OnContractCancel(ctx *plugin.Context, c *contract.ContractAggregate) error {
    return p.provisioningClient.DeleteServer(ctx.Context(), c.ContractID())
}
```

:::caution
The `OnContractResume` hook cannot distinguish between initial provisioning (first payment) and re-activation (payment after suspension). Consider tracking provisioning state externally:

```go
func (p *HostingPlugin) OnContractResume(ctx *plugin.Context, c *contract.ContractAggregate) error {
    if p.provisioningClient.Exists(ctx.Context(), c.ContractID()) {
        return p.provisioningClient.StartServer(ctx.Context(), c.ContractID())
    }
    return p.provisioningClient.CreateServer(ctx.Context(), c.ContractID(), c.PlanID())
}
```

See [Issue #5](https://github.com/contract-to-cash/core/issues/5) for details.
:::

### Multi-Hook Plugins

A single plugin can implement multiple hook interfaces:

```go
type MetricsPlugin struct {
    priority int
    metrics  MetricsCollector
}

// Implements OnContractChangeHook + OnInvoiceIssuedHook + OnPaymentProcessedHook
func (p *MetricsPlugin) OnContractChange(ctx *plugin.Context, event plugin.ContractChangeEvent) error {
    p.metrics.RecordContractChange(event.ChangeType, event.MRRChange)
    return nil
}

func (p *MetricsPlugin) OnInvoiceIssued(ctx *plugin.Context, inv *invoice.Invoice) error {
    p.metrics.RecordRevenue(inv.Total())
    return nil
}

func (p *MetricsPlugin) OnPaymentProcessed(ctx *plugin.Context, pay *payment.Payment) error {
    p.metrics.RecordPayment(pay.Status(), pay.Amount())
    return nil
}
```

## Registering Plugins

```go
registry := plugin.NewRegistry()

// The registry auto-classifies plugins by their interfaces
registry.Register(&LoyaltyDiscountPlugin{})   // → DiscountHook
registry.Register(&AuditLogPlugin{})          // → InvoiceLifecycleHook
registry.Register(&HostingPlugin{})           // → Multiple contract hooks

configs := map[string]plugin.Config{
    "loyalty-discount": {"priority": plugin.PriorityNormal},
    "audit-log":        {"priority": plugin.PriorityHighest},
    "hosting":          {"priority": plugin.PriorityHigh},
}
registry.InitializeAll(ctx, configs)
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

### InvoiceCleanup Plugin

Handles cleanup of draft and stale invoices:

```go
cleanupPlugin := invoicecleanup.NewInvoiceCleanupPlugin(invoiceRepo, clock)
```

## Plugin Execution Order

During invoice generation, plugins execute in this order:

1. **InvoiceLifecycleHook.BeforeCalculation** (priority-ordered)
2. Calculate subtotal
3. **DiscountHook.CalculateDiscount** (priority-ordered, multiple)
4. Cap discounts
5. **TaxHook.CalculateTax** (priority-ordered)
6. Create invoice
7. **InvoiceLifecycleHook.AfterCalculation** (priority-ordered)

```bash
go run ./examples/plugin-pipeline-demo/
```
