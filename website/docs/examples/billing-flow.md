---
sidebar_position: 1
---

# Basic Billing Flow

This example demonstrates the **recommended Payment-Gated Provisioning** flow — the contract-to-cash pattern where service starts only after payment confirmation:
1. Create a subscription contract (Draft)
2. Register a tax plugin
3. Generate a draft invoice
4. Activate and finalize both contract and invoice
5. Suspend the contract (awaiting payment)
6. Process payment and resume the contract
7. View event history

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

### Invoice Generation (Draft)

`BillingService` generates a draft invoice with tax automatically applied while the contract is still in Draft:

```
Subtotal:  ¥3,000
Tax (10%): ¥300
Total:     ¥3,300
```

### Activate and Finalize

After user confirmation, the contract is activated and the invoice is finalized:

```go
agg.Activate(metadata)  // Contract: Draft → Active
inv.Finalize()           // Invoice: Draft → Finalized
```

### Suspend (Awaiting Payment)

The contract is immediately suspended to prevent service access until payment is confirmed:

```go
agg.Suspend(contract.SuspensionConfiguration{
    BillingBehavior: contract.SuspensionBillingSkip,
    Reason:          "awaiting_initial_payment",
}, metadata)  // Contract: Active → Suspended
```

### Payment Processing and Resume

Payment is processed via a mock gateway. Once confirmed, the contract is resumed and service starts:

```go
paymentService.ProcessPayment(ctx, inv.ID(), service.ProcessPaymentInput{...})
// Invoice: Finalized → Paid

agg.Resume(metadata)   // Contract: Suspended → Active (service starts)
```

### Event History

The event store records every operation:

```
[1] contract.created   (v1) at 2026-04-01
[2] contract.activated (v2) at 2026-04-01
[3] contract.suspended (v3) at 2026-04-01
[4] contract.resumed   (v4) at 2026-04-01
```

## Key Takeaways

- **Payment-Gated Provisioning** ensures service starts only after payment is confirmed
- The `Suspended` status serves as a unified "service inactive" state for both initial payment and non-payment scenarios
- The billing pipeline automatically applies registered plugins (tax, discounts)
- All operations are recorded as immutable events
- The invoice tracks subtotal, discount, tax, and credit breakdown
- Payment processing is decoupled from billing via the gateway interface

> **Note:** This is the recommended flow. A simpler `Draft → Activate → Generate Invoice → Pay` flow is also supported if payment gating is not needed.
