# Contract Billing Core

[![Go Reference](https://pkg.go.dev/badge/github.com/contract-to-cash/core.svg)](https://pkg.go.dev/github.com/contract-to-cash/core)
[![CI](https://github.com/contract-to-cash/core/actions/workflows/ci.yml/badge.svg)](https://github.com/contract-to-cash/core/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

[日本語](README.ja.md) | [Documentation](https://contract-to-cash.github.io/core/)

An event-sourced billing engine with a plugin architecture for SaaS and subscription businesses. Provides domain models, billing pipelines, and extension points — you bring your own database and payment gateway.

## Features

- **Event Sourcing** — Full audit trail, temporal queries, snapshot recovery
- **Plugin Architecture** — Extend via discount, tax, lifecycle, payment, and metrics hooks (ISP-compliant)
- **Product/Price Separation** — Stripe-style immutable prices with grandfathering support
- **Multiple Billing Models** — One-time, subscription, and usage-based (flat, tiered, volume)
- **Contract Renewal** — Auto-renewal with pending price promotion (`pendingPriceID`)
- **Payment Gateway Abstraction** — Charge, authorize/capture, refund, hierarchical method fallback
- **Credit Ledger** — FIFO-based credits for prorations, cancellations, and adjustments
- **Temporal Queries** — Reconstruct contract state at any past point in time

## Quick Start

```bash
go get github.com/contract-to-cash/core
```

Requires **Go 1.25** or later.

```go
package main

import (
    "context"
    "math/big"
    "time"

    "github.com/contract-to-cash/core/application/service"
    "github.com/contract-to-cash/core/domain/contract"
    "github.com/contract-to-cash/core/domain/balance"
    "github.com/contract-to-cash/core/domain/pricing"
    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/eventstore"
    "github.com/contract-to-cash/core/infrastructure/inmemory"
    "github.com/contract-to-cash/core/plugin"
    "github.com/contract-to-cash/core/plugins/tax"
)

func main() {
    ctx := context.Background()
    clock := shared.FixedClock{FixedTime: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}

    // Infrastructure (replace with your own in production)
    es := inmemory.NewInMemoryEventStore(clock)
    contractRepo := inmemory.NewInMemoryContractRepository(es, clock)
    invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
    balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
    usageRepo := inmemory.NewInMemoryUsageRepository()
    priceRepo := inmemory.NewInMemoryPriceRepository()
    productRepo := inmemory.NewInMemoryProductRepository()

    // Register plugins
    registry := plugin.NewRegistry()
    registry.Register(tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{}))
    registry.InitializeAll(ctx, map[string]plugin.Config{
        "tax": {"priority": plugin.PriorityLow},
    })
    defer registry.ShutdownAll(ctx)

    // Create a Price and Contract
    price := shared.NewMoney(new(big.Rat).SetInt64(3000), shared.CurrencyJPY)
    pe := pricing.NewPrice(shared.NewProductID(), price, shared.CurrencyJPY, pricing.BillingCycleMonthly, nil)
    priceRepo.Save(ctx, pe)

    cID := shared.NewContractID()
    agg := contract.NewContractAggregate(cID, clock)
    agg.Create(contract.CreateContractCommand{
        AccountID: shared.AccountID("acct-001"), PlanID: shared.PlanID("plan-std"),
        PriceID: pe.ID(), ContractType: contract.ContractTypeSubscription,
        BillingCycle: contract.BillingCycleMonthly, Price: price, BasePrice: price,
    }, eventstore.EventMetadata{UserID: "system"})
    agg.Activate(eventstore.EventMetadata{UserID: "system"})
    contractRepo.Save(ctx, agg)

    // Generate invoice (¥3,000 + 10% tax = ¥3,300)
    bs := service.NewBillingService(
        contractRepo, invoiceRepo, usageRepo,
        balance.BalanceConfig{},
        priceRepo, productRepo, registry,
        service.BillingConfig{DaysUntilDue: 30}, clock,
        service.WithBalanceRepo(balanceRepo),
    )
    inv, _ := bs.GenerateInvoice(ctx, cID, agg.CurrentPeriod())
    // inv.Total() => ¥3,300
    _ = inv
}
```

## Examples

All examples use in-memory implementations and require no external dependencies.

```bash
go run ./examples/billing-demo/           # Full contract-to-cash flow
go run ./examples/event-sourcing-demo/    # Time travel & snapshots
go run ./examples/plugin-pipeline-demo/   # Multi-plugin billing pipeline
go run ./examples/lifecycle-demo/         # Trial, suspend, cancel, credits
go run ./examples/hosting-integration-demo/ # Provisioning via lifecycle hooks
go run ./examples/multi-service-demo/     # Multi-service routing
go run ./examples/pricing-models-demo/    # Flat, tiered, volume, usage-based
```

## Architecture

```
domain/           # Entities, value objects, aggregates (zero dependencies)
├── contract/     # Event-sourced contract aggregate
├── invoice/      # Invoice entity
├── payment/      # Payment entity
├── balance/       # Credit ledger
├── usage/        # Usage records
├── pricing/      # Immutable Price entity, pricing models
├── product/      # Product entity
└── shared/       # Money, DateRange, IDs, Clock, errors

application/      # Services, ports, queries
├── service/      # BillingService, PaymentService, SnapshotService
├── port/         # PaymentGateway interface
├── query/        # TemporalQueryService
└── projection/   # Event projections

plugin/           # Hook interfaces and registry
plugins/          # Official plugins (tax, coupon, invoicecleanup)
eventstore/       # Event store interface, aggregate base, snapshots
infrastructure/   # In-memory implementations (for testing/demos)
batch/            # Batch processors (contract renewal)
```

## Plugin System

Implement only the hooks you need:

| Category | Hooks | Purpose |
|----------|-------|---------|
| **Billing** | `DiscountHook`, `TaxHook`, `InvoiceLifecycleHook` | Discounts, tax, pre/post calculation |
| **Contract** | `OnContractCreate/Activate/Suspend/Resume/Cancel/Renew/TrialEndHook` | Lifecycle reactions |
| **Payment** | `BeforeChargeHook`, `AfterChargeHook`, `OnPaymentFailedHook`, `OnRefundHook` | Payment flow |
| **Metrics** | `OnContractChangeHook`, `OnInvoiceIssuedHook`, `OnPaymentProcessedHook` | KPI collection |
| **Invoice Gen** | `InvoiceGenerationHook` | PDF rendering, delivery |

Official plugins: **Tax** (Japanese consumption tax), **Coupon** (percentage/fixed, stacking, usage limits), **InvoiceCleanup** (void orphaned invoices on cancellation).

## Documentation

Full documentation is available at **[contract-to-cash.github.io/core](https://contract-to-cash.github.io/core/)** (English & Japanese).

| Section | Description |
|---------|-------------|
| [Introduction](https://contract-to-cash.github.io/core/docs/introduction) | Overview and design principles |
| [Quick Start](https://contract-to-cash.github.io/core/docs/quick-start) | Installation and first billing flow |
| [Architecture](https://contract-to-cash.github.io/core/docs/architecture) | System design deep dive |
| [Core Concepts](https://contract-to-cash.github.io/core/docs/concepts/domain-model) | Domain model, event sourcing, plugins, payment gateway |
| [Guides](https://contract-to-cash.github.io/core/docs/guides/integration) | Integration, custom plugins, temporal queries |
| [API Reference](https://contract-to-cash.github.io/core/docs/api/domain-types) | Types, services, hooks, event store |

## Contributing

Contributions are welcome! Please open an issue to discuss your idea before submitting a pull request.

```bash
make lint   # Run linter
make test   # Run tests
```

## License

[MIT](LICENSE)
