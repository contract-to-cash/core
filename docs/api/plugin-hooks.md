---
sidebar_position: 3
---

# Plugin Hooks Reference

## Base Plugin Interface

```go
type Plugin interface {
    Name() string
    Version() string
    Initialize(ctx context.Context, config Config) error
    Shutdown(ctx context.Context) error
    Priority() int
}

type Config map[string]interface{}
```

### Priority Constants

| Constant | Value | Use Case |
|----------|-------|----------|
| `PriorityHighest` | 0 | Audit logging, validation |
| `PriorityHigh` | 100 | Pre-processing, core business logic |
| `PriorityNormal` | 500 | Default plugins |
| `PriorityLow` | 900 | Post-processing (tax) |
| `PriorityLowest` | 1000 | Last resort, cleanup |

---

## Billing Calculation Hooks

### DiscountHook

```go
type DiscountHook interface {
    Plugin
    CalculateDiscount(ctx *CalculationContext) (shared.Money, error)
}
```

Called during step 4 of invoice generation. Multiple DiscountHooks are called in priority order. Each returns the discount amount to subtract.

### TaxHook

```go
type TaxHook interface {
    Plugin
    CalculateTax(ctx *CalculationContext) (shared.Money, error)
}
```

Called during step 7. Receives `ctx.SubtotalAfterDiscount()` for calculation.

### InvoiceLifecycleHook

```go
type InvoiceLifecycleHook interface {
    Plugin
    BeforeCalculation(ctx *CalculationContext) error
    AfterCalculation(ctx *CalculationContext, invoice *invoice.Invoice) error
}
```

---

## CalculationContext

```go
type CalculationContext struct { ... }

ctx.Context() context.Context
ctx.Contract() *contract.ContractAggregate
ctx.ContractID() shared.ContractID
ctx.Subtotal() shared.Money
ctx.SubtotalAfterDiscount() shared.Money
ctx.AppliedDiscounts() []AppliedDiscount
ctx.Invoice() *invoice.Invoice

ctx.SetSubtotal(s shared.Money)
ctx.SetSubtotalAfterDiscount(s shared.Money)
ctx.RecordDiscount(d AppliedDiscount)
ctx.SetInvoice(inv *invoice.Invoice)
```

### AppliedDiscount

```go
type AppliedDiscount struct {
    PluginName string
    Code       string      // Coupon code, promo name, etc.
    Amount     shared.Money
}
```

---

## Contract Lifecycle Hooks

All contract hooks receive `*plugin.Context` and the contract aggregate.

```go
type OnContractCreateHook interface {
    Plugin
    OnContractCreate(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractActivateHook interface {
    Plugin
    OnContractActivate(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractSuspendHook interface {
    Plugin
    OnContractSuspend(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractResumeHook interface {
    Plugin
    OnContractResume(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractCancelHook interface {
    Plugin
    OnContractCancel(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractCancelScheduledHook interface {
    Plugin
    OnContractCancelScheduled(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractCancelUnscheduledHook interface {
    Plugin
    OnContractCancelUnscheduled(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractRenewHook interface {
    Plugin
    OnContractRenew(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractTrialEndHook interface {
    Plugin
    OnContractTrialEnd(ctx *Context, contract *contract.ContractAggregate, converted bool) error
}
```

### plugin.Context

```go
type Context struct { ... }

ctx.Context() context.Context
ctx.SetMetadata(key string, value interface{})
ctx.GetMetadata(key string) (interface{}, bool)
```

---

## Payment Hooks

```go
type BeforeChargeHook interface {
    Plugin
    BeforeCharge(ctx *PaymentContext, amount shared.Money) error
}

type AfterChargeHook interface {
    Plugin
    AfterCharge(ctx *PaymentContext) error
}

type OnPaymentFailedHook interface {
    Plugin
    OnPaymentFailed(ctx *PaymentContext, err error) error
}

type OnRefundHook interface {
    Plugin
    OnRefund(ctx *PaymentContext, refundAmount shared.Money) error
}
```

### PaymentContext

```go
type PaymentContext struct { ... }

ctx.Context() context.Context
ctx.Payment() *payment.Payment
ctx.Invoice() *invoice.Invoice
ctx.Contract() *contract.ContractAggregate
ctx.ContractID() shared.ContractID
ctx.AccountID() shared.AccountID
ctx.SetContract(c *contract.ContractAggregate)
```

---

## Metrics Hooks

```go
type ContractChangeType string
// created, activated, suspended, resumed, cancelled, renewed, trial_end, expired

type ContractChangeEvent struct {
    ContractID shared.ContractID
    ChangeType ContractChangeType
    OldStatus  *contract.ContractStatus
    NewStatus  *contract.ContractStatus
    OldPriceID *shared.PriceID
    NewPriceID *shared.PriceID
    MRRChange  *shared.Money
    Timestamp  time.Time
}

type OnContractChangeHook interface {
    Plugin
    OnContractChange(ctx *Context, event ContractChangeEvent) error
}

type OnInvoiceIssuedHook interface {
    Plugin
    OnInvoiceIssued(ctx *Context, invoice *invoice.Invoice) error
}

type OnPaymentProcessedHook interface {
    Plugin
    OnPaymentProcessed(ctx *PaymentContext) error
}
```

`OnPaymentProcessedHook` receives the same `*PaymentContext` as the payment hooks
(issue #223): `ctx.Payment()` is the processed payment, and `ctx.Invoice()` /
`ctx.ContractID()` / `ctx.AccountID()` let metrics plugins attribute the payment
to a contract and account without extra lookups.

---

## Invoice Generation Hooks

```go
type InvoiceDocument struct {
    InvoiceID     string
    InvoiceNumber string
}

type DeliveryResult struct {
    DeliveryID string
    Status     string
    SentAt     *time.Time
    Error      *string
}

type InvoiceGenerationHook interface {
    Plugin
    BuildDocument(ctx *Context, invoice *invoice.Invoice, doc *InvoiceDocument) error
    AfterRender(ctx *Context, doc *InvoiceDocument, rendered []byte) error
    AfterDelivery(ctx *Context, doc *InvoiceDocument, result *DeliveryResult) error
}
```

---

## CreditNote Hooks

### OnCreditNoteIssuedHook

```go
type OnCreditNoteIssuedHook interface {
    Plugin
    OnCreditNoteIssued(ctx *Context, creditNote *invoice.CreditNote) error
}
```

Called after a credit note transitions from draft to issued (post-save, outside transaction). Hook failures are logged but do not fail the operation.

### OnInvoiceRevisedHook

```go
type OnInvoiceRevisedHook interface {
    Plugin
    OnInvoiceRevised(ctx *Context, original *invoice.Invoice, replacement *invoice.Invoice) error
}
```

Called after an invoice is voided and a replacement is created via `CreditNoteService.ReissueInvoice` (post-commit, outside transaction). Receives both the voided original and the new replacement invoice. Hook failures are logged but do not fail the operation.

---

## Plugin Registry

```go
registry := plugin.NewRegistry()

registry.Register(p Plugin) error
registry.InitializeAll(ctx context.Context, configs map[string]Config) error
registry.ShutdownAll(ctx context.Context) error

// Getters (return hooks sorted by priority)
registry.GetDiscountHooks() []DiscountHook
registry.GetTaxHooks() []TaxHook
registry.GetInvoiceLifecycleHooks() []InvoiceLifecycleHook
registry.GetOnContractCreateHooks() []OnContractCreateHook
registry.GetOnContractActivateHooks() []OnContractActivateHook
registry.GetOnContractSuspendHooks() []OnContractSuspendHook
registry.GetOnContractResumeHooks() []OnContractResumeHook
registry.GetOnContractCancelHooks() []OnContractCancelHook
registry.GetOnContractCancelScheduledHooks() []OnContractCancelScheduledHook
registry.GetOnContractCancelUnscheduledHooks() []OnContractCancelUnscheduledHook
registry.GetOnContractRenewHooks() []OnContractRenewHook
registry.GetOnContractTrialEndHooks() []OnContractTrialEndHook
registry.GetBeforeChargeHooks() []BeforeChargeHook
registry.GetAfterChargeHooks() []AfterChargeHook
registry.GetOnPaymentFailedHooks() []OnPaymentFailedHook
registry.GetOnRefundHooks() []OnRefundHook
registry.GetOnContractChangeHooks() []OnContractChangeHook
registry.GetOnInvoiceIssuedHooks() []OnInvoiceIssuedHook
registry.GetOnPaymentProcessedHooks() []OnPaymentProcessedHook
registry.GetInvoiceGenerationHooks() []InvoiceGenerationHook
registry.GetOnCreditNoteIssuedHooks() []OnCreditNoteIssuedHook
registry.GetOnInvoiceRevisedHooks() []OnInvoiceRevisedHook
```
