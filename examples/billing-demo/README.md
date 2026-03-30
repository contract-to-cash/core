# Billing Demo

The simplest end-to-end example: create a subscription contract, generate a tax-inclusive invoice, and process a payment.

## What You'll See

```
1. Contract created   -> Draft (¥3,000/month)
2. Contract activated  -> Active
3. Invoice generated   -> ¥3,000 + Tax 10% = ¥3,300
4. Invoice finalized   -> Ready for payment
5. Payment processed   -> ¥3,300 completed
6. Invoice updated     -> Paid, Balance ¥0
```

## Key Concepts

| Concept | Description |
|---------|-------------|
| **BillingService** | Orchestrates the 14-step invoice generation flow |
| **TaxPlugin** | Calculates 10% Japanese consumption tax on the after-discount subtotal |
| **PaymentService** | Charges via PaymentGateway, records payment, updates invoice status |
| **EventStore** | All contract changes are persisted as events (audit trail) |

> **Note**: This demo uses the simple `Draft -> Activate -> Invoice -> Pay` flow. For use cases where services should only be provisioned after payment clears, see the recommended [Payment-Gated Provisioning flow](../../docs/architecture.md) which adds Suspend/Resume steps. Also see [Issue #5](https://github.com/contract-to-cash/core/issues/5) for the full design discussion.

## Run

```bash
go run ./examples/billing-demo/
```
