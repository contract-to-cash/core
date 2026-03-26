# Lifecycle Demo

Walks through the full contract lifecycle -- from trial to cancellation -- including credit management with FIFO application.

## What You'll See

```
  Draft -> Trialing -> Active -> Suspended -> Active -> Cancelled

  1. Contract Created (Draft)       ¥3,000/month
  2. Trial Started (14 days)        AutoConvert=true
  3. Trial Ended -> Active          Auto-converted
  4. First Invoice Paid             ¥3,300 (tax included)
  5. Contract Suspended             Reason: customer on vacation
  6. Contract Resumed               Back to active
  7. Credits Issued                 ¥1,000 (goodwill) + ¥500 (proration)
  8. Second Invoice (Credits FIFO)  Total ¥3,300 - Credit ¥1,500 = Due ¥1,800
  9. Contract Cancelled             Reason: switching to competitor
```

## Key Concepts

| Concept | Description |
|---------|-------------|
| **Trial** | 14-day trial with auto-conversion and reminder configuration |
| **Suspension** | `SuspensionBillingSkip` -- no charges during suspension |
| **Resume** | Contract returns to Active; billing resumes next cycle |
| **Credit (FIFO)** | Goodwill credit (¥1,000) consumed first, then proration credit (¥500) |
| **Cancel** | Terminal state with full event history preserved |

## Credit Application Detail

```
Invoice Total:  ¥3,300 (¥3,000 + 10% tax)

Credit 1: ¥1,000 (goodwill)   -> applied first (FIFO by createdAt)
Credit 2: ¥500  (proration)   -> applied second

Applied Credit: ¥1,500
Amount Due:     ¥1,800
```

Credits are applied in FIFO order (oldest first), skipping expired entries. Each application creates an audit record (`CreditApplication`).

## Run

```bash
go run ./examples/lifecycle-demo/
```
