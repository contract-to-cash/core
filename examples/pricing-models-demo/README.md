# Pricing Models Demo

Compares four pricing models side by side with the same usage amounts, showing how each model calculates differently.

## What You'll See

```
  Usage     Flat          Graduated     Volume        Usage-Based
  --------------------------------------------------------------
  0         ¥5000         ¥0            ¥0            ¥500
  100       ¥5000         ¥1000         ¥1000         ¥5000
  1000      ¥5000         ¥10000        ¥10000        ¥50000
  3500      ¥5000         ¥30000        ¥28000        ¥50000
  10000     ¥5000         ¥67000        ¥50000        ¥50000
```

## Pricing Models

### Flat
Fixed price regardless of usage. Best for simple subscriptions.

### Graduated Tiered
Each tier's usage is charged at that tier's rate. Like income tax brackets.

```
3,500 calls:
  Tier 1: 1,000 x ¥10 = ¥10,000
  Tier 2: 2,500 x ¥8  = ¥20,000
  Total:                 ¥30,000
```

### Volume Tiered
Total usage determines which single rate applies to ALL units. Rewards high usage.

```
3,500 calls -> falls in tier 2
  All 3,500 x ¥8 = ¥28,000
```

### Usage-Based with Min/Max
Per-unit price clamped between a minimum and maximum. Guarantees revenue floor and cost ceiling.

```
¥50/GB, min ¥500, max ¥50,000

    3 GB -> ¥150 -> clamped to ¥500   (minimum)
  100 GB -> ¥5,000                    (normal)
2,000 GB -> ¥100,000 -> clamped to ¥50,000 (maximum)
```

## Key Concepts

| Concept | Description |
|---------|-------------|
| **PricingModel interface** | `CalculatePrice(usage int64) Money` -- all models share one interface |
| **FlatPrice** | Ignores usage, returns fixed amount |
| **TieredPrice** | Supports both Graduated and Volume modes |
| **UsagePrice** | Per-unit with optional min/max clamps |
| **Composable** | Assign any model to a Plan; mix models across usage metrics |

## Run

```bash
go run ./examples/pricing-models-demo/
```
