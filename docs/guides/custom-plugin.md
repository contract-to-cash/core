---
sidebar_position: 2
---

# Custom Plugin Guide

Learn how to build custom plugins to extend billing behavior.

## Basic Structure

Every plugin implements the `Plugin` interface, plus one or more hook interfaces:

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

## Example: Loyalty Discount Plugin

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

## Example: Audit Log Plugin

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

## Example: Hosting Provisioning Plugin

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
The `OnContractResume` hook cannot distinguish between initial provisioning (first payment) and re-activation (payment after suspension). Consider tracking provisioning state externally and checking it in `OnContractResume`:

```go
func (p *HostingPlugin) OnContractResume(ctx *plugin.Context, c *contract.ContractAggregate) error {
    if p.provisioningClient.Exists(ctx.Context(), c.ContractID()) {
        return p.provisioningClient.StartServer(ctx.Context(), c.ContractID())
    }
    return p.provisioningClient.CreateServer(ctx.Context(), c.ContractID(), c.PlanID())
}
```
:::

## Registering Custom Plugins

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

## Multi-Hook Plugins

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

## Plugin Execution Order

The core guarantees the invoice generation pipeline order structurally (Discount → Tax → Total). Priority only controls execution within the same hook type.

> For the complete pipeline diagram, see [Plugin System — Invoice Generation Pipeline](../concepts/plugin-system.md#invoice-generation-pipeline).
