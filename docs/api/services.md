---
sidebar_position: 2
---

# Services Reference

## BillingService

Orchestrates invoice generation with the full plugin pipeline.

```go
import "github.com/contract-to-cash/core/application/service"

billingService := service.NewBillingService(
    contractRepo,  // contract.Repository
    invoiceRepo,   // invoice.Repository
    usageRepo,     // usage.Repository
    balanceConfig, // balance.BalanceConfig
    priceRepo,     // pricing.PriceRepository
    productRepo,   // product.Repository
    registry,      // *plugin.Registry
    billingConfig, // service.BillingConfig
    clock,         // shared.Clock
    // Optional:
    service.WithBalanceRepo(balanceRepo),       // balance.Repository (via option)
    service.WithBillingTxManager(txManager),    // tx.TxManager — REQUIRED for production; without it, defaults to NoopTxManager (non-atomic, warns at construction)
    service.WithBillingLogger(logger),          // *slog.Logger (via option, defaults to slog.Default())
)
```

### BillingConfig

```go
type BillingConfig struct {
    GracePeriod         time.Duration    // Integrator-interpreted: core does NOT act on this.
                                         //   The integrator's scheduler decides when to call
                                         //   FinalizeInvoice; this value is carried as config for
                                         //   that scheduler (how long to leave a draft open to
                                         //   absorb late usage / hook adjustments). NOT an overdue
                                         //   window and NOT enforced by core. Default 1h.
    DaysUntilDue        int              // Days added to the invoice ISSUE DATE (clock.Now() at
                                         //   generation), NOT the billing period end, to compute
                                         //   the due date. Zero value falls back to 30 days at
                                         //   usage time, so BillingConfig{} is not immediately due.
                                         //   Default 30.
    CollectionMethod    CollectionMethod // Integrator-interpreted: core does NOT act on this and
                                         //   never auto-charges. Carried for the integrator's
                                         //   collection flow. service.CollectionAutoCharge
                                         //   ("charge_automatically") or service.CollectionSendInvoice
                                         //   ("send_invoice"). Default CollectionAutoCharge.
    AllowPartialPayment bool             // If true, generated invoices accept partial payment
                                         //   (invoice.allowPartialPay). Default false.
    TaxRoundingMode     shared.RoundingMode // Minor-unit rounding mode the pipeline applies to
                                         //   subtotal / total discount / total tax so the
                                         //   persisted amounts are integral in the currency's
                                         //   minor unit (issue #189). Default shared.RoundDown
                                         //   (truncate toward zero, per JP consumption-tax
                                         //   practice). shared.RoundHalfUp / shared.RoundUp also
                                         //   accepted.
}
```

> **Integrator-interpreted knobs**: `GracePeriod` and `CollectionMethod` are validated by
> `NewBillingConfig` but the core billing pipeline does not read them to drive behavior. They
> are configuration carried for the integrator's own scheduler / collection flow. Core neither
> auto-finalizes on `GracePeriod` nor auto-charges on `CollectionMethod`.
>
> **Due-date anchor**: the due date is `issueDate + DaysUntilDue`, where `issueDate` is
> `clock.Now()` at generation time (not the billing period end). A zero-value `DaysUntilDue`
> resolves to 30 days at usage time.

`CollectionMethod` is a typed string with two values:

```go
const (
    CollectionAutoCharge  CollectionMethod = "charge_automatically"
    CollectionSendInvoice CollectionMethod = "send_invoice"
)
```

Use `service.NewBillingConfig(opts...)` (with `WithGracePeriod` / `WithDaysUntilDue` /
`WithCollectionMethod` / `WithAllowPartialPayment` / `WithTaxRoundingMode`) for validated
construction with defaults.

> **Minor-unit rounding (#189)**: unlike the integrator-interpreted knobs above, `TaxRoundingMode`
> IS read by the core pipeline. It quantises the subtotal, total discount, and total tax to the
> invoice currency's minor unit (`shared.Currency.MinorUnitExponent()`: JPY=0, USD/EUR=2), so the
> persisted subtotal/discount/tax/total/amountDue are integral in minor units and reconcile exactly
> against integer-only payment gateways. Register non-default currencies with
> `shared.RegisterCurrencyMinorUnit`.

### GenerateInvoice

```go
func (s *BillingService) GenerateInvoice(
    ctx context.Context,
    contractID shared.ContractID,
    billingPeriod shared.DateRange,
) (*invoice.Invoice, error)
```

Executes the billing pipeline (see `executeBillingPipeline` in `billing_service.go`):
1. Load contract → 2. Status/duplicate guards → 3. Calculate subtotal (Price-aware) →
4. BeforeCalculation hooks (**`ctx.Subtotal()` is 0 here**; the subtotal is set on the
context *after* this hook) → 5. Apply DiscountHooks → 6. Cap discounts → 7. After-discount
subtotal → 8. Apply TaxHooks → 9. Calculate total → 10. Apply credits (FIFO, in tx) →
11. Amount due → 12. Create draft invoice (in tx) → 13. **AfterCalculation hooks (fired BEFORE
Save — the invoice is not yet persisted)** → 14. Save (in tx) → return.

For persistence-dependent work, use `OnInvoiceIssuedHook`, which fires after the save in
`FinalizeInvoice`, not `AfterCalculation`.

---

## PaymentService

Processes payments with gateway abstraction and hooks.

```go
paymentService := service.NewPaymentService(
    gateway,       // port.PaymentGateway
    paymentRepo,   // payment.Repository
    invoiceRepo,   // invoice.Repository
    contractRepo,  // contract.Repository
    eventStore,    // eventstore.Store
    registry,      // *plugin.Registry
    clock,         // shared.Clock
    // Optional:
    service.WithCustomerGateway(customerGateway),  // port.CustomerGateway — fallback resolution
    service.WithCustomerIDResolver(resolver),      // port.CustomerIDResolver — maps AccountID → gateway customer ID (issue #231).
                                                   // Without it, the AccountID is sent verbatim (only valid for gateways that
                                                   // accept caller-chosen customer IDs); Stripe-style ID-minting gateways MUST wire one.
    service.WithPaymentTxManager(txManager),       // tx.TxManager — REQUIRED for production; without it, defaults to NoopTxManager (non-atomic, warns at construction)
    service.WithPaymentLogger(logger),             // *slog.Logger (defaults to slog.Default())
)
```

### ProcessPayment

```go
type ProcessPaymentInput struct {
    PaymentMethodID string       // Optional (resolved via fallback chain if empty)
    Amount          shared.Money
    Currency        shared.Currency
    IdempotencyKey  string       // Required — an empty key is rejected up front with ErrCodeValidation (issue #241)
    Metadata        map[string]string
}

func (s *PaymentService) ProcessPayment(
    ctx context.Context,
    invoiceID shared.InvoiceID,
    input ProcessPaymentInput,
) (*payment.Payment, error)
```

Flow: BeforeChargeHook → Gateway.Charge → AfterChargeHook (success) / OnPaymentFailedHook (failure)

### Refund

```go
type RefundInput struct {
    Amount *shared.Money     // nil = refund full remaining amount
    Reason port.RefundReason
    // Optional explicit gateway idempotency key. Leave empty (common case) to let
    // Refund derive a deterministic, amount-bound key (issue #235); see
    // docs/internals/payment-gateway.md §6.3.2 for the derivation and caller contract.
    IdempotencyKey string
}

func (s *PaymentService) Refund(
    ctx context.Context,
    paymentID shared.PaymentID,
    input RefundInput,
) error
```

Processes a refund for a payment. If `Amount` is nil, the remaining unrefunded amount is refunded. The refund is issued via the payment gateway and recorded on the payment entity within a transaction. Post-commit `OnRefundHook` hooks are fired.

---

## CreditNoteService

Orchestrates credit note creation, issuance, and invoice revision workflows.

```go
creditNoteService := service.NewCreditNoteService(
    invoiceRepo,    // invoice.Repository
    creditNoteRepo, // invoice.CreditNoteRepository
    registry,       // *plugin.Registry
    clock,          // shared.Clock
    // Optional:
    service.WithBillingService(billingService),     // *BillingService — required for ReissueInvoice
    service.WithCreditNoteTxManager(txManager),     // tx.TxManager — REQUIRED for production; without it, defaults to NoopTxManager (non-atomic, warns at construction)
    service.WithCreditNoteLogger(logger),           // *slog.Logger (defaults to slog.Default())
)
```

### CreateCreditNote

```go
func (s *CreditNoteService) CreateCreditNote(
    ctx context.Context,
    invoiceID shared.InvoiceID,
    reason invoice.CreditNoteReason,
    items []invoice.CreditNoteItem,
    memo string,
) (*invoice.CreditNote, error)
```

Creates a new credit note in draft status for an existing invoice. The invoice must be in an eligible status (`issued`, `paid`, `partial_paid`, or `overdue`). Validates that items are non-empty and that the credit note total does not exceed the invoice total.

### IssueCreditNote

```go
func (s *CreditNoteService) IssueCreditNote(
    ctx context.Context,
    creditNoteID shared.CreditNoteID,
) (*invoice.CreditNote, error)
```

Transitions a credit note from draft to issued and fires `OnCreditNoteIssuedHook` hooks.

### ApplyCreditNote

```go
func (s *CreditNoteService) ApplyCreditNote(
    ctx context.Context,
    creditNoteID shared.CreditNoteID,
    creditAmount shared.Money,
) (*invoice.CreditNote, error)
```

Transitions a credit note from issued to applied (account credit). The credit amount must not exceed the credit note total.

### RefundCreditNote

```go
func (s *CreditNoteService) RefundCreditNote(
    ctx context.Context,
    creditNoteID shared.CreditNoteID,
    refundAmount shared.Money,
) (*invoice.CreditNote, error)
```

Transitions a credit note from issued to refunded (payment refund). The refund amount must not exceed the credit note total.

### ReissueInvoice

```go
func (s *CreditNoteService) ReissueInvoice(
    ctx context.Context,
    originalInvoiceID shared.InvoiceID,
    reason string,
) (*invoice.Invoice, error)
```

Voids the original invoice and generates a replacement linked to it. The replacement invoice has `revisionOf` set to the original's ID, and `originalInvoiceID` set to the root of the revision chain. All writes run within a transaction. Requires a `BillingService` to be configured via `WithBillingService`. Post-commit `OnInvoiceRevisedHook` hooks are fired.

---

## SnapshotService

Manages event sourcing snapshots for performance.

```go
snapshotService := service.NewSnapshotService(
    eventStore, // eventstore.Store
    clock,      // shared.Clock
    interval,   // int — snapshot every N events
)

snapshotService.CreateSnapshot(ctx, aggregate) error
```

---

## TemporalQueryService

Time-travel queries on event-sourced aggregates.

```go
import "github.com/contract-to-cash/core/application/query"

queryService := query.NewTemporalQueryService(eventStore, clock)

// Reconstruct state at a specific time
agg, err := queryService.GetContractAsOf(ctx, contractID, asOf time.Time)

// Full change history
history, err := queryService.GetContractHistory(ctx, contractID)
// Returns []ContractHistoryEntry{EventType, OccurredAt, UserID, Data}
```

---

## ProjectionService

Builds read-optimized views from events.

```go
import "github.com/contract-to-cash/core/application/projection"

type Projector interface {
    Project(ctx context.Context, event eventstore.Event) error
    Rebuild(ctx context.Context, until time.Time) error
}

projService := projection.NewProjectionService(eventStore, projection.ProjectionOptions{
    SyncMode:   true,
    BatchSize:  100,
    MaxRetries: 3,
    RetryDelay: time.Second,
    Logger:     logger, // *slog.Logger (defaults to slog.Default())
})

projService.RegisterProjector(myProjector)
projService.Start(ctx) // Blocking — subscribes to events
projService.ProcessEvent(ctx, event) // Manual processing
```

---

## BatchProcessor

```go
import "github.com/contract-to-cash/core/batch"

type BatchProcessor interface {
    Process(ctx context.Context, opts BatchOptions) (*BatchResult, error)
}

type BatchOptions struct {
    DryRun          bool // report what would be done, without side effects
    ContinueOnError bool
    Concurrency     int
    Limit           int  // cap on due items loaded per run (0 = unlimited, issue #197)
}

// Accounting invariant: Total == Succeeded + Failed + Skipped (issue #242).
type BatchResult struct {
    Total     int
    Succeeded int
    Failed    int
    // Skipped counts items not attempted because the run stopped early after a
    // failure with ContinueOnError=false — including in-flight concurrent items
    // aborted by the early-stop context cancellation (not recorded in Errors)
    // and items never launched.
    Skipped   int
    Errors    []error
    // DryRunActions (dry runs only): the action a real run would take for each
    // would-succeed item, one entry per Succeeded item in processing order.
    // ContractRenewalProcessor labels: "renew" / "expire" (autoRenew=false) /
    // "cancel" (scheduled cancelAtPeriodEnd) — a dry run mirrors the real run's
    // classification instead of reporting these as failures (issue #242).
    DryRunActions []DryRunAction
}

type DryRunAction struct {
    ItemID string // e.g. contract ID
    Action string // processor-specific label
}
```

Provided processors (scheduling is the consumer's concern — run them from your
cron / CronJob / Cloud Scheduler):

```go
batch.NewContractRenewalProcessor(contractRepo, priceRepo, registry, clock, txManager, logger)
batch.NewTrialExpirationProcessor(contractRepo, registry, clock, txManager, logger)
batch.NewBalanceExpirationProcessor(balanceRepo, clock, txManager, logger) // forfeits expired credit (issue #159)
```

`InvoiceGenerator` / `PaymentRetry` / `UsageAggregator` are NOT shipped as
processors — call the corresponding service methods (`BillingService`,
`PaymentService`) from your own scheduler instead.
