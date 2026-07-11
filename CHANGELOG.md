# Changelog

All notable changes to `github.com/contract-to-cash/core` are documented in this
file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `port.CustomerGateway.SetDefaultPaymentMethod(ctx, customerID, paymentMethodID)`:
  sets the customer's default payment method used for automatic charges when no
  invoice- or contract-level method is specified, complementing the existing
  Level 3 default-payment-method resolution in `PaymentService.ResolvePaymentMethod`.
  **BREAKING**: external `CustomerGateway` implementations must implement this
  method.
- `SECURITY.md` (vulnerability reporting policy + integrator hardening checklist) and
  `docs/guides/data-protection.md` (PII in an append-only event store, erasure
  patterns, logging, retention responsibilities). README/README.ja gained a
  **Stability** section (pre-v1.0, semver, first tagged release pending).
- `port.WebhookProcessorConfig.MaxEventAge` (`time.Duration`, default `0` =
  disabled): an optional coarse, one-directional staleness bound that drops only
  events whose body `CreatedAt` is older than the bound (e.g. 30 days). It is a
  sanity guard for garbage/absurdly-old payloads, NOT a replay control, and is
  off by default so legitimate old redeliveries always flow.

### Fixed

- **Webhook redeliveries with an old event timestamp are no longer rejected and
  lost (#191)** — `WebhookProcessor.ProcessWebhook` previously rejected any event
  whose body `CreatedAt` fell outside a bidirectional `TimestampTolerance` window
  (default 5 min). Real gateways (Stripe, Adyen) redeliver failed webhooks with
  backoff over hours-to-days carrying the ORIGINAL `CreatedAt`, so a transient
  handler failure followed by a later redelivery was rejected as "webhook
  timestamp too old" and — because the processor's recovery design relies on
  gateway redelivery — the event was permanently lost. The processor no longer
  gates on the event-body `CreatedAt` for replay. Transport-level replay
  protection is now explicitly the responsibility of `WebhookHandler.ParseAndVerify`
  (the HMAC-signed transport timestamp, which an attacker cannot forge);
  duplicates continue to be suppressed by the `WebhookDeduplicator`. The
  `WebhookHandler` interface contract documents this responsibility.

### Deprecated

- **BREAKING (behavioral, pre-v1.0)**: `port.WebhookProcessorConfig.TimestampTolerance`
  is deprecated and NO LONGER APPLIED. It formerly gated the event-body
  `CreatedAt` bidirectionally, which dropped legitimate gateway redeliveries
  (#191). The field is retained (and still validated as non-negative) for
  source/config backward compatibility but has no effect; it will be removed in a
  future major version. Move replay protection into `ParseAndVerify` (signed
  transport timestamp) and, if a coarse staleness bound is desired, use the new
  `MaxEventAge`.

### Changed

- **Low-priority batch cleanup (#162)** — a group of small, low-risk correctness
  and clarity fixes surfaced by the 2026-07-06 review:
  - **BREAKING (pre-v1.0)**: renamed `contract.Repository.FindTrialsEndingSoon`
    to `FindTrialsEndingBefore`. The method returns trials whose `TrialEndDate`
    is before the given time (i.e. already ended when called with `now`); the old
    name read as "ending in the near future". Update repository implementations
    and callers.
  - Plugin execution order within a single hook type is now deterministic:
    `Registry` sorts same-priority hooks with a STABLE sort, so plugins sharing a
    `Priority` run in registration order instead of an arbitrary order (P1).
  - `batch.ContractRenewalProcessor` dry run now validates billing-interval
    resolution, so a contract with a dangling `PendingPriceID` fails the dry run
    instead of passing it and failing in production (B2).
  - Natural term-end expiry now fires `OnContractChange` with the new
    `plugin.ContractChangeExpired` type instead of `ContractChangeCancelled`, so
    churn metrics no longer conflate expiry with voluntary cancellation (B3).
  - `shared.DateRange.UnmarshalJSON` normalizes both bounds to UTC (matching
    `NewDateRange`); it deliberately still tolerates a historically-persisted
    inverted range to stay replay-safe (L-3).
  - Contract upcasters now surface an error on an unknown legacy `billing_cycle`
    (via the new `pricing.BillingCycleToIntervalStrict`) instead of silently
    migrating it to Monthly (L-4).
  - `payment.Payment.MarkChargedBack` is now permitted from `partially_refunded`
    (real-world chargebacks follow partial refunds), not only from `completed` (L-9).
  - Defensive intake copies for `invoice.NewCreditNote` items and
    `invoice.WithLineItems`; `Invoice.SetRevisionOf` / `SetOriginalInvoiceID`
    now reject a self-reference as a no-op (L-6, L-9).
  - `application/tx.RetryOnConflict`'s parameter was renamed `maxRetries` →
    `maxAttempts` to match its actual "total attempt count" semantics (L4).
  - `BillingService` invoice-generation paths gained the nil guards after
    `FindByID` that `FinalizeInvoice` already had (L5).
  - Non-code hardening/clarity: in-memory event store `Append` no longer mutates
    the caller's event slice (I3); `PaymentService` logs (Warn) the previously
    swallowed failed-payment save error (L1); godoc notes on plugin `Context`
    single-goroutine ownership (P2), read-only live-aggregate access via
    `CalculationContext`/`PaymentContext` (P3), `ProjectionService.RegisterProjector`
    ordering (L3), `UsagePrice.Minimum` at zero usage (L-2), the contract-aggregate
    Apply/RaiseEvent ordering (L-8), and `invoicecleanup` partial-void semantics
    (C5). Corrected the `application/` dependency wording in `CLAUDE.md` and
    `docs/architecture.md` (L6).

### Removed

- **Dead-code inventory (#159, applying the #116 delete-unused policy)**:
  - Removed the read-only `contract.Contract` entity (`domain/contract/entity.go`)
    — a state-stored mirror of `ContractAggregate` with no constructor, zero
    package-external references, and 0% coverage. `ContractStatus` /
    `ContractType` / the `BillingInterval` alias remain. The event-sourced
    `ContractAggregate` is the single contract model; read models / projections
    are the consumer's concern.
  - Removed the write-never `ContractAggregate.metadata` field and
    `GetMetadata()`: no event ever set it and no setter existed, so it survived
    only through the snapshot round-trip — an adapter-populated value would
    diverge between snapshot restore and event replay. Historical snapshots
    that still carry a `metadata` key deserialize fine (unknown JSON keys are
    ignored); no snapshot schema bump.
  - Dropped the never-implemented `IdempotencyConfig{TTL}` claim from
    design-decisions 4.1; key TTL/expiry is an adapter concern.

- **BREAKING**: dropped the deprecated `BillingCycle` backward-compat
  scaffolding from the contract domain before v1.0 (#111). Removed:
  `CreateContractCommand.BillingCycle` (use `Interval BillingInterval`),
  `ContractAggregate.GetBillingCycle()` (use `GetInterval()`),
  `ContractAggregate.Renew(BillingCycle, …)` (use `RenewWithInterval(BillingInterval, …)`),
  the `billing_cycle`/`old_billing_cycle`/`new_billing_cycle` fields on
  `ContractCreatedEvent`/`ContractRenewedEvent`, the `billing_cycle` field on the
  `ContractState` snapshot, the `Contract.BillingCycle()` entity getter, and the
  `type BillingCycle = pricing.BillingCycle` alias plus the re-exported
  `BillingCycleDaily/Weekly/Monthly/Yearly` constants in `domain/contract`.
  The `pricing.BillingCycle` string type, its constants, `BillingCycleToInterval`,
  and `BillingInterval.ToBillingCycle` remain in the `pricing` package for `Price`
  construction, display formatting, and adapter code reading third-party strings.
- Migrate call sites: set `Interval: pricing.Monthly()` (or `Daily/Weekly/Yearly/`
  `Quarterly/SemiAnnual`) instead of `BillingCycle`, and read `GetInterval()`.

### Changed

- **BREAKING** (pre-v1.0 domain behavior): `ContractAggregate.Create` now
  REQUIRES a non-empty `CreateContractCommand.IdempotencyKey` and rejects an
  empty key with a `validation` DomainError (#159). The key was declared
  required by design-decisions 4.1 but never validated nor persisted — a dead
  field that let retried creations produce duplicate contracts. The key is now
  carried on `ContractCreatedEvent` (`idempotency_key`, SchemaVersion 2 → 3;
  the new `ContractCreatedIdempotencyKeyUpcaster` bumps historical v1/v2
  payloads, which replay with an empty key that `Apply` tolerates), exposed via
  `ContractAggregate.IdempotencyKey()`, and stored in the contract snapshot
  (empty in legacy snapshots). Enforcement boundary: the core validates
  PRESENCE; UNIQUENESS is the repository/adapter's contract — see the new
  godoc note on `contract.Repository.Save` (recommended partial unique index +
  `ErrCodeConflict`, mirroring the #149 invoice per-period pattern). Migration:
  pass a caller-generated key (e.g. a request ID) in every
  `CreateContractCommand`.
- **BREAKING** (pre-v1.0 API): `balance.Repository` gains
  `FindExpired(ctx, asOf) ([]*BalanceEntry, error)` (#159) — expired,
  not-yet-forfeited entries in creation order; the scan feeding the new
  `batch.BalanceExpirationProcessor`. BYO-DB implementations must add it (the
  in-memory reference implementation is included).
- design-decisions 3.2 now records the batch-processor implementation status
  honestly (#159): `InvoiceGenerator` / `PaymentRetry` / `UsageAggregator` are
  NOT shipped as processors (integrator-scheduler scope); shipped processors
  are `ContractRenewalProcessor`, `TrialExpirationProcessor`, and the new
  `BalanceExpirationProcessor`.
- **BREAKING** (pre-v1.0 API): `port.WebhookDeduplicator` gains a second method,
  `MarkProcessed(ctx, eventID, ttl) error`, splitting webhook deduplication into
  a CHECK phase (`IsDuplicate`) and a RECORD phase (`MarkProcessed`) (#155).
  `WebhookProcessor.ProcessWebhook` now records the dedup marker ONLY AFTER the
  event handler succeeds. Previously the marker was written *before* the handler
  ran, so a transient handler failure (e.g. a DB outage during a
  `payment.succeeded` webhook) returned an error to the gateway but the
  gateway's redelivery was then swallowed as a duplicate — with `dlq == nil` the
  event was lost permanently. Consequences and migration:
  - Delivery is now **at-least-once**: an event can reach the handler more than
    once (crash between handler success and `MarkProcessed`, a `MarkProcessed`
    failure, or two redeliveries racing through `IsDuplicate`). **Handlers MUST
    be idempotent** — this is documented on the new `port.WebhookEventHandler`
    type and on `WebhookDeduplicator`.
  - Concurrency is unchanged and explicit: `ProcessWebhook` does not serialize
    same-event deliveries, so two concurrent redeliveries may both run the
    handler. An implementation MAY make `IsDuplicate` perform an atomic
    check-and-set for stronger dedup, but callers must not rely on it.
  - The DLQ stays optional but is now a **backstop for poison messages**, not the
    sole recovery path (the gateway retry works again because failures no longer
    record a marker). When a handler exhausts its retries and `dlq == nil`, the
    processor logs the failure at **error level** (via a new optional
    `slog.Logger`, `port.WithWebhookLogger`, defaulting to `slog.Default()`) and
    returns the error, so the loss is observable and recovery proceeds via
    gateway redelivery. A `MarkProcessed` failure *after* a successful handler is
    logged at warn level and is non-fatal.
  - Migration: implementations of `WebhookDeduplicator` must add `MarkProcessed`
    (record the event ID with a TTL); implementations that previously recorded
    inside `IsDuplicate` must move the write to `MarkProcessed`. The
    `ProcessWebhook` handler parameter is now the named type
    `port.WebhookEventHandler` (assignable from the same func literal, so call
    sites are unaffected).
- **Event Sourcing / on-disk schema**: contract event payloads and the contract
  snapshot no longer carry `billing_cycle`. Historical events that recorded only
  `billing_cycle` are migrated on read by new upcasters
  (`ContractCreatedEventUpcaster`, `ContractRenewedEventUpcaster`) which convert
  `billing_cycle` → `interval` and bump the event `SchemaVersion` to 2. Legacy
  contract snapshots (schema_version 0/1) are converted to an interval on
  `LoadFromSnapshot`; new snapshots record `schema_version: 2`. No data migration
  is required — existing streams and snapshots replay correctly.
- **Event Sourcing — schema-version self-declaration** (#153): `RaiseEvent` no
  longer hardcodes `SchemaVersion: 1`. A new optional `eventstore.SchemaVersioned`
  interface (`CurrentSchemaVersion() int`) lets an event self-declare the version
  its current payload serializes to; `RaiseEvent` stamps that value (default 1 for
  events that do not implement it). The four semantically-v2 contract events
  (`contract.created`, `contract.price_changed`, `contract.trial_ended`,
  `contract.renewed`) now declare version 2, so freshly written events are stamped
  v2 and **skip the upcaster chain on replay** (each upcaster's `CanUpcast` is
  `fromVersion <= 1`). This removes the per-replay rewrite cost for new events and
  closes the latent hazard whereby a future non-idempotent upcaster would corrupt
  freshly written events mis-stamped as v1. Historical v1 events still upcast to v2
  as before — fully backward compatible, no data migration.
- `eventstore.UpcasterChain.Upcast` now iterates to a fixpoint (bounded by
  `maxUpcastIterations`) instead of a single pass, so a multi-hop migration
  converges regardless of upcaster registration order (e.g. a v1→v2 upcaster
  registered after the v2→v3 upcaster it feeds). A non-converging upcaster returns
  an error rather than looping forever.

### Fixed

- `pricing.TieredPrice` gains a validating constructor `NewTieredPrice(tiers, mode)`
  and no longer silently mis-bills a misconfigured tiered price (#156). Previously
  `TieredPrice` took its tiers through exported fields with no validation, so two
  misconfigurations passed silently: (1) tiers not sorted ascending by `UpTo` made
  the graduated `tierCapacity = UpTo - prevUpTo` go negative, producing a **negative
  tier charge**; and (2) mixing currencies across a tier's `UnitPrice`/`FlatFee`
  made the internal `Money.Add` fail, and every failure was swallowed to
  `shared.Zero(currency)` — a broken price **billed ¥0 with no error**.
  - `NewTieredPrice` validates: at least one tier; a known mode
    (`graduated`/`volume`); tiers sorted strictly ascending by `UpTo`; `UpTo == 0`
    (unlimited) only on the last tier and `UpTo > 0` on every non-last tier; and a
    single currency across all tiers' `UnitPrice`/`FlatFee`. It returns a
    `shared.DomainError` (`validation_error`, or `currency_mismatch` for the
    currency rule), mirroring `NewUsagePrice`'s style (#148).
  - `CalculatePrice` no longer swallows `Money.Add` errors to zero. Because the
    constructor makes a currency mismatch impossible, the remaining Add failures
    are impossible-by-construction; a bypassed constructor now surfaces the
    invariant violation by panicking (via `mustAddTier`) rather than billing zero,
    the same policy as `assertNonNegativeUsage`. The `CalculatePrice` signature is
    unchanged (it is a `PricingModel` interface method that cannot return an error).
  - The exported fields remain writable for backward compatibility and persistence
    reconstruction. Snapshot restore (`Price.FromSnapshot`) carries the stored
    `PricingModel` through as-is and does not re-run `NewTieredPrice`, so
    historically persisted prices always load (replay-safety), consistent with the
    rest of the snapshot path. All in-repo construction (tests, benchmarks, the
    `pricing-models-demo` example) now goes through `NewTieredPrice`.
- `CouponPlugin.CalculateDiscount` no longer zeroes out a valid coupon when
  stacking is disabled and the repository returns an invalid coupon (e.g.
  expired) ahead of it (#158). Previously the plugin truncated the candidate
  slice to `coupons[:1]` (and to `coupons[:MaxCouponsPerInvoice]`) *before* the
  per-coupon validity checks (window, minAmount, currency, per-account limit)
  ran, so a leading invalid coupon was the only one considered and the customer
  received no discount despite holding a valid coupon. Selection is now
  "first valid wins": the first coupon that passes every check is applied, and
  `MaxCouponsPerInvoice` counts VALIDATED (applied) coupons rather than scanned
  ones. Behavior is unchanged for the all-valid cases already covered by tests
  (no-stacking, stacking with a cap, `MaxCouponsPerInvoice == 0` = unlimited).
- `BillingService.GenerateInvoice` no longer produces immediately-due invoices
  from a zero-value `BillingConfig{}` (#154). `DaysUntilDue == 0` now falls back to
  a 30-day due date at usage time (`BillingConfig.effectiveDaysUntilDue`), matching
  the struct's "zero values are defaults" contract and `NewBillingConfig`'s default.
  The due-date anchor is documented as the invoice issue date (`clock.Now()` at
  generation), not the billing period end; the docs previously misstated the anchor
  as `period.End()`. Behavior is unchanged for callers that set `DaysUntilDue`.
- Clarified that `BillingConfig.GracePeriod` and `CollectionMethod` are
  integrator-interpreted configuration: `NewBillingConfig` validates them, but the
  core billing pipeline does not act on them (core does not auto-finalize on
  `GracePeriod` nor auto-charge on `CollectionMethod`). Field godocs and docs
  (`docs/internals/plugin-system.md` §8.1, `docs/api/services.md`) updated to match;
  no behavior change (#154).
- `eventstore.EventRegistry.Register` now returns an `error` and rejects a
  duplicate `EventType` registration instead of silently overwriting the prior
  Go-type mapping (which could route deserialization to the wrong type), mirroring
  `plugin.Registry.Register` (#153). The contract package's registry initializer
  fails fast via a `mustRegister` panic on duplicates (a programmer error at init).
- `inmemory.InMemoryEventStore.Append` now validates event `Version` contiguity
  against `expectedVersion` (events must be numbered `expectedVersion+1, +2, …`),
  rejecting a gapped/out-of-order batch with a `validation_error` before it can
  corrupt the append-only log (#153).
- `SnapshotService.CreateSnapshot` now requires the aggregate to implement
  `eventstore.SnapshotMarshaler` and returns a `validation_error` `DomainError`
  naming the interface when it does not, instead of falling back to
  `json.Marshal` (#157). Event-sourced aggregates keep their state in unexported
  fields, so the old fallback silently serialized an empty `{}` snapshot that a
  later `LoadFromSnapshot` restored as an empty aggregate at a non-zero version —
  silent state corruption. `CreateSnapshot` was previously untested; added
  round-trip, non-marshaler-error, and save-error-propagation tests.
- `inmemory.InMemoryEventStore.LoadSnapshot` now returns the highest-`Version`
  snapshot rather than the most recently appended one (#157). An out-of-order
  `SaveSnapshot` (e.g. a lagging rebuild worker persisting a stale snapshot after
  a newer one) previously made reads resume from an older snapshot and replay
  events that predate it. `LoadSnapshotBefore` is likewise made robust to
  out-of-order saves: among snapshots created before the cutoff it returns the
  one with the greatest `CreatedAt` (tie-broken by higher `Version`), matching
  the postgres reference (`ORDER BY created_at DESC LIMIT 1`); the CreatedAt cut
  semantics are unchanged.

### Added

- `Invoice.MarkIssued()` and `Invoice.MarkOverdue(now)` (#159): explicit
  transitions to the previously snapshot-only `issued` and `overdue` statuses
  (the defect class fixed for `refunded` in #99). `MarkIssued` is
  finalized → issued, fired by the integrator's delivery flow; `MarkOverdue`
  is finalized|issued → overdue, only when `now` is strictly after the due
  date (no due date ⇒ rejected), fired by the integrator's dunning scheduler
  (e.g. after `Repository.FindOverdue`). `partial_paid` deliberately does not
  transition to overdue. Both bump the optimistic-locking version per the
  #147 rule; both statuses remain accepted from snapshots.
- `batch.BalanceExpirationProcessor` (#159): forfeits expired credit-ledger
  entries. `BalanceEntry` supported an expiry and `FindAvailable` filtered
  expired entries out of billing, but nothing transitioned them — expired
  credit sat as live-looking non-zero rows forever. New
  `BalanceEntry.MarkExpired(now)` zeroes the remaining amount (original
  amount + expiry kept for audit; fully-consumed entries are an idempotent
  no-op; a forfeiting expiry bumps the optimistic-lock version so it cannot
  race a concurrent `Consume`). The processor mirrors
  `ContractRenewalProcessor` exactly: dry-run / ContinueOnError / Concurrency,
  scan outside the tx, per-entry load-mutate-save inside the tx boundary
  (#151). Scheduling remains the consumer's concern.
- Transactional cumulative over-credit cap for `CreditNoteService.CreateCreditNote`
  (#124): the load-invoice → aggregate-existing-credit-notes → cumulative-cap-check
  → save sequence now runs inside a single `tx.Run` transaction. The invoice is
  loaded through the transaction-scoped `repos.Invoices.FindByID`, which a real
  database adapter backs with a row lock (`SELECT ... FOR UPDATE`), so concurrent
  `CreateCreditNote` calls for the same invoice are serialized and cannot
  collectively over-credit it — the loser re-reads the larger aggregate and is
  rejected with `business_rule_violation`. A new in-memory
  `inmemory.InMemoryCreditNoteRepository` and an integration regression test
  (concurrent 600 + 600 issuance against a 1000 invoice, one rejected) accompany
  the change. Adapter `ReposFactory` implementations must be extended to populate
  the new `CreditNotes` field (follow-up in the adapters repo); until then the
  service falls back to its field repo, preserving best-effort behavior.
- Release workflow (`.github/workflows/release.yml`): tags and publishes a
  GitHub Release when a versioned `## [x.y.z]` section lands in `CHANGELOG.md`
  on `main`, or on manual `workflow_dispatch` with an explicit tag input.
  Runs build + test + lint before tagging.
- Optimistic-locking coverage for all mutating paths on `invoice.Invoice` and
  `invoice.CreditNote` (#147). Previously only `Invoice.Finalize` and
  `Invoice.MarkRefunded` bumped the version, and `CreditNote` had no version
  machinery at all, so a compliant optimistic-locking adapter could not detect
  lost updates on `RecordPayment` / `Void` / `VoidWithReason` (concurrent
  partial payments silently under-reported `paidAmount`; a Void-vs-RecordPayment
  race lost the payment) or on any `CreditNote` transition (a concurrent
  `Apply` + `Refund` on one issued note both succeeded — crediting the account
  AND refunding the gateway while recording one outcome).
  - `Invoice.RecordPayment`, `Void`, `VoidWithReason`, and the revision-chain
    link setters `SetRevisionOf` / `SetOriginalInvoiceID` now bump `version`
    (in addition to the existing `Finalize` / `MarkRefunded`).
  - `invoice.CreditNote` gains `Version()`, `LoadedVersion()`, and
    `SetVersion()` (mirroring `Invoice`); `Issue` / `Apply` / `Refund` / `Void`
    bump the version. `CreditNoteSnapshot` carries a new `Version` field and
    `CreditNoteFromSnapshot` restores both `version` and `loadedVersion` from
    it.
  - `domain/invoice/credit_note_repository.go` godoc documents the same
    concurrency contract as `invoice.Repository.Save` (#130): implementations
    must either optimistic-lock on `LoadedVersion()` (returning
    `tx.ErrVersionConflict` on mismatch) or serialize reads. The in-memory
    `InMemoryCreditNoteRepository` now enforces the lock so the contract is
    exercisable in tests.
  - These additions are backward compatible: fresh entities start at version 0
    and adapters that never populate the field keep working. The adapter-side
    follow-up is tracked at contract-to-cash/adapters#30.
- Optimistic locking for `invoice.Invoice` (#130): new `Version()`,
  `LoadedVersion()`, and `SetVersion()` methods mirror `balance.BalanceEntry`.
  `Finalize()` now bumps the version so two concurrent finalizations cannot both
  succeed. `InvoiceSnapshot` carries a `Version` field; `InvoiceFromSnapshot`
  restores `version` and `loadedVersion` from it. The in-memory
  `invoice.Repository` enforces the lock (returns `tx.ErrVersionConflict` on
  version mismatch), and `BillingService.FinalizeInvoice` wraps its
  load→finalize→save closure in `tx.RetryOnConflict` so the race loser re-reads
  the finalized row and is rejected with `invalid_state_transition` — firing
  `OnInvoiceIssued` at most once. These additions are backward compatible: a
  fresh invoice starts at version 0 and adapters that never populate the field
  keep working.

### Fixed

- In-memory repositories now return isolated copies from every read, and
  optimistic-lock conflicts are recognised uniformly (#152). Previously the
  reference `infrastructure/inmemory` repositories handed back the stored
  pointer from `FindByID` and the list finders, so concurrent load-modify raced
  on aggregate/entity internals, the optimistic-locking contract was
  unexercisable through the raw repo (two loads shared one instance), and
  unsaved mutations were visible to other readers. Reads now return a fresh copy
  — the event-sourced `ContractAggregate` is re-materialized from the event
  store; the state-stored `Invoice`, `CreditNote`, `BalanceEntry`, and `Payment`
  are cloned via their snapshot round-trip (preserving `version`/`loadedVersion`)
  — and `Save` stores an isolated copy so later caller mutations cannot leak in.
  This lets integrators test optimistic locking directly against the in-memory
  repos, so the hand-rolled `isolatingInvoiceRepo` test wrapper was removed as
  redundant. Separately, `tx.RetryOnConflict` now also treats the event store's
  `shared.ErrCodeVersionConflict` `DomainError` (returned by contract saves) as
  retriable — not only the `tx.ErrVersionConflict` sentinel — via the new
  `tx.IsVersionConflict` helper, so a contract-save conflict wrapped in
  `RetryOnConflict` retries the same way an invoice/balance/credit-note conflict
  does. (The two encodings stay distinct because `domain/shared` must not import
  `application/tx`; `tx` matches the code instead.)
- Documentation: the README quick start (en/ja) and several published docs used
  APIs removed in #111 and stale constructor signatures, so copy-pasted snippets
  failed to compile (#160). Corrected `CreateContractCommand` to use
  `Interval: pricing.Monthly()` (was the removed `BillingCycle` field), added the
  required 6th `createdAt` argument to every `pricing.NewPrice(...)` example, fixed
  `invoicecleanup.NewInvoiceCleanupPlugin(invoiceRepo)` (dropped the removed `clock`
  argument), re-synced the stale Japanese i18n pages (`PlanID` → `PriceID`,
  `GetBillingCycle()` → `GetInterval()`, `Renew` → `RenewWithInterval`) to the
  current English sources, and wired the previously unlisted
  `guides/postgres-payment-repository` and `research/2026-04-10-payment-idempotency-patterns`
  docs into `website/sidebars.ts`.
- Documentation: reconciled the canonical `docs/internals/` spec with the code (#161).
  Relabeled phantom components in `payment-gateway.md` (`infrastructure/gateway/` /
  `DefaultGatewayRouter` / `domain/payment/subscription_gateway.go`) as consumer-side
  (BYO Gateway) reference sketches not shipped in this repo; relabeled
  `metrics-invoicegen.md` §5's never-realized layout (`plugin/metrics/`,
  `plugin/invoicegen/`, `subscription_service.go`) as consumer-side reference architecture
  and corrected the in-repo paths (flat `plugin/hooks_*.go`, real services, `inmemory/`
  only); replaced the heavily stale `plugin-system.md` §8.1 `BillingService` listing
  (removed `c.Plan()`, phantom `ProcessPriceChange`/`calculateDueDate`) with an abridged
  excerpt pointing at the source; unified the `BeforeCalculation` ordering across
  `architecture.md`, `plugin-system.md`, `design-decisions.md`, and
  `concepts/plugin-system.md`, adding an explicit note that `ctx.Subtotal()` is zero during
  `BeforeCalculation` and `AfterCalculation` fires before Save; fixed `api/services.md`
  `CollectionMethod` values (`charge_automatically`), added `AllowPartialPayment`, corrected
  the `GracePeriod` description and the AfterCalculation/Save order; updated
  `plugin-system.md` §2.2 (`productID`/`ProductID()`) and §6.2 (`Coupon` struct) to match
  the code; and corrected the `plugins/tax` spec to document the actual unconditional-10%
  minimal calculator with jurisdiction-aware calculation noted as a consumer extension point.
- Applied the `FinalizeInvoice` transaction pattern (in-tx load → state check →
  mutate → save, with `RetryOnConflict` where an optimistic-lock conflict can
  occur) to the remaining load-mutate-save-outside-tx sites (#151):
  - `PaymentService.ProcessPayment` now re-loads the invoice through the
    transaction-scoped repository inside the tx closure before `RecordPayment`,
    instead of mutating and saving the copy read before the gateway charge. On a
    backend honouring the invoice concurrency contract this converges with a
    concurrent payment (fresh-state `RecordPayment`) rather than failing the
    stale copy's `Save` with a version conflict (#147). Documented idempotency
    behaviours (pre-charge short-circuit, #97 duplicate-key convergence, 3DS
    upgrade, saga compensation) are unchanged.
  - `CreditNoteService.ReissueInvoice` now loads the original invoice inside the
    transaction (mirroring `CreateCreditNote`), so a payment committed
    concurrently is observed by `VoidWithReason` instead of a stale pre-tx
    snapshot silently overwriting a paid invoice with `voided`.
  - `CreditNoteService.IssueCreditNote` / `ApplyCreditNote` / `RefundCreditNote`
    now run load → state check → transition → save inside `tx.Run` wrapped in
    `RetryOnConflict`, using the credit-note version machinery (#147). Concurrent
    `IssueCreditNote` on the same note fires `OnCreditNoteIssued` exactly once and
    the loser gets a clean `invalid_state_transition` domain error instead of a
    raw version conflict, preventing double ledger postings.
  - `batch.ContractRenewalProcessor` and `batch.TrialExpirationProcessor` now
    resolve the interval / mutate (`RenewWithInterval` / `EndTrial`) and save a
    repository-loaded instance INSIDE the transaction. A failed `Save` no longer
    leaves the aggregate returned by `FindDueForRenewal` / `FindTrialsEndingSoon`
    half-mutated with dangling uncommitted events for the next batch run.
- Money currency / sign guards were missing at several money-critical sites,
  rooted in `Money.GreaterThan` returning `false` (rather than erroring) on a
  currency mismatch (#148). All six verified sites now guard explicitly, mirroring
  the reference `payment.ValidateRefund`:
  - `CreditNote.Apply` / `CreditNote.Refund` reject a mismatched-currency amount
    (`currency_mismatch`) and a non-positive amount (`validation_error`) before
    the "exceeds total" check, so a foreign-currency or negative amount can no
    longer be persisted onto an issued note. Version bumps (#147) are preserved.
  - `Invoice.ValidatePayment` / `RecordPayment` reject a negative payment
    (`validation_error`), which previously slipped through when partial payment
    was enabled and decreased `paidAmount`. Zero remains accepted so a
    zero-amount invoice can be settled by a zero payment.
  - `invoice.WithAppliedBalance` no longer silently swallows a currency-mismatch
    error: `NewInvoice` now surfaces it (via a deferred option-error field)
    instead of leaving `amountDue`/`balance` inconsistent.
  - `NewCreditNote` rejects any item whose amount is zero or negative
    (`validation_error`); `NewCreditNoteItem`'s signature is unchanged.
  - The coupon plugin now returns an error from `Coupon.CalculateDiscount` when a
    `maxDiscount` cap is configured in a foreign currency (previously the cap was
    silently dropped, yielding an unbounded discount), and skips a coupon whose
    `minAmount` is denominated in a foreign currency (consistent with existing
    foreign-currency fixed-discount handling).
  - `pricing.NewUsagePrice` validates that `Minimum`/`Maximum` clamps share the
    unit-price currency, since `UsagePrice.CalculatePrice` (a `PricingModel`
    interface method that cannot return an error) would otherwise silently drop a
    wrong-currency clamp. The broader currency-safe-`CalculatePrice` work is
    tracked in #156.
- **BREAKING** (pre-v1.0 API): `balance.NewBalanceEntry` and `payment.NewPayment`
  now return `(*T, error)` so they can reject invalid amounts at construction.
  `NewBalanceEntry` rejects a negative amount (a negative credit would be applied
  as a debit and inflate an invoice's amount due) but permits zero;
  `NewPayment` rejects a negative amount but permits zero. Snapshot-restore paths
  (`FromSnapshot` family) deliberately bypass these guards, so replay of
  historically valid entities is unaffected. Migrate call sites to handle the
  returned error.
- Concurrent `BillingService.GenerateInvoice(contractID, samePeriod)` calls could
  each pass the duplicate-invoice check (which ran BEFORE the transaction opened)
  and both insert, producing two billable invoices for one period and a downstream
  double charge (#149). The duplicate guard now also runs INSIDE the `tx.Run`
  closure through the transaction-scoped repo for `GenerateInvoice`,
  `RegenerateInvoice`, and (implicitly, via its intentional exemption)
  `GenerateProrationInvoice`, so a backend that serializes the re-check (row lock /
  SERIALIZABLE) closes the window. Because inserting a NEW invoice cannot raise an
  optimistic-lock conflict, the fix also documents a per-period uniqueness contract
  on `invoice.Repository.Save`: adapters MUST enforce a partial unique index on
  `(contract_id, billing_period)` for non-voided, non-proration invoices and return
  a `shared.ErrCodeConflict` `DomainError` on violation. The in-memory reference
  repository now enforces this the same way (voided and proration invoices exempt,
  so void-and-recreate and proration adjustments still work). New invoice-type
  metadata constants (`invoice.MetadataKeyInvoiceType`, `InvoiceTypeProration`,
  `InvoiceTypeRegeneration`) and an `Invoice.IsProration()` helper back the
  exemption. Regression tests: a concurrent `GenerateInvoice` race (single winner,
  losers get a clean conflict), `RegenerateInvoice` after void for the same period,
  and distinct-period generation left unaffected.
- Release workflow: the `resolve` job no longer fails on `main` pushes that
  carry no versioned `## [x.y.z]` heading. `grep` matching zero lines returns
  exit 1, which under `set -e -o pipefail` aborted the step before the
  skip branch; the no-op path now exits cleanly.

### ⚠ BREAKING CHANGES

#### `payment.Repository.Save` must return a typed duplicate-key error (#97)

**Why:** `PaymentService.ProcessPayment` now catches concurrent-success races
(two goroutines with the same `IdempotencyKey` that both reach the success
path) by detecting a dedicated sentinel error from the repository. Without
this, the race loser's duplicate save used to proceed silently, leaving two
payment records under one key and double-recording the invoice.

**What changed:** `domain/payment/repository.go` godoc now requires
implementations to return an error matching
`errors.Is(err, payment.ErrDuplicateIdempotencyKey)` (typically a
`*payment.DuplicateIdempotencyKeyError`) when a `Save` would introduce a
non-empty `IdempotencyKey` collision with a DIFFERENT existing `PaymentID`.
Same-ID re-saves (e.g. 3DS Pending → Completed upgrade) MUST still succeed.

**Who is affected:** consumers that implement `payment.Repository` against a
production backend (Postgres, MySQL, DynamoDB, etc.). The `Repository`
interface signature is unchanged, so non-compliant adapters will still
**compile** — but under concurrent load they will route the race loser
through `saga.Compensate()`, issuing a `Refund` against the shared gateway
transaction and **undoing the winner's legitimate charge**. This is a
silent regression vector; migrate before deploying this version.

**Migration recipe (Postgres, `lib/pq` or `pgx`):**

1. Add a unique index on `idempotency_key`:
   ```sql
   CREATE UNIQUE INDEX idx_payments_idempotency_key
     ON payments (idempotency_key)
     WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';
   ```

2. Translate `23505` unique-violation errors in your `Save`:
   ```go
   import (
       "errors"

       "github.com/contract-to-cash/core/domain/payment"
       "github.com/jackc/pgx/v5/pgconn"
   )

   func (r *PostgresPaymentRepo) Save(ctx context.Context, p *payment.Payment) error {
       _, err := r.db.Exec(ctx, upsertSQL, /* ... */ p.ID(), p.IdempotencyKey())
       if err == nil {
           return nil
       }

       var pgErr *pgconn.PgError
       if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
           pgErr.ConstraintName == "idx_payments_idempotency_key" {
           return &payment.DuplicateIdempotencyKeyError{
               Key:         p.IdempotencyKey(),
               AttemptedID: p.ID(),
               // ExistingID is optional — fill it via a follow-up
               // SELECT if your adapter wants to give operators the
               // winner's ID for diagnostics.
           }
       }
       return err
   }
   ```

   The same-ID `UPSERT` pattern (`INSERT ... ON CONFLICT (id) DO UPDATE`)
   naturally preserves the 3DS upgrade path; only a genuine duplicate
   `idempotency_key` with a different `id` triggers the error branch.

**DynamoDB migration:**

Use a conditional put on `idempotency_key`; catch
`ConditionalCheckFailedException` and translate it to
`&payment.DuplicateIdempotencyKeyError{...}`.

See [`docs/guides/postgres-payment-repository.md`](docs/guides/postgres-payment-repository.md)
for a fuller walkthrough.

### Added

- `payment.ErrDuplicateIdempotencyKey` sentinel error.
- `payment.DuplicateIdempotencyKeyError` typed error with `Key`, `ExistingID`,
  `AttemptedID` fields; `errors.Is` against the sentinel returns `true`.
- `infrastructure/inmemory/payment_repository.go`: simulates the unique
  constraint so tests and demos exercise the same contract production
  backends must honor.
- `tests/integration/payment_idempotency_test.go`:
  `TestPaymentIdempotency_ConcurrentSuccess_Race_Integration` — end-to-end
  reproduction of #97 plus an `AfterCharge` hook assertion that the
  post-convergence invoice re-fetch surfaces the winner's committed state.
- `docs/guides/postgres-payment-repository.md` — reference implementation
  sketch for consumer Postgres adapters.

### Changed

- `PaymentService.ProcessPayment`: on `payment.ErrDuplicateIdempotencyKey`
  inside `RunInTx`, the race loser now converges on the winner's record via
  `FindByIdempotencyKey` and re-fetches the invoice outside the tx so
  `AfterCharge` plugins see the committed invoice state (not the loser's
  stale local clone).

#### `invoice.Repository.Save` must protect concurrent `FinalizeInvoice` (#130)

**Why:** `BillingService.FinalizeInvoice`'s guarantee that two concurrent
finalizations cannot both fire `OnInvoiceIssued` only holds when the repository
serializes the load→check→save sequence. Previously this contract was implicit;
last-writer-wins adapters silently allowed a double finalize and double hook
firing.

**What changed:** `domain/invoice/repository.go` godoc now requires `Save`
implementations to EITHER implement optimistic locking — compare
`Invoice.LoadedVersion()` against the stored version and return an error
matching `errors.Is(err, tx.ErrVersionConflict)` on mismatch, persisting
`Invoice.Version()` on success — OR serialize reads via a row lock
(`SELECT ... FOR UPDATE`) or `SERIALIZABLE` isolation. The `Invoice` API is
extended additively (`Version` / `LoadedVersion` / `SetVersion`, plus
`InvoiceSnapshot.Version`), so adapters still **compile** unchanged.

**Who is affected:** consumers implementing `invoice.Repository` against a
production backend. A non-compliant (unconditional upsert) adapter compiles but
double-fires `OnInvoiceIssued` under concurrent finalization. Track the
adapter-side fix at contract-to-cash/adapters#12.

**Migration recipe (Postgres):** add a `version` integer column and finalize
with a version-guarded update, translating a zero-row result to
`tx.ErrVersionConflict`:

```sql
UPDATE invoices
   SET status = $newStatus, version = version + 1, /* ... */
 WHERE id = $id AND version = $loadedVersion;
```

```go
tag, err := r.db.Exec(ctx, updateSQL, /* ... */ inv.ID(), inv.LoadedVersion())
if err != nil {
    return err
}
if tag.RowsAffected() == 0 {
    return tx.ErrVersionConflict
}
```

Alternatively, load the row `FOR UPDATE` inside the finalize transaction so the
loser blocks until the winner commits and then observes the finalized status.

---

## Prior history

This project did not maintain a `CHANGELOG.md` before this entry. Refer to
the git log for the full commit history and the PR/issue tracker for
contextualized release notes.
