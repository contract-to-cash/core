# Introduce typed strings for BalanceEntry.sourceType and UsageRecord.metricName

**Labels**: `type-safety`, `medium-priority`, `domain`
**Source**: codebase-review-20260327 items #9, #10

## Problem

Two fields use raw `string` where typed strings would prevent silent bugs:

### BalanceEntry.sourceType (`domain/balance/entity.go:27`)
```go
sourceType string  // "proration", "manual", "refund_conversion"
```
`BalanceReason` is already a typed string in the same package. `sourceType` should follow the same pattern. A typo like `"proratoin"` would go undetected.

### UsageRecord.metricName (`domain/usage/entity.go:14`)
```go
metricName string  // "api_calls", "storage_gb", "active_users"
```
No type safety — `"api_calls"` vs `"apiCalls"` vs `"API_CALLS"` would all be treated as different metrics, leading to billing errors.

## Proposal

### For sourceType
```go
// domain/balance/entity.go
type BalanceSourceType string

const (
    BalanceSourceProration        BalanceSourceType = "proration"
    BalanceSourceManual           BalanceSourceType = "manual"
    BalanceSourceRefundConversion BalanceSourceType = "refund_conversion"
)
```

### For metricName
```go
// domain/shared/identifier.go (or domain/usage/entity.go)
type MetricName string
```
This is intentionally NOT an enum — metric names are user-defined. The typed string prevents accidental mixing with other string fields (e.g., passing a description where a metric name is expected).

## Acceptance Criteria

- [ ] `BalanceSourceType` typed string with constants defined in `domain/balance/`
- [ ] `MetricName` typed string defined in `domain/shared/` (shared across usage, product, pricing)
- [ ] All call sites updated (constructors, repository methods, test helpers)
- [ ] `Product.UsageMetric.Name` field type changed to `MetricName`
- [ ] No breaking changes to public API signatures (typed string is assignable from string literal)
- [ ] All tests pass with `-race`

## Impact

- `BalanceEntry`: constructor, `NewBalanceEntry()`, repository `FindAvailable()`
- `UsageRecord`: constructor, repository `GetSummary()`, `GetRecords()`
- `Product.UsageMetric`: `Name` field
- `BillingService.calculateUsageCharge()`: metric iteration
