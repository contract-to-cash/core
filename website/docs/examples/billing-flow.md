---
sidebar_position: 1
---

# Basic Billing Flow

This example demonstrates the complete contract-to-cash flow:
1. Create a subscription contract
2. Register a tax plugin
3. Generate an invoice
4. Process payment
5. View event history

## Running the Example

```bash
go run ./examples/billing-demo/
```

## What It Does

### Setup

Creates in-memory infrastructure and registers a 10% Japanese consumption tax plugin:

```go
registry := plugin.NewRegistry()
taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})
registry.Register(taxPlugin)
registry.InitializeAll(ctx, map[string]plugin.Config{
    "tax": {"priority": plugin.PriorityLow},
})
```

### Contract Creation

Creates a ¥3,000/month subscription and activates it:

```go
agg := contract.NewContractAggregate(contractID, clock)
agg.Create(contract.CreateContractCommand{
    AccountID:    shared.AccountID("acct-demo-001"),
    PlanID:       shared.PlanID("plan-standard"),
    PriceID:      priceEntity.ID(),
    ContractType: contract.ContractTypeSubscription,
    BillingCycle: contract.BillingCycleMonthly,
    Price:        moneyJPY(3000),
    BasePrice:    moneyJPY(3000),
}, metadata)
agg.Activate(metadata)
contractRepo.Save(ctx, agg)
```

### Invoice Generation

`BillingService` generates the invoice with tax automatically applied:

```
Subtotal:  ¥3,000
Tax (10%): ¥300
Total:     ¥3,300
```

### Payment Processing

Payment is processed via a mock gateway, and the invoice status updates to `paid`.

### Event History

The event store records every operation:

```
[1] contract.created (v1) at 2026-04-01
[2] contract.activated (v2) at 2026-04-01
```

## Key Takeaways

- The billing pipeline automatically applies registered plugins (tax, discounts)
- All operations are recorded as immutable events
- The invoice tracks subtotal, discount, tax, and credit breakdown
- Payment processing is decoupled from billing via the gateway interface
