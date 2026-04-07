# Add benchmark tests for critical paths

**Labels**: `testing`, `performance`, `framework`

## Problem

Zero benchmark tests exist across 52 test files. For a billing framework, users need to understand:

- How fast can invoices be generated under load?
- What is the overhead of the plugin hook chain?
- How does event replay performance degrade with history length?
- What is the cost of concurrent credit consumption (lock contention)?

Without benchmarks, users cannot make informed decisions about:
- Whether to use snapshots (and at what interval)
- How many plugins are practical before latency becomes an issue
- Whether inmemory implementations are suitable for their test suite at scale

## Proposal

### Core Benchmarks

```go
// application/service/billing_service_bench_test.go
func BenchmarkGenerateInvoice_Subscription(b *testing.B)
func BenchmarkGenerateInvoice_UsageBased(b *testing.B)
func BenchmarkGenerateInvoice_WithPlugins(b *testing.B)       // 3 plugins
func BenchmarkGenerateInvoice_WithCredits(b *testing.B)       // 10 credit entries

// domain/contract/aggregate_bench_test.go
func BenchmarkLoadFromHistory_100Events(b *testing.B)
func BenchmarkLoadFromHistory_1000Events(b *testing.B)
func BenchmarkLoadFromHistory_10000Events(b *testing.B)
func BenchmarkLoadFromSnapshot_Then100Events(b *testing.B)
func BenchmarkMarshalSnapshot(b *testing.B)

// plugin/registry_bench_test.go
func BenchmarkGetDiscountHooks_10Plugins(b *testing.B)        // concurrent read
func BenchmarkRegister_ConcurrentReadWrite(b *testing.B)

// domain/balance/entity_bench_test.go
func BenchmarkConsume_Sequential(b *testing.B)
func BenchmarkFindAvailable_100Entries(b *testing.B)

// domain/pricing/model_bench_test.go
func BenchmarkTieredPrice_Graduated_10Tiers(b *testing.B)
func BenchmarkUsagePrice_WithMinMax(b *testing.B)
```

## Acceptance Criteria

- [ ] At least 15 benchmark functions covering: invoice generation, event replay, plugin dispatch, credit consumption, pricing calculation
- [ ] Benchmarks use realistic data (not trivial single-field objects)
- [ ] `make bench` target added to Makefile
- [ ] Results documented in a `docs/performance.md` with baseline numbers
- [ ] Memory allocation benchmarks included (`b.ReportAllocs()`)

## Non-Goals

- Optimizing code in this issue (benchmarks first, optimize later)
- Load testing or distributed stress testing
