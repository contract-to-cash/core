---
sidebar_position: 2
---

# Quick Start

Get up and running with Contract Billing Core in minutes.

## Installation

```bash
go get github.com/contract-to-cash/core
```

Requires Go 1.22 or later.

## Basic Billing Flow

This example demonstrates the complete contract-to-cash flow: create a contract, generate an invoice with tax, and process payment.

### 1. Set Up Infrastructure

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

    // Infrastructure (replace with your own implementations in production)
    eventStore := inmemory.NewInMemoryEventStore(clock)
    contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
    invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
    balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
    usageRepo := inmemory.NewInMemoryUsageRepository()
    priceRepo := inmemory.NewInMemoryPriceRepository()
    productRepo := inmemory.NewInMemoryProductRepository()
```

### 2. Register Plugins

```go
    // Register tax plugin (10% Japanese consumption tax)
    registry := plugin.NewRegistry()
    taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})
    registry.Register(taxPlugin)
    registry.InitializeAll(ctx, map[string]plugin.Config{
        "tax": {"priority": plugin.PriorityLow},
    })
    defer registry.ShutdownAll(ctx)
```

### 3. Create a Contract

```go
    // Create a Price entity
    price := shared.NewMoney(new(big.Rat).SetInt64(3000), shared.CurrencyJPY)
    priceEntity := pricing.NewPrice(
        shared.NewProductID(), price, shared.CurrencyJPY,
        pricing.BillingCycleMonthly, nil,
    )
    priceRepo.Save(ctx, priceEntity)

    // Create and activate a subscription contract
    contractID := shared.NewContractID()
    agg := contract.NewContractAggregate(contractID, clock)
    agg.Create(contract.CreateContractCommand{
        AccountID:    shared.AccountID("acct-001"),
        PlanID:       shared.PlanID("plan-standard"),
        PriceID:      priceEntity.ID(),
        ContractType: contract.ContractTypeSubscription,
        BillingCycle: contract.BillingCycleMonthly,
        Price:        price,
        BasePrice:    price,
    }, eventstore.EventMetadata{UserID: "system"})
    agg.Activate(eventstore.EventMetadata{UserID: "system"})
    contractRepo.Save(ctx, agg)
```

### 4. Generate an Invoice

```go
    billingService := service.NewBillingService(
        contractRepo, invoiceRepo, usageRepo, balanceRepo,
        credit.BalanceConfig{},
        priceRepo, productRepo, registry,
        service.BillingConfig{DaysUntilDue: 30},
        clock,
    )

    inv, _ := billingService.GenerateInvoice(ctx, contractID, agg.CurrentPeriod())
    // inv.Subtotal()       => ¥3,000
    // inv.TaxAmount()      => ¥300 (10% tax)
    // inv.Total()          => ¥3,300
```

### 5. Process Payment

```go
    paymentService := service.NewPaymentService(
        myGateway, paymentRepo, invoiceRepo, contractRepo,
        nil, eventStore, registry, clock,
    )

    inv.Finalize()
    invoiceRepo.Save(ctx, inv)

    payment, _ := paymentService.ProcessPayment(ctx, inv.ID(),
        service.ProcessPaymentInput{
            PaymentMethodID: "pm-visa-1234",
            Amount:          inv.AmountDue(),
            Currency:        shared.CurrencyJPY,
            IdempotencyKey:  "pay-001",
        },
    )
    // payment.Status() => "completed"
}
```

## Running the Examples

The repository includes 7 runnable examples:

```bash
# Basic billing flow
go run ./examples/billing-demo/

# Event sourcing time travel
go run ./examples/event-sourcing-demo/

# Plugin pipeline with multiple hooks
go run ./examples/plugin-pipeline-demo/

# Contract lifecycle (trial, suspend, cancel)
go run ./examples/lifecycle-demo/

# External service provisioning via hooks
go run ./examples/hosting-integration-demo/

# Multi-service routing
go run ./examples/multi-service-demo/

# Pricing models (flat, tiered, volume, usage)
go run ./examples/pricing-models-demo/
```

All examples use in-memory implementations and require no external dependencies.

## Next Steps

- [Architecture](./architecture) — Understand the system design
- [Integration Guide](./guides/integration) — How to integrate into your service
- [Custom Plugin Guide](./guides/custom-plugin) — Build your own plugins
