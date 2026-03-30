---
sidebar_position: 3
---

# Billing and Pricing

This guide covers billing models, invoice generation, and the calculation pipeline.

## Pricing Models

Pricing model is defined on the Price entity, not the contract. Multiple prices can exist for the same product (monthly/yearly, different tiers).

### Flat Pricing

Simple fixed amount per billing period:

```go
price := pricing.NewPrice(productID, moneyJPY(3000), shared.CurrencyJPY,
    pricing.BillingCycleMonthly, nil)
// ¥3,000/month regardless of usage
```

### Tiered Pricing

Different rates for different usage tiers (each tier priced independently):

```go
model := pricing.NewTieredPrice([]pricing.Tier{
    {UpTo: 100, UnitPrice: moneyJPY(10)},    // First 100: ¥10/unit
    {UpTo: 500, UnitPrice: moneyJPY(8)},     // 101-500: ¥8/unit
    {UpTo: 0, UnitPrice: moneyJPY(5)},       // 501+: ¥5/unit (unlimited)
})
// 250 units = (100 × ¥10) + (150 × ¥8) = ¥2,200
```

### Volume Pricing

Single rate based on total volume (all units priced at the tier they fall into):

```go
model := pricing.NewVolumePrice([]pricing.Tier{
    {UpTo: 100, UnitPrice: moneyJPY(10)},    // 1-100 units: ¥10/unit
    {UpTo: 500, UnitPrice: moneyJPY(8)},     // 101-500 units: ¥8/unit
    {UpTo: 0, UnitPrice: moneyJPY(5)},       // 501+ units: ¥5/unit
})
// 250 units = 250 × ¥8 = ¥2,000 (all at the ¥8 tier)
```

### Usage-Based

Charges based on metered usage with an included quantity:

```go
product := product.NewProduct("API Service",
    product.WithUsageMetrics([]product.UsageMetric{
        {Name: "api_calls", IncludedQuantity: 1000},
    }),
)
price := pricing.NewPrice(product.ID(), moneyJPY(5000), shared.CurrencyJPY,
    pricing.BillingCycleMonthly, pricingModel)
// ¥5,000 base + usage charges for API calls beyond 1,000
```

Usage metrics are defined on the Product, pricing rules on the Price. Per-contract price overrides allow custom negotiated pricing.

## Invoice Generation

`BillingService.GenerateInvoice()` executes a 14-step pipeline. See [Architecture](./architecture#invoice-generation-pipeline) for the full step breakdown.

### Example: Billing Flow with Tax

```go
// Register tax plugin (10% Japanese consumption tax)
registry := plugin.NewRegistry()
taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})
registry.Register(taxPlugin)
registry.InitializeAll(ctx, map[string]plugin.Config{
    "tax": {"priority": plugin.PriorityLow},
})

// Generate invoice for a ¥3,000/month contract
inv, _ := billingService.GenerateInvoice(ctx, contractID, agg.CurrentPeriod())
// Subtotal:  ¥3,000
// Tax (10%): ¥300
// Total:     ¥3,300
```

### Plugin Pipeline Example

When multiple plugins are registered, they compose into a calculation pipeline with priority-based execution:

| Priority | Plugin | Type | Action |
|----------|--------|------|--------|
| 0 | audit-log | InvoiceLifecycleHook | Logs calculation flow |
| 500 | coupon | DiscountHook | 10% coupon discount |
| 500 | loyalty-discount | DiscountHook | 5% loyalty discount |
| 900 | tax | TaxHook | 10% consumption tax |

For a ¥10,000/month contract:

```
1. [AuditLog] BeforeCalculation (logs start)
2. Subtotal:             ¥10,000
3. [Coupon] -10%:        -¥1,000
4. [Loyalty] -5%:        -¥500
5. After discounts:      ¥8,500
6. [Tax] +10%:           +¥850
7. Total:                ¥9,350
8. [AuditLog] AfterCalculation (logs result)
```

Key points:
- Plugins execute in priority order (lower number = higher priority)
- Multiple DiscountHooks stack — each applies to the original subtotal
- Tax is calculated on the post-discount amount
- InvoiceLifecycleHook provides before/after visibility into the pipeline

## Running the Examples

```bash
# Basic billing flow
go run ./examples/billing-demo/

# Pricing models (flat, tiered, volume, usage)
go run ./examples/pricing-models-demo/

# Plugin pipeline with multiple hooks
go run ./examples/plugin-pipeline-demo/
```
