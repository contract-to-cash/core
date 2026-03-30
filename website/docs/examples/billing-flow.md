---
sidebar_position: 1
---

# Basic Billing Flow

This example demonstrates the simplest contract-to-cash pattern:
1. Create a subscription contract (Draft)
2. Register a tax plugin
3. Activate the contract
4. Generate and finalize an invoice
5. Process payment
6. View event history

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

### Contract Creation (Draft)

Creates a ¥3,000/month subscription in Draft status:

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
contractRepo.Save(ctx, agg)
```

### Activate

The contract is activated, transitioning from Draft to Active:

```go
agg.Activate(metadata)  // Contract: Draft → Active
contractRepo.Save(ctx, agg)
```

### Invoice Generation

`BillingService` generates an invoice with tax automatically applied by the registered plugin:

```
Subtotal:  ¥3,000
Tax (10%): ¥300
Total:     ¥3,300
```

### Payment Processing

Payment is processed via a mock gateway, and the invoice transitions to Paid:

```go
paymentService.ProcessPayment(ctx, inv.ID(), service.ProcessPaymentInput{
    PaymentMethodID: "pm-visa-1234",
    Amount:          inv.AmountDue(),
    Currency:        shared.CurrencyJPY,
    IdempotencyKey:  "demo-pay-001",
})
// Invoice: Finalized → Paid
```

### Event History

The event store records every contract operation:

```
[1] contract.created   (v1) at 2026-04-01
[2] contract.activated (v2) at 2026-04-01
```

## Key Takeaways

- The billing pipeline automatically applies registered plugins (tax, discounts)
- All operations are recorded as immutable events
- The invoice tracks subtotal, discount, tax, and credit breakdown
- Payment processing is decoupled from billing via the gateway interface

## Payment-Gated Provisioning

For use cases where services should only be provisioned after payment clears (e.g., hosting), add Suspend/Resume steps:

```
Draft → Invoice(draft) → Activate + Finalize → Suspend(awaiting payment) → Pay → Resume → Active
```

The `Suspended` state serves as a unified "service inactive" state for both initial payment pending and non-payment suspension. See [Architecture: Payment-Gated Provisioning](../architecture.md) and [Issue #5](https://github.com/contract-to-cash/core/issues/5) for the full design discussion.
