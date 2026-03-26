# Plugin Pipeline Demo

Shows how multiple plugins compose into a billing calculation pipeline with priority-based execution. Includes two custom plugins written from scratch.

## What You'll See

```
  Registered Plugins:
    [Priority 0]   audit-log        (InvoiceLifecycleHook)
    [Priority 500] coupon            (DiscountHook)
    [Priority 500] loyalty-discount  (DiscountHook)
    [Priority 900] tax               (TaxHook)

  >> [AuditLog] BeforeCalculation: ContractID=...
  >> [Coupon] Usage recorded for coupon SAVE10
  >> [Loyalty] 5% discount on ¥10000 = -¥500
  >> [AuditLog] AfterCalculation: Total=¥9350, Discounts=2 applied

  Subtotal:              ¥10,000
  Discount (coupon+loyalty): -¥1,500
  After Discounts:       ¥8,500
  Tax (10%):             +¥850
  Total:                 ¥9,350
```

## Plugins in This Demo

| Plugin | Type | Priority | Behavior |
|--------|------|----------|----------|
| **AuditLogPlugin** (custom) | InvoiceLifecycleHook | 0 (highest) | Logs before/after calculation |
| **CouponPlugin** (built-in) | DiscountHook | 500 | Applies 10% coupon "SAVE10" |
| **LoyaltyDiscountPlugin** (custom) | DiscountHook | 500 | 5% loyalty discount |
| **TaxPlugin** (built-in) | TaxHook | 900 (low) | 10% Japanese consumption tax |

## Key Concepts

| Concept | Description |
|---------|-------------|
| **Priority ordering** | Lower number = runs first. Tax runs after discounts by design |
| **Hook composition** | Multiple DiscountHooks stack; each sees the original subtotal |
| **Custom plugin** | Just implement the interface -- no registration boilerplate |
| **CalculationContext** | Type-safe context passed through the pipeline |

## How to Write a Custom Plugin

```go
type MyPlugin struct{ priority int }

func (p *MyPlugin) Name() string    { return "my-plugin" }
func (p *MyPlugin) Version() string { return "1.0.0" }
func (p *MyPlugin) Priority() int   { return p.priority }
func (p *MyPlugin) Initialize(_ context.Context, config plugin.Config) error { return nil }
func (p *MyPlugin) Shutdown(_ context.Context) error { return nil }

// Implement one or more hook interfaces:
func (p *MyPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
    // your logic here
}
```

## Run

```bash
go run ./examples/plugin-pipeline-demo/
```
