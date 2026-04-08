---
sidebar_label: Performance
---

# Performance Baseline

Benchmark results measured on Apple M4 Pro (darwin/arm64), Go 1.25, `make bench`.

## Invoice Generation (`application/service/`)

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| GenerateInvoice_Subscription | 1,527 | 2,940 | 84 |
| GenerateInvoice_UsageBased | 13,114 | 2,845 | 49 |
| GenerateInvoice_WithPlugins (3 discount) | 2,091 | 4,141 | 120 |
| GenerateInvoice_WithCredits (10 entries) | 1,517 | 3,004 | 85 |

## Event Replay (`domain/contract/`)

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| LoadFromHistory_10Events | 9,235 | 4,643 | 111 |
| LoadFromHistory_100Events | 69,181 | 35,623 | 786 |
| LoadFromHistory_1000Events | 642,212 | 345,445 | 7,536 |
| MarshalSnapshot | 1,944 | 1,386 | 18 |
| LoadFromSnapshot | 4,568 | 2,177 | 55 |
| LoadFromSnapshot_Then100Events | 68,636 | 36,600 | 805 |

## Pricing Models (`domain/pricing/`)

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| FlatPrice_CalculatePrice | 0.25 | 0 | 0 |
| TieredPrice_Graduated_10Tiers | 3,219 | 5,019 | 232 |
| TieredPrice_Volume_10Tiers | 245 | 344 | 19 |
| UsagePrice_Simple | 125 | 216 | 10 |
| UsagePrice_WithMinMax | 212 | 408 | 14 |

## Credit Consumption (`domain/balance/`)

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| Consume_Sequential (100 ops) | 22,287 | 43,206 | 1,500 |
| Consume_FullyConsumed | 196 | 400 | 11 |
| IsExpired | 1.4 | 0 | 0 |

## Plugin Dispatch (`plugin/`)

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| GetDiscountHooks_10Plugins (parallel) | 147 | 248 | 4 |
| Register_Sequential (20 plugins) | 1,636 | 3,656 | 14 |
| GetDiscountHooks_ConcurrentReadWrite | 745 | 1,877 | 4 |

## Event Store & Repository (`infrastructure/inmemory/`)

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| EventStore_Append | 369 | 560 | 2 |
| EventStore_Append_Batch10 | 1,449 | 7,120 | 6 |
| EventStore_Load_100Events | 2,940 | 20,480 | 1 |
| EventStore_SaveSnapshot | 72 | 571 | 0 |
| EventStore_LoadSnapshot | 31 | 96 | 1 |
| BalanceRepository_FindAvailable_100Entries | 1,605 | 2,224 | 10 |

## How to Run

```bash
make bench                    # Run all benchmarks
make bench | grep Benchmark   # Summary only
```

For statistical comparison, use [benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat):

```bash
go test -bench=. -benchmem -count=5 ./... > old.txt
# ... make changes ...
go test -bench=. -benchmem -count=5 ./... > new.txt
benchstat old.txt new.txt
```
