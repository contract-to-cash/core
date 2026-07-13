---
sidebar_position: 1
---

# Integration Guide

This guide explains how to integrate Contract Billing Core into your service.

> **BYO-DB integrators: start from the [contract-to-cash/adapters](https://github.com/contract-to-cash/adapters) repository.**
> It ships production implementations of the interfaces covered below — PostgreSQL/MySQL
> persistence and Stripe/fincode payment gateways — so you can use (or fork) those instead
> of hand-writing everything from the snippets in this guide.

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
>
> Production implementations of all of these interfaces (event store, repositories
> including `CreditNoteRepository`, `tx.TxManager`, `projection.Projector`) ship in the
> [adapters](https://github.com/contract-to-cash/adapters) repository's `postgres/` and
> `mysql/` packages.

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

Implement `application/port.PaymentGateway` for your payment provider. Ready-made Stripe
and fincode gateways (implementing `port.PaymentGateway`, `port.CustomerGateway`, and
`port.WebhookHandler`) ship in the
[adapters](https://github.com/contract-to-cash/adapters) repository's `stripe/` and
`fincode/` packages:

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

## Transactional Outbox (durable notifications, issue #248)

The core-fired payment/invoice hooks (`AfterCharge`, `OnPaymentProcessed`,
`OnInvoiceIssued`) run **after** the bookkeeping transaction commits, so you
cannot enqueue a notification in the same transaction as the payment/invoice
write. A crash between commit and enqueue silently drops events such as
`payment.charged` / `contract.first_payment`.

To make notifications durable, implement one or both **outbox writer ports** and
let the core call them **inside** the write transaction, immediately after the
row is saved and before commit — so your outbox row is written in the SAME
transaction as the payment/invoice:

```go
// PaymentOutboxWriter (and/or InvoiceOutboxWriter) — application/port.
type myOutbox struct{}

func (o *myOutbox) OnPaymentRecorded(ctx context.Context, p *payment.Payment, inv *invoice.Invoice) error {
    // ctx is TRANSACTION-SCOPED: take the current tx off it and piggy-back
    // a lightweight INSERT into your own outbox table. Do NOT open a new
    // connection/transaction, or atomicity breaks silently.
    q := QuerierFromContext(ctx) // your helper, wired by your repositories
    _, err := q.ExecContext(ctx,
        `INSERT INTO outbox (id, kind, payload) VALUES ($1, 'payment.charged', $2)
         ON CONFLICT (id) DO NOTHING`, // dedup: at-least-once delivery
        p.ID(), buildPayload(p, inv))
    return err
}

func (o *myOutbox) OnInvoiceFinalized(ctx context.Context, inv *invoice.Invoice) error {
    q := QuerierFromContext(ctx)
    _, err := q.ExecContext(ctx,
        `INSERT INTO outbox (id, kind, payload) VALUES ($1, 'invoice.finalized', $2)
         ON CONFLICT (id) DO NOTHING`,
        inv.ID(), buildInvoicePayload(inv))
    return err
}

outbox := &myOutbox{}

paymentService := service.NewPaymentService(
    gateway, paymentRepo, invoiceRepo, contractRepo, eventStore, registry, clock,
    service.WithPaymentTxManager(txManager),        // REQUIRED: no tx = no atomicity
    service.WithPaymentOutboxWriter(outbox),
)

billingService := service.NewBillingService(
    contractRepo, invoiceRepo, usageRepo, balanceConfig,
    priceRepo, productRepo, registry, service.BillingConfig{DaysUntilDue: 30}, clock,
    service.WithBillingTxManager(txManager),         // REQUIRED: no tx = no atomicity
    service.WithInvoiceOutboxWriter(outbox),
)
```

A **separate relay/poller** reads the `outbox` table out of band and performs the
actual webhook delivery. Keep `OnPaymentRecorded` / `OnInvoiceFinalized` to a
lightweight, idempotent INSERT: **no external calls or real delivery inside the
transaction** (it holds locks/connections open).

Key rules:

- **Same transaction (ctx piggy-back).** The `ctx` argument carries the active
  transaction. Insert on it. A separate connection defeats the whole point.
- **Veto = rollback, and on the payment path = charge reversal.** Returning an
  error rolls the transaction back. For `OnPaymentRecorded` the already-successful
  gateway charge is then **reversed by saga compensation (Void → Refund)** — the
  same path as a payment-save failure. A transient error triggers a real refund,
  so the INSERT must be robust and idempotent. `OnInvoiceFinalized` moves no
  money, so its rollback is only a harmless re-finalize.
- **At-least-once / dedup.** Retries and idempotent-replay convergence can call a
  writer more than once for the same payment/invoice; key the outbox row (e.g. on
  the payment/invoice ID) so duplicates collapse.
- **NoopTxManager gives no atomicity.** Wiring an outbox writer without a real
  `TxManager` logs a dedicated warning at construction — the payment/invoice save
  and the outbox INSERT are NOT atomic in that case, defeating the outbox.
- **Coexists with post-commit hooks.** `OnInvoiceIssued` / `AfterCharge` /
  `OnPaymentProcessed` still fire. Use the outbox writer for guaranteed delivery
  and the post-commit hooks for best-effort work (e.g. metrics).

Full design and the per-path firing table (which paths fire vs. skip) are in
`docs/internals/plugin-system.md` §11.

## Step 5: Set Up Batch Jobs

Schedule batch processors for recurring operations:

```go
// Contract renewal (run daily)
renewalProcessor := batch.NewContractRenewalProcessor(contractRepo, registry, clock)
renewalProcessor.Process(ctx, batch.BatchOptions{
    ContinueOnError: true,
    Concurrency:     4,
    // Limit caps how many due contracts a single run loads/processes (issue #197).
    // 0 (default) = no limit. Set a positive value to bound memory against a large
    // due-set; the finder returns the oldest-eligible rows first, so schedule the
    // job on a cadence (or loop until BatchResult.Total < Limit) to drain a
    // backlog larger than Limit across runs.
    Limit: 500,
})

// Snapshot creation (run periodically for performance).
// NOTE: CreateSnapshot rejects an aggregate that still holds uncommitted events
// (issue #197) — persist the aggregate (append + ClearUncommittedEvents) first.
snapshotService := service.NewSnapshotService(eventStore, clock, 50) // every 50 events
```

> **Repository finders take a `limit` (BREAKING, issue #197)**: `Process` threads
> `BatchOptions.Limit` into the repository finders
> (`contract.Repository.FindDueForRenewal` / `FindTrialsEndingBefore`,
> `balance.Repository.FindExpired`), whose signatures now take a trailing
> `limit int`. A positive value bounds the rows returned (oldest-eligible first);
> `0` means unbounded and preserves prior behaviour. BYO-DB adapters must add the
> parameter — push the limit down to the query (`LIMIT`) rather than truncating in
> memory.

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
