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
    creditRepo,    // credit.Repository
    creditConfig,  // credit.CreditConfig
    priceRepo,     // pricing.PriceRepository
    productRepo,   // product.Repository
    registry,      // *plugin.Registry
    billingConfig, // service.BillingConfig
    clock,         // shared.Clock
)
```

### BillingConfig

```go
type BillingConfig struct {
    GracePeriod      time.Duration // Grace period for overdue invoices
    DaysUntilDue     int           // Days from invoice creation to due date
    CollectionMethod string        // "send_invoice" or "auto_charge"
}
```

### GenerateInvoice

```go
func (s *BillingService) GenerateInvoice(
    ctx context.Context,
    contractID shared.ContractID,
    billingPeriod shared.DateRange,
) (*invoice.Invoice, error)
```

Executes the 14-step billing pipeline:
1. Load contract → 2. BeforeCalculation hooks → 3. Calculate subtotal (Price-aware) → 4. Apply DiscountHooks → 5. Cap discounts → 6. After-discount subtotal → 7. Apply TaxHooks → 8. Calculate total → 9. Apply credits (FIFO) → 10. Amount due → 11. Create invoice → 12. Save → 13. AfterCalculation hooks → 14. Return

---

## PaymentService

Processes payments with gateway abstraction and hooks.

```go
paymentService := service.NewPaymentService(
    gateway,       // port.PaymentGateway
    paymentRepo,   // payment.Repository
    invoiceRepo,   // invoice.Repository
    contractRepo,  // contract.Repository
    customerGateway, // port.CustomerGateway (optional, for fallback resolution)
    eventStore,    // eventstore.Store
    registry,      // *plugin.Registry
    clock,         // shared.Clock
)
```

### ProcessPayment

```go
type ProcessPaymentInput struct {
    PaymentMethodID string       // Optional (resolved via fallback chain if empty)
    Amount          shared.Money
    Currency        shared.Currency
    IdempotencyKey  string       // Required for deduplication
    Metadata        map[string]string
}

func (s *PaymentService) ProcessPayment(
    ctx context.Context,
    invoiceID shared.InvoiceID,
    input ProcessPaymentInput,
) (*payment.Payment, error)
```

Flow: BeforeChargeHook → Gateway.Charge → AfterChargeHook (success) / OnPaymentFailedHook (failure)

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
    DryRun          bool
    ContinueOnError bool
    Concurrency     int
}

type BatchResult struct {
    Total     int
    Succeeded int
    Failed    int
    Errors    []error
}
```
