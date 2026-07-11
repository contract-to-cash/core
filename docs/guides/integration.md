---
sidebar_position: 1
---

# Integration Guide

This guide explains how to integrate Contract Billing Core into your service.

## Prerequisites

- Go 1.25+
- A database for event store and repositories (PostgreSQL, MySQL, DynamoDB, etc.)
- A payment gateway implementation

## Step 1: Implement Repository Interfaces

Contract Billing Core defines repository interfaces in the domain layer. You implement them for your database: `contract.Repository`, `invoice.Repository`, `payment.Repository`, `balance.Repository`, `usage.Repository`, `pricing.PriceRepository`, and `product.Repository`.

> See [Domain Types Reference](../api/domain-types.md) for complete interface definitions.
>
> See [Postgres Payment Repository](./postgres-payment-repository.md) for
> the required error-translation pattern on `payment.Repository.Save` —
> without it, concurrent `ProcessPayment` calls with the same
> `IdempotencyKey` can silently refund legitimate gateway charges (see
> issue #97).

### Example: PostgreSQL Contract Repository

```go
type PostgresContractRepository struct {
    db         *sql.DB
    eventStore eventstore.Store
    clock      shared.Clock
}

func (r *PostgresContractRepository) Save(ctx context.Context, agg *contract.ContractAggregate) error {
    events := agg.UncommittedEvents()
    if len(events) == 0 {
        return nil
    }
    return r.eventStore.Append(ctx, string(agg.ContractID()), events, agg.Version()-len(events))
}

func (r *PostgresContractRepository) FindByID(ctx context.Context, id shared.ContractID) (*contract.ContractAggregate, error) {
    // Try loading from snapshot first
    snap, _ := r.eventStore.LoadSnapshot(ctx, string(id))

    agg := contract.NewContractAggregate(id, r.clock)
    if snap != nil {
        agg.LoadFromSnapshot(*snap)
        // Load only events after snapshot
        events, _ := r.eventStore.LoadUntilVersion(ctx, string(id), snap.Version)
        // ... replay remaining events
    } else {
        events, _ := r.eventStore.Load(ctx, string(id))
        agg.LoadFromHistory(events)
    }
    return agg, nil
}
```

## Step 2: Implement Event Store

Implement the `eventstore.Store` interface for your database:

```go
type PostgresEventStore struct {
    db    *sql.DB
    clock shared.Clock
}

func (s *PostgresEventStore) Append(ctx context.Context, streamID string, events []eventstore.Event, expectedVersion int) error {
    tx, _ := s.db.BeginTx(ctx, nil)
    defer tx.Rollback()

    // Check current version (optimistic locking)
    var currentVersion int
    tx.QueryRowContext(ctx,
        "SELECT COALESCE(MAX(version), 0) FROM events WHERE stream_id = $1", streamID,
    ).Scan(&currentVersion)

    if currentVersion != expectedVersion {
        return fmt.Errorf("optimistic locking: expected version %d, got %d", expectedVersion, currentVersion)
    }

    // Insert events
    for _, e := range events {
        tx.ExecContext(ctx,
            "INSERT INTO events (id, stream_id, type, version, data, metadata, occurred_at, recorded_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
            e.ID, streamID, e.Type, e.Version, e.Data, e.Metadata, e.OccurredAt, s.clock.Now(),
        )
    }

    return tx.Commit()
}
```

## Step 3: Implement Payment Gateway

Implement `application/port.PaymentGateway` for your payment provider:

```go
type MyGateway struct { /* ... */ }

func (g *MyGateway) ID() string { return "my-gateway" }
func (g *MyGateway) SupportedMethods() []port.PaymentMethodType {
    return []port.PaymentMethodType{port.PaymentMethodTypeCreditCard}
}
func (g *MyGateway) Charge(ctx context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
    // Call your payment provider API
}
// ... implement remaining methods
```

## Step 4: Wire Everything Together

```go
func NewBillingModule(db *sql.DB, gateway port.PaymentGateway) *BillingModule {
    clock := &shared.SystemClock{}
    eventStore := NewPostgresEventStore(db, clock)

    contractRepo := NewPostgresContractRepository(db, eventStore, clock)
    invoiceRepo := NewPostgresInvoiceRepository(db, clock)
    paymentRepo := NewPostgresPaymentRepository(db)
    balanceRepo := NewPostgresCreditRepository(db, clock)
    usageRepo := NewPostgresUsageRepository(db)
    priceRepo := NewPostgresPriceRepository(db)
    productRepo := NewPostgresProductRepository(db)

    // Plugins
    registry := plugin.NewRegistry()
    registry.Register(tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{}))
    // ... register your custom plugins
    registry.InitializeAll(ctx, configs)

    billingService := service.NewBillingService(
        contractRepo, invoiceRepo, usageRepo,
        balance.BalanceConfig{
            DowngradePolicy:    balance.BalancePolicyLedger,
            CancellationPolicy: balance.BalancePolicyLedger,
        },
        priceRepo, productRepo, registry,
        service.BillingConfig{DaysUntilDue: 30},
        clock,
        service.WithBalanceRepo(balanceRepo),
    )

    paymentService := service.NewPaymentService(
        gateway, paymentRepo, invoiceRepo, contractRepo,
        eventStore, registry, clock,
    )

    return &BillingModule{
        BillingService:  billingService,
        PaymentService:  paymentService,
    }
}
```

## Transaction Manager (REQUIRED for production)

> ⚠️ **Wire a real `tx.TxManager` into every write-side service and batch
> processor.** The wiring in Step 4 above omits it to keep the example short — do
> **not** ship that. When no manager is supplied the library falls back to
> `tx.NewNoopTxManager`, which runs each closure **inline with no transaction**:
> a multi-write flow that fails partway leaves earlier writes committed and later
> ones lost. The code compiles and passes happy-path tests; the corruption only
> appears under partial failure in production.

Because forgetting the option is silent, each write-side service and batch
processor now emits a **`Warn`-level log at construction** when it falls back to
the default noop manager, e.g.:

```
level=WARN msg="running without a transaction manager: multi-write operations
  are NOT atomic and can corrupt data under partial failure"
  component=BillingService remedy="wire WithBillingTxManager(...) ..."
```

Treat that log as a production blocker.

### Concrete corruption shapes when the TxManager is missing

- **Credits consumed with no invoice (billing pipeline).**
  `BillingService.executeBillingPipeline` applies credit-ledger balances (FIFO
  `entry.Consume` + `BalanceApplication`) **before** it saves the invoice. Without
  a transaction, an invoice `Save` failure leaves the balance entries already
  drawn down while no invoice exists to justify the deduction — the customer's
  credit silently evaporates.
- **Void without replacement (`CreditNoteService.ReissueInvoice`).**
  The void-and-reissue flow voids the original invoice and generates its
  replacement as one logical unit. Without a transaction, a failure between the
  two steps leaves the original voided and **no replacement issued** — the
  customer is left with no live invoice.
- **Payment recorded but invoice not updated (`PaymentService`).**
  Payment and invoice writes are meant to commit together; a non-atomic run can
  record the payment while the invoice status update is lost (or vice versa),
  desynchronising the ledger.
- **Batch processors** (`ContractRenewalProcessor`, `TrialExpirationProcessor`,
  `BalanceExpirationProcessor`) likewise apply an aggregate mutation plus its
  persistence non-atomically when no manager is wired.

### Wiring a real manager

Implement `tx.TxManager` so `RunInTx` opens one database transaction and yields
`tx.Repos` whose repositories all run on that transaction's connection, then pass
it via the service option / constructor argument:

```go
txManager := NewPostgresTxManager(db) // your implementation of tx.TxManager

billingService := service.NewBillingService(
    contractRepo, invoiceRepo, usageRepo, balanceConfig,
    priceRepo, productRepo, registry, service.BillingConfig{DaysUntilDue: 30}, clock,
    service.WithBalanceRepo(balanceRepo),
    service.WithBillingTxManager(txManager),   // <- REQUIRED for production
)

paymentService := service.NewPaymentService(
    gateway, paymentRepo, invoiceRepo, contractRepo, eventStore, registry, clock,
    service.WithPaymentTxManager(txManager),   // <- REQUIRED for production
)

creditNoteService := service.NewCreditNoteService(
    invoiceRepo, creditNoteRepo, registry, clock,
    service.WithCreditNoteTxManager(txManager), // <- REQUIRED for production
)

renewalProcessor := batch.NewContractRenewalProcessor(
    contractRepo, priceRepo, registry, clock, txManager, logger,
)
```

**In-memory / demo / test code** legitimately runs without transactions. To
acknowledge that intentionally and silence the warning, opt in explicitly with
`service.WithoutTransactions()` /
`service.WithoutPaymentTransactions()` /
`service.WithoutCreditNoteTransactions()` (services) or
`tx.NewNoopTxManagerExplicit(...)` (batch processors) instead of leaving the
manager unset.

## Step 5: Set Up Batch Jobs

Schedule batch processors for recurring operations:

```go
// Contract renewal (run daily)
renewalProcessor := batch.NewContractRenewalProcessor(contractRepo, registry, clock)
renewalProcessor.Process(ctx, batch.BatchOptions{
    ContinueOnError: true,
    Concurrency:     4,
})

// Snapshot creation (run periodically for performance)
snapshotService := service.NewSnapshotService(eventStore, clock, 50) // every 50 events
```

## Directory Structure

A typical service using Contract Billing Core:

```
your-service/
├── cmd/
│   └── server/main.go
├── internal/
│   ├── billing/
│   │   ├── module.go           # Wiring
│   │   ├── gateway_stripe.go   # PaymentGateway implementation
│   │   └── plugins/
│   │       └── my_discount.go  # Custom plugins
│   ├── infrastructure/
│   │   ├── postgres/
│   │   │   ├── event_store.go
│   │   │   ├── contract_repo.go
│   │   │   ├── invoice_repo.go
│   │   │   └── ...
│   │   └── migrations/
│   └── api/
│       └── billing_handler.go  # HTTP/gRPC handlers
└── go.mod
```
