---
sidebar_position: 5
---

# Pricing Models Demo

This example demonstrates the different pricing models supported by the library.

## Running the Example

```bash
go run ./examples/pricing-models-demo/
```

## Supported Models

### Flat Pricing

Simple fixed amount per billing period:

```go
price := pricing.NewPrice(productID, moneyJPY(3000), shared.CurrencyJPY,
    pricing.BillingCycleMonthly, nil, createdAt)
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
    pricing.BillingCycleMonthly, pricingModel, createdAt)
// ¥5,000 base + usage charges for API calls beyond 1,000
```

## Key Takeaways

- Pricing model is defined on the Price entity, not the contract
- Multiple prices can exist for the same product (monthly/yearly, different tiers)
- Per-contract price overrides allow custom negotiated pricing
- Usage metrics are defined on the Product, pricing rules on the Price
