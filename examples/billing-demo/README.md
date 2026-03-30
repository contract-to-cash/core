# Billing Demo

The primary end-to-end example demonstrating the **recommended payment-gated provisioning flow**: create a contract, generate a draft invoice, activate, suspend while awaiting payment, process payment, and resume.

## What You'll See

```
1. Contract created (Draft)     -> ¥3,000/month subscription
2. Draft invoice generated      -> ¥3,000 + Tax 10% = ¥3,300
3. User confirmed               -> Contract: Active, Invoice: Finalized
4. Contract suspended            -> Awaiting initial payment
5. Payment processed             -> ¥3,300 completed
6. Contract resumed              -> Service is now active!
7. Final state                   -> Invoice: Paid, Balance: ¥0
```

## Payment-Gated Provisioning Flow

This demo follows the recommended flow where services are only provisioned after payment clears:

```
Draft ──→ Invoice(draft) ──→ Activate + Finalize ──→ Suspend(awaiting payment)
                                                          │
                                                    Payment succeeds
                                                          │
                                                    Resume ──→ Active (service starts)
```

The `Suspended` state serves as a unified "service not active" state for both:
- **Initial activation**: new contract awaiting first payment
- **Non-payment suspension**: existing contract with overdue payment (dunning)

Both resolve the same way: payment completes -> Resume -> service (re-)activated.

> **Note**: This is the recommended flow, but not the only option. You can use a simpler `Draft -> Activate -> Invoice -> Pay` flow for use cases that don't require payment confirmation before service provisioning. See [Issue #5](https://github.com/contract-to-cash/core/issues/5) for the full design discussion.

## Key Concepts

| Concept | Description |
|---------|-------------|
| **BillingService** | Orchestrates the invoice generation flow (status guard allows Draft contracts) |
| **TaxPlugin** | Calculates 10% Japanese consumption tax on the after-discount subtotal |
| **PaymentService** | Charges via PaymentGateway, records payment, updates invoice status |
| **Suspend/Resume** | Gates service provisioning on payment confirmation |
| **EventStore** | All contract changes are persisted as events (audit trail) |

## Run

```bash
go run ./examples/billing-demo/
```
