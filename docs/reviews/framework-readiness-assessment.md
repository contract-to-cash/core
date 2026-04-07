# Framework Readiness Assessment

**Date**: 2026-04-07
**Scope**: Gaps between current state (reference implementation) and production-ready framework
**Baseline**: codebase-review-20260327.md + subsequent PRs (#50-#62)

---

## Review Item Status (from codebase-review-20260327)

| # | Severity | Issue | Status | Resolution |
|---|----------|-------|--------|------------|
| 1 | High | `NewUsageRecord` accepts negative quantity | **Resolved** | PR #58 — returns `DomainError` |
| 2 | High | `UsagePrice.CalculatePrice` returns negative on negative usage | **Resolved** | Guard added: `usage <= 0` returns zero |
| 3 | High | `NewLineItem` accepts negative quantity | **Resolved** | PR #58 — returns `(LineItem, error)` |
| 4 | High | `domain/payment` state transitions untested | **Resolved** | PR #59 — coverage 35% → 96% |
| 5 | High | `application/port/webhook.go` untested | **Resolved** | PR #59 — 13 tests, coverage 0% → 73% |
| 6 | High | `application/query/` TemporalQueryService untested | **Open** | See Issue proposal below |
| 7 | High | `application/projection/` ProjectionService untested | **Resolved** | PR #59 — async/sync mode tests |
| 8 | Medium | `Product.AddFeature/AddUsageMetric` no dedup | **Resolved** | Name-based dedup added |
| 9 | Medium | `BalanceEntry.sourceType` raw string | **Open** | See Issue proposal below |
| 10 | Medium | `UsageRecord.metricName` raw string | **Open** | See Issue proposal below |
| 11 | Medium | `infrastructure/inmemory/` 7 files untested | **Partial** | 4/8 repos tested; 4 remain |

---

## New Gaps Identified (not in original review)

### Gap 1: Service-layer logging is wired but unused
- `slog.Logger` injected via `WithBillingLogger` options pattern
- **Zero structured log calls** in `billing_service.go`, `payment_service.go`, `credit_note_service.go`
- Critical operations (invoice generation, payment processing, credit application) are silent

### Gap 2: OpenTelemetry instrumentation absent
- `go.opentelemetry.io/*` present in go.mod (indirect)
- No span creation, no metric counters anywhere in codebase
- No trace propagation through plugin hooks or event store operations

### Gap 3: No benchmark tests
- Zero `Benchmark*` functions across 52 test files
- No performance baselines for: invoice generation, event replay, plugin hook chain, concurrent credit consumption

### Gap 4: No SQL migration or reference DB implementation
- `infrastructure/inmemory/` only — suitable for demo/test
- No PostgreSQL/MySQL reference implementation
- No migration files (SQL DDL documented in event-sourcing.md but not provided as files)
- Users must implement 8+ repository interfaces from scratch

### Gap 5: No configuration validation
- `BillingConfig` fields (`GracePeriod`, `DaysUntilDue`, `CollectionMethod`) are unvalidated
- Zero/negative values silently accepted
- No `NewBillingConfig()` constructor with validation

---

## Proposed GitHub Issues

See individual issue files in `docs/reviews/issues/` directory.
