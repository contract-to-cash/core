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

## Run

```bash
go run ./examples/billing-demo/
```
