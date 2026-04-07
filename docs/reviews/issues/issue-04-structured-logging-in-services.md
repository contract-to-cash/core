# Add structured logging to service layer

**Labels**: `observability`, `high-priority`, `framework`

## Problem

The logging infrastructure is **wired but unused**:

- `slog.Logger` is injectable via `WithBillingLogger()`, `WithPaymentLogger()`, `WithCreditNoteLogger()` options
- Logger fields exist on all three service structs
- **Zero `slog` calls** in any service method

This means critical operations execute silently:
- Invoice generation (success/failure, amounts, credit application)
- Payment processing (charge attempts, gateway responses, saga compensation)
- Credit note issuance and invoice revision
- Plugin hook execution (which hooks ran, duration, errors)

For a billing framework, silent failures in payment processing or incorrect invoice amounts are unacceptable in production.

## Proposal

Add structured log calls at key decision points using `slog.Logger`:

### BillingService
```go
s.logger.InfoContext(ctx, "invoice generation started",
    "contract_id", contractID,
    "billing_period_start", billingPeriod.Start(),
    "billing_period_end", billingPeriod.End(),
)
// ... after calculation
s.logger.InfoContext(ctx, "invoice generated",
    "invoice_id", inv.ID(),
    "subtotal", subtotal.Amount().FloatString(2),
    "discount", totalDiscount.Amount().FloatString(2),
    "tax", totalTax.Amount().FloatString(2),
    "credits_applied", appliedBalance.Amount().FloatString(2),
    "amount_due", amountDue.Amount().FloatString(2),
)
```

### PaymentService
```go
s.logger.InfoContext(ctx, "payment processing started", ...)
s.logger.ErrorContext(ctx, "gateway charge failed", "error", err, ...)
s.logger.WarnContext(ctx, "saga compensation triggered", ...)
s.logger.InfoContext(ctx, "payment completed", ...)
```

## Acceptance Criteria

- [ ] `BillingService.GenerateInvoice`: log start, subtotal calculation, discount/tax/credit results, final invoice
- [ ] `BillingService.RegenerateInvoice`: log void reason, revision chain link
- [ ] `BillingService.GenerateProrationInvoice`: log proration amounts
- [ ] `PaymentService.ProcessPayment`: log start, payment method resolution, charge result, saga compensation if triggered
- [ ] `PaymentService.Refund`: log refund amount, gateway response
- [ ] `CreditNoteService`: log issuance, application, void
- [ ] Plugin hook execution: log which hooks ran and any non-fatal errors (currently `_ = hookErr`)
- [ ] All log calls use `slog.InfoContext`/`ErrorContext`/`WarnContext` with structured key-value pairs
- [ ] Log levels: Info for normal flow, Warn for non-fatal issues (hook errors, partial payments), Error for failures
- [ ] No sensitive data logged (no full card numbers, no raw gateway responses)
- [ ] Existing tests still pass (logger defaults to `slog.Default()`)

## Non-Goals

- Custom log format or log rotation (user's responsibility)
- Request-scoped logger injection (can be done via context)
- Log-based alerting (monitoring layer concern)
