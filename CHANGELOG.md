# Changelog

All notable changes to `github.com/contract-to-cash/core` are documented in this
file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Removed

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

- **Event Sourcing / on-disk schema**: contract event payloads and the contract
  snapshot no longer carry `billing_cycle`. Historical events that recorded only
  `billing_cycle` are migrated on read by new upcasters
  (`ContractCreatedEventUpcaster`, `ContractRenewedEventUpcaster`) which convert
  `billing_cycle` → `interval` and bump the event `SchemaVersion` to 2. Legacy
  contract snapshots (schema_version 0/1) are converted to an interval on
  `LoadFromSnapshot`; new snapshots record `schema_version: 2`. No data migration
  is required — existing streams and snapshots replay correctly.

### Added

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
