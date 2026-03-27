---
sidebar_position: 3
---

# Plugin Pipeline Demo

This example shows how multiple plugins compose into a billing calculation pipeline with priority-based execution.

## Running the Example

```bash
go run ./examples/plugin-pipeline-demo/
```

## Plugin Setup

Four plugins are registered with different priorities:

| Priority | Plugin | Type | Action |
|----------|--------|------|--------|
| 0 | audit-log | InvoiceLifecycleHook | Logs calculation flow |
| 500 | coupon | DiscountHook | 10% coupon discount |
| 500 | loyalty-discount | DiscountHook | 5% loyalty discount |
| 900 | tax | TaxHook | 10% consumption tax |

## Calculation Pipeline

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

## Custom Plugin Example

The demo includes a custom `loyaltyDiscountPlugin`:

```go
type loyaltyDiscountPlugin struct {
    priority int
}

func (p *loyaltyDiscountPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
    rate := new(big.Rat).SetFrac64(5, 100) // 5%
    discount := ctx.Subtotal().Multiply(rate)
    ctx.RecordDiscount(plugin.AppliedDiscount{
        PluginName: "loyalty-discount",
        Code:       "LOYALTY5",
        Amount:     discount,
    })
    return discount, nil
}
```

## Key Takeaways

- Plugins execute in priority order (lower number = higher priority)
- Multiple DiscountHooks stack — each applies to the original subtotal
- Tax is calculated on the post-discount amount
- InvoiceLifecycleHook provides before/after visibility into the pipeline
