# Changelog

All notable changes to `github.com/contract-to-cash/core` are documented in this
file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Fixes from the 2026-07-12 objective review (#231–#246) and the follow-up merge
review (money-path items #233/#234/#235, docs #237/#245/#246). Contains several
**BREAKING** changes, marked below per the pre-v1.0 convention; see the
per-entry upgrade notes.

### Added

- **Per-refund idempotency-key ledger on `Payment` (#235 review)** —
  `payment.RefundEntry`, `Payment.RecordRefundWithKey(amount, key)`,
  `Payment.Refunds()`, `Payment.HasRefundWithIdempotencyKey(key)`,
  `Payment.RefundKeysComplete()`, and the `PaymentSnapshot.Refunds` field. Every
  refund recorded through `PaymentService.Refund` now stores the gateway
  idempotency key it was executed under, which is what makes the refund
  convergence classification exact (see Fixed). The legacy keyless
  `Payment.RecordRefund` remains and marks the ledger incomplete.
  **Upgrade note for BYO persistence adapters**: persist and rehydrate
  `PaymentSnapshot.Refunds`. Not persisting it is safe but degrades — payments
  with refund history then classify concurrent advances as a conservative
  `ErrCodeConflict` instead of recording (see Fixed).
- **`plugin.CompensationReasonIdempotencyConflict` (#234 review)** — new
  `CompensationReason` value reported to `OnCompensationExecutedHook` when saga
  compensation was triggered by the in-tx idempotency conflict with a `Failed`
  payment record (a pre-charge-lookup race). Previously this path was
  mislabeled `local_save_failed` even though no Save was ever attempted; the
  caller-facing error now also names the collision ("idempotency key collided
  with a failed payment record (gateway charge reversed)") instead of claiming
  a local save failure. Additive — Minor.

- **`port.CustomerIDResolver` + `WithCustomerIDResolver` (#231)** — `PaymentService` now
  resolves the gateway-side customer ID before `Charge`/`GetCustomer` instead of passing
  the internal `AccountID` verbatim. Unwired, the identity fallback preserves existing
  behavior — but it only suits gateways that accept caller-chosen customer IDs; Stripe-style
  ID-minting gateways MUST wire a resolver. Resolver errors abort before the gateway call.
- **`infrastructure/inmemory.CouponRepository` (#240)** — shipped reference implementation
  of the atomic `SaveRedemption` contract ((a) idempotent same-key no-op, (b) atomic limit
  enforcement with `ErrUsageLimitReached`, (c) insert — all under one mutex), with
  concurrency tests. The e2e/integration/example bespoke copies now delegate to it.
- **`plugin.Config.Int` / `plugin.Config.Bool` (#239)** — type-coercing config readers
  (accept `int` and integral JSON `float64`; descriptive error on mismatch). Official
  plugins now use them and **return an error from `Initialize` on mistyped values**
  instead of silently applying defaults (**BREAKING** — a config typo now fails startup
  loudly; see the Changed entry).
- **`plugin.FireNonFatal`** — exported composition of `SafeInvoke` +
  `LogNonFatalHookError` for integrator-fired lifecycle hooks (the §5.4 pattern);
  the hosting demo now uses it.
- **`Invoice.ParticipatesInPeriodUniqueness()`** — single-source predicate for the
  period-uniqueness contract (not voided / not proration / non-zero period), used by the
  billing-service guards, the in-memory repository, and referenced by the
  `invoice.Repository.Save` contract docs.
- **`TieredPrice.Validate()` / `UsagePrice.Validate()` (#238)** — graceful pre-checks
  mirroring the constructor invariants, for validating persisted/literal models.
- **`tx.InTransaction(ctx)`** — intent-revealing probe for "running inside a caller's
  transaction" (used by the #233 joined-tx guards).
- **`projection.ErrSubscriptionClosed` (#246)** — `ProjectionService.Start` now returns
  this sentinel when the subscription channel closes while the context is live
  (previously `nil`, indistinguishable from graceful shutdown); a close after
  cancellation consistently returns `ctx.Err()`.
- **`batch.BatchResult.Skipped` + `DryRunActions` (#242)** — result accounting invariant
  `Total == Succeeded + Failed + Skipped`; renewal dry-runs now mirror the real run's
  classification (`renew`/`expire`/`cancel` actions) instead of reporting
  `cancelAtPeriodEnd`/`autoRenew=false` contracts as failures. External context
  cancellation now surfaces as a non-nil error from `Process` (previously a cancelled
  run could look like a clean one).

### Changed

- **BREAKING — `ProcessPayment` rejects an empty `IdempotencyKey` (#241)** with
  `ErrCodeValidation` before any work. Previously an empty key silently disabled every
  dedup layer (a retried request double-charged). Callers must supply a key; the e2e
  harness shows the pattern.
- **BREAKING — refund idempotency keys now bind the amount (#235)**:
  `refund-<paymentID>-<currency>-<prior>-<amount>` (was `...-<prior>`), and `Refund` now
  guarantees **at most one gateway movement per invocation**: the gateway is called once
  before the recording transaction; concurrent same-amount duplicates collapse to a single
  movement (the loser gets `ErrCodeConflict`); concurrent distinct amounts are both real
  and both recorded; explicit-key conflicts are never recorded and log a
  MANUAL RECONCILIATION error. **Upgrade note**: the key format changed — a refund left
  ambiguous across the deploy (gateway moved money, local record failed) must be verified
  at the gateway before retrying, because the retry will derive a fresh key.
- **BREAKING — `NewInvoice(..., WithStatus(s))` errors for `s != draft` (#238)** —
  non-draft statuses carry state construction cannot supply (paid amounts, void reasons),
  so minting them created invariant-violating invoices. Persistence adapters keep using
  `InvoiceFromSnapshot`; tests use real transitions.
- **BREAKING — `NewInvoice(..., WithRevisionOf(id))` / `WithOriginalInvoiceID(id)` reject a
  self-reference (#238)** — an invoice can no longer be constructed as a revision (or
  reissue) of itself; `NewInvoice` returns `ErrCodeValidation` when either option carries
  the invoice's own ID. A self-referencing revision link corrupted the revision chain
  (walking `revisionOf`/`originalInvoiceID` loops forever). Consumers that passed the same
  ID (always a bug) must pass the actual predecessor's ID.
- **BREAKING — official plugins reject mistyped `Initialize` config values (#239)** — a
  config typo (e.g. a string where an int is expected) that previously ran silently with
  the default value now fails `Registry.InitializeAll` at startup with a descriptive error
  naming the key. JSON-loaded numeric values (integral `float64`) remain accepted via
  `plugin.Config.Int`. Fix the config value's type to upgrade.
- **BREAKING — `coupon.NewCoupon` returns `(*Coupon, error)` (#244)** and rejects a nil
  value, so a nil-value coupon fails at construction instead of panicking inside the
  billing pipeline.
- **BREAKING — `Money.Multiply(nil)` panics (#244)** instead of silently returning zero
  (a nil rate is a caller bug that previously produced a silent zero charge). The official
  tax plugin guards its `TaxCalculator` contract and returns a clean `ErrCodeBusinessRule`
  error for a nil rate — custom `TaxCalculator`s must return `big.NewRat(0,1)` for
  "no tax", never nil.
- **BREAKING — struct-literal `TieredPrice`/`UsagePrice` bypasses now fail loudly (#238)**:
  `CalculatePrice` validates tier ordering and clamp currencies (previously produced
  silently wrong amounts, e.g. a wrong-currency `Maximum` was ignored and usage bills
  sailed past their cap). The billing pipeline converts a poisoned *persisted* price into
  a per-contract `DomainError` naming the price (no process crash); direct misuse panics
  per the documented policy.
- **BREAKING — `Contract.Create` validates `ContractType` (#243)** — unknown/empty types are
  rejected at command intake with `ErrCodeValidation` (previously persisted uncorrectably and
  failed at first invoice). `ContractType` is an open string type, so consumers that passed
  custom values (which previously flowed through) now get a validation error; use the
  shipped `one_time` / `subscription` / `usage_based` constants.
- **`DateRange.Next` / `AddBillingCycleDuration` deprecated (#244)** — they silently
  default unknown cycle strings to monthly; use `pricing.BillingInterval`.

### Fixed

- **BREAKING — refund convergence can no longer book a phantom refund under
  3+ concurrent writers (#235 review)** — the old classification reasoned from
  the cumulative refunded total alone, so with three racing refunds
  A(3000)/B(3000)/C(2000) all loading prior=0 and committing A→C→B, the
  replayed B observed an advance of 5000, was misclassified as "real movement",
  and recorded 3000 the gateway never moved (ledger 8000 vs gateway 5000,
  silently). `Refund` now matches this invocation's exact gateway key against
  the payment's per-refund key ledger: a recorded refund carrying the key
  proves a no-movement replay (conflict, no record); an absent key with a
  COMPLETE ledger proves a real movement (recorded). The invariant is now
  strict: **no path records an amount the gateway did not move**. BREAKING
  edges: (1) payments whose refund history lacks complete keys (pre-upgrade
  data, keyless `RecordRefund` bookkeeping) get a conservative
  `ErrCodeConflict` + MANUAL RECONCILIATION error log on concurrent advances
  where the old code recorded; (2) re-invoking `Refund` with an explicit key
  that is already recorded now returns `ErrCodeConflict` instead of silently
  double-booking the gateway's replay.
- **Duplicate-key convergence now dispatches on the winner's status
  (#234 review)** — the race loser previously returned whatever record the
  winner wrote as unconditional success: a PENDING winner (3DS
  requires_action / async settlement) was returned as `(payment, nil)` and the
  loser fired `AfterCharge`/`OnPaymentProcessed` for a non-Completed payment.
  Now: Completed → success (unchanged); Pending → `(pendingPayment,
  ErrPaymentPending)` with no success hooks and no outbox fire (same result
  shape as the first caller's pending path); Failed/Refunded/
  PartiallyRefunded/ChargedBack → `ErrCodeConflict`, mirroring the in-tx
  idempotency switch. No branch fires saga compensation. Applies to both the
  gateway and zero-amount call sites.
- **Duplicate-invoice guards now exempt proration invoices (#232)** — a mid-period
  proration invoice no longer permanently blocks `GenerateInvoice` /
  `RegenerateInvoice` / `ReissueInvoice` for its period (matching the repository
  uniqueness contract and both SQL adapters' indexes). `RegenerateInvoice`'s
  voided-invoice selection likewise excludes prorations, so a voided proration can no
  longer become the revision root, hijack the balance restoration target, or authorize
  minting a net-new invoice.
- **Joined-tx duplicate-key race no longer refunds the winner's charge (#233)** —
  inside a caller's transaction, `ProcessPayment` returns a retryable conflict instead
  of reading through the aborted tx and firing saga compensation; a top-level
  winner-read failure also converges without compensation.
- **Terminal-state replay no longer double-refunds (#234)** — an in-tx idempotency
  collision with a `Refunded`/`PartiallyRefunded`/`ChargedBack` record converges without
  compensation (WARN logged); `Failed` still routes to compensation because a captured
  response cannot be a replay of a never-captured charge.
- **Silent misbilling guards (#241)** — the billing pipeline and
  `RestoreBalancesForVoidedInvoice` now fail loudly when `WithBalanceRepo` is wired but
  the TxManager's repos omit `Balances` (previously credit application / restoration was
  silently skipped); `Refund`'s in-tx reload nil-guard added; zero-amount duplicate-key
  losers converge after the tx exits.
- **Trial-expiration decisions re-evaluated inside the transaction (#242)** — a payment
  method detached between scan and tx can no longer auto-convert a trial the
  `RequirePaymentMethod` gate should block. Renewal `ContractChangeEvent`s now populate
  `OldPriceID`/`NewPriceID` when a pending price change is applied.
- **`ContractExpiredEvent` clears pending price change and trial state (#243)** — a
  terminal Expired contract no longer reports `HasPendingChange() == true` forever.
- **Reference demo corrected (#236)** — `hosting-integration-demo` no longer double-fires
  `AfterCharge` (core fires it), fires lifecycle hooks after save (not before), isolates
  integrator-fired hooks via `plugin.FireNonFatal`, and uses the injected clock.
- **`eventstore.Store` godocs now specify the full BYO implementer contract (#237)** —
  conflict-error encoding for `tx.RetryOnConflict`, Load ordering, gap-free monotonic
  `GlobalPosition` visibility, Subscribe semantics, empty-stream behavior, and
  `Event.Version` handling. `BaseAggregate.UncommittedEvents` returns a copy.
- **Coupon plugin no longer writes the core-owned `SetSubtotalAfterDiscount` (#244)**.

### Docs

- Canonical docs synced with all behavior changes above: `payment-gateway.md`
  (empty-key rejection, amount-bound refund keys + convergence policy, terminal-state
  policy, `CustomerIDResolver`), `domain-model.md` (17 domain events incl.
  PastDue/Recovered, `WithStatus` draft-only, `ContractType` validation, Expired-clearing,
  `ParticipatesInPeriodUniqueness`), `plugin-system.md` (pre-1.0 versioning note, §5.4
  Name/Priority isolation gap, `Config.Int/Bool` + `FireNonFatal`, in-memory
  `CouponRepository` reference), SECURITY.md (tagged releases exist; pin the latest tag),
  CLAUDE.md (lint scope, CreditNote lifecycle), and removal of the ghost `domain/billing`
  package references.
- `payment-gateway.md` §6.1/§6.3 synced with the merge-review money-path fixes above:
  per-refund key-ledger classification table + adapter upgrade note, winner-status
  dispatch on duplicate-key convergence, and the `idempotency_conflict` compensation
  reason for the Failed-state collision (also added to `plugin-system.md` §3.7).
- `event-sourcing.md` caught up with shipped behavior (#237/#246, canonical-docs
  policy): the `ProjectionService.Start` pseudocode now shows the real
  channel-close semantics (`projection.ErrSubscriptionClosed` when the feed dies
  with a live context — a supervisor-restart signal — vs `ctx.Err()` on
  cancellation); new §2.4 summarizes the BYO `eventstore.Store` implementer
  contract (conflict-error encoding required by `tx.RetryOnConflict`,
  Version-ascending Load ordering, empty-stream convention, gap-free monotonic
  `GlobalPosition` visibility, Subscribe semantics, `Event.Version` stamping,
  snapshot selection) with `eventstore/store.go` godoc as normative; new §6.2
  documents `GetContractAsOf`'s empty-aggregate-for-nonexistent-contract
  convention and the per-stream monotonic-OccurredAt assumption.
- `plugin-system.md`: §11.4 zero-amount outbox table row updated to the actual
  post-tx duplicate-key convergence (`errDuplicateKeyRaceSignal` →
  `convergeOnDuplicateKeyWinner`; the pre-#241b in-closure wording was stale);
  §10.3 no longer asserts a v1.0.0 initial release (the library is v0.x).
- `docs/architecture.md` §2.2 dependency graph gained the
  `infrastructure/inmemory -.-> plugins` implements-edge (in-memory
  `CouponRepository`, #240). README.md / README.ja.md architecture trees no
  longer list the nonexistent `domain/billing` package.

## [0.7.0] - 2026-07-14

### Added

- **`PaymentInstructions` on `ChargeResponse` for asynchronous / push charges
  (konbini vouchers, bank-transfer virtual accounts)** — async gateways return
  customer-facing payment instructions (voucher URL, reference number, expiry)
  that `ChargeResponse` could not previously carry; adapters smuggled a URL
  through `ThreeDSecureResult` and the pending-payment path dropped it entirely.
  Adds the additive `PaymentInstructions` type (`kind` / `url` / `reference` /
  `expires_at`) to `ChargeResponse`, and `PaymentService.ProcessPayment` now
  persists it on the unsettled payment's metadata under reserved keys
  (`payment.MetadataKeyInstructions*`) in `persistUnsettledCharge`, so the
  payment returned with `ErrPaymentPending` / `ErrRequiresAction` already
  carries what the integrator needs to notify the customer. Spec:
  `payment-gateway.md` §6.5.6. Additive only — adapters that leave
  `Instructions` nil are behavior-identical. Minor bump.

- **`OnCompensationExecutedHook` — non-fatal plugin hook for saga compensation
  (#257)** — closes the observability blind spot on the `ProcessPayment`
  "gateway charge succeeded → local transaction failed → saga compensation
  (Void / fallback Refund)" path, which previously emitted only slog lines.
  The core now fires the new hook after the compensation attempt, on **both**
  outcomes: charge reversed (Method `void` or `refund`) and the double-failure
  MANUAL RECONCILIATION state (Method `none`, `CompensationErr` non-nil — the
  case integrators most need to page on). `CompensationResult` carries the
  original gateway transaction ID and amount, the reversal method, the trigger
  reason (`local_save_failed` or `outbox_veto`, issue #248), and any
  `MarkCompensated` marker-write failure (issue #87). Hook errors and panics
  are logged via the standard non-fatal policy and never change the outcome of
  `ProcessPayment`. Hook interface total goes from 22 to 23 (payment category
  4 → 5). Additive only — Minor bump.

## [0.6.0] - 2026-07-13

### Added

- **`ProcessPaymentInput.ReturnURL` (platform#66)** — optional URL the customer is
  sent back to after approving a redirect-based payment (qr_code wallets such as
  PayPay, card 3DS challenges). When non-empty, `PaymentService.ProcessPayment`
  propagates it to the gateway as `ChargeRequest.ThreeDSecure.ReturnURL`
  (`ThreeDSecureRequest.Required` is intentionally not set — forcing a 3DS
  challenge is a separate concern); when empty, `ChargeRequest.ThreeDSecure`
  stays nil, so existing callers and gateway adapters are unchanged. Additive
  only — backward compatible, Minor bump.

- **`PaymentMethodType` hint on `ChargeRequest` / `AuthorizeRequest` (#253)** —
  optional field naming the payment method's type as known to the caller (e.g.
  from the stored `PaymentMethodDetail`). Gateway adapters MAY use it to skip a
  per-charge payment-method lookup; the zero value means "unknown" and adapters
  resolve the method themselves as before. `PaymentService.ProcessPayment`
  forwards `ProcessPaymentInput.PaymentMethod` as this hint only when the caller
  also set `PaymentMethodID` explicitly — when the method ID was resolved via the
  Invoice→Contract→Customer fallback chain, no hint is sent (the declared type
  may not describe the resolved method, and a wrong hint is worse than none).
  Additive only — existing gateways and callers are unaffected.

## [0.5.0] - 2026-07-13

### Added

- **First-class asynchronous settlement for pay-later payment methods (#252)** —
  bank transfer, convenience-store (konbini), carrier billing, and similar methods
  where the gateway accepts the charge but the customer pays out-of-band later.
  `PaymentService.ProcessPayment` now treats a gateway `TransactionStatusPending`
  charge response as a first-class outcome instead of an unexpected-status error:
  it persists a **Pending** payment (idempotency key, gateway transaction ID,
  resolved method type), leaves the invoice untouched, and returns the new exported
  sentinel **`service.ErrPaymentPending`** (check with `errors.Is`; the mirror of the
  existing 3DS `ErrRequiresAction`). No saga compensation fires — nothing was
  captured, so there is nothing to reverse. The existing in-transaction
  Pending→Completed promotion and idempotent-replay convergence apply unchanged, so
  a retry whose charge now replays as Captured/Succeeded upgrades the same record.

- **Settlement APIs `SettlePayment` / `MarkPaymentFailed` (#252)** — integrator
  entry points for the async funds lifecycle, called from webhook handling
  (`payment.received` / instruction-expired events):
  - `SettlePayment(ctx, paymentID)`: Pending→Completed — records the amount on the
    invoice and saves both in one transaction (`tx.RetryOnConflict` + `tx.Run`, the
    same optimistic-locking pattern as `Refund` / `FinalizeInvoice`), fires the
    `PaymentOutboxWriter` in-tx after both saves and before commit (plugin-system.md
    §11 contract), and fires `AfterCharge` + `OnPaymentProcessed` hooks post-commit
    non-fatally on an actual transition only. Idempotent: settling an
    already-Completed payment is a no-op success (safe under at-least-once webhook
    redelivery); terminal states are rejected with `invalid_state_transition`.
  - `MarkPaymentFailed(ctx, paymentID, reason)`: Pending→Failed (voucher lapsed,
    transfer never arrived); the invoice is untouched, `OnPaymentFailed` hooks fire
    post-commit non-fatally on an actual transition. Already-Failed is a no-op;
    a Completed payment is never knocked back by a late expiry notification.

  A `SettlePayment` outbox veto moves no gateway money (the funds already arrived
  out-of-band), so its rollback is harmless and converges via webhook redelivery —
  documented in the `PaymentOutboxWriter` contract and plugin-system.md §11.4/§11.5.

### Documentation

- `docs/internals/payment-gateway.md`: refreshed the stale §6 service summary to the
  current source, added §6.4 documenting the previously undocumented
  Invoice→Contract→Customer `ResolvePaymentMethod` fallback chain (including the
  zero-amount skip and the separate method-*type* resolution order), and added §6.5
  for the pending outcome and settlement APIs. `docs/internals/plugin-system.md`
  §5.3 hook-firing and §11.4 outbox firing-path tables now include the settlement
  APIs; `docs/concepts/payment-gateway.md` gained the matching EN summary section.

Additive only — no plugin hook signature changes, no `payment.Repository` interface
changes, and the synchronous card path is behaviourally unchanged. Minor bump.

## [0.4.0] - 2026-07-13

### Added

- **Transactional outbox writer ports (#248)** — two new integrator ports,
  `port.PaymentOutboxWriter` and `port.InvoiceOutboxWriter`, that the core calls
  *inside* the bookkeeping transaction (immediately after the payment/invoice row is
  saved, before commit) so an integrator can write a durable notification row in the
  SAME transaction as the write — closing the event-loss window that the post-commit
  hooks (`AfterCharge` / `OnPaymentProcessed` / `OnInvoiceIssued`) cannot. Wire them via
  `service.WithPaymentOutboxWriter(...)` (fires on `ProcessPayment`'s Pending→Completed
  promotion and normal success paths, including zero-amount settlement — but NOT on
  idempotent-replay / race-convergence paths) and `service.WithInvoiceOutboxWriter(...)`
  (fires on `FinalizeInvoice`'s save). A writer error vetoes (rolls back) the transaction;
  on the payment path that reverses the successful gateway charge via saga compensation
  (`OnInvoiceFinalized` moves no money, so its rollback is a harmless re-finalize). Writer
  panics are isolated via `plugin.SafeInvoke` (converted to `*plugin.PluginPanicError`,
  never propagated through `tx.Run`); a wired writer under a default `NoopTxManager` logs a
  dedicated non-atomicity warning. Additive and non-breaking: unset writers skip the outbox
  stage entirely (existing behaviour unchanged), the `plugin` package and registry are
  unchanged, and the hook count stays at 22. Design and per-path firing table:
  `docs/internals/plugin-system.md` §11; how-to: `docs/guides/integration.md`.

- **Zero-interval `one_time` contracts + `pricing.NewOneTimePrice` (#218)** — first-class
  one-time modeling: `CreateContractCommand.Interval` may now be omitted (zero) when
  `ContractType == ContractTypeOneTime`; the contract activates (or converts from trial)
  with an unset `CurrentPeriod()` (zero-value `DateRange`), is excluded from
  `FindDueForRenewal` / `FindExpiring` and the renewal batch (the processor now counts a
  force-fed zero-interval contract as `BatchResult.Skipped` with a Warn log instead of
  failing it), and `RenewWithInterval` rejects it with a clear business-rule error.
  `pricing.NewOneTimePrice(productID, amount, currency, createdAt, opts...)` builds the
  matching interval-less Price (zero `Interval()`, empty `BillingCycle()`, nil pricing
  model); `NewPriceWithInterval` still rejects zero intervals. Non-breaking/additive: no
  event-schema changes (a zero interval serializes as `interval:null` and passes the
  existing upcaster chain unchanged), other contract types still require an interval with
  the same error, and existing one_time contracts created WITH an interval replay and
  snapshot-restore unchanged. `LoadFromSnapshot` now accepts a zero-interval one_time
  snapshot instead of misclassifying it as a broken legacy (billing_cycle-only) snapshot.

- **`OnContractCancelScheduledHook` / `OnContractCancelUnscheduledHook` (#227)** — two new
  integrator-fired contract lifecycle plugin hooks mirroring the existing five
  (Create/Activate/Suspend/Resume/Cancel). They correspond to the aggregate's existing
  `ScheduleCancellation` / `UnscheduleCancellation` operations: the integrator calls the
  aggregate method, fires `registry.GetOnContractCancelScheduledHooks()` /
  `GetOnContractCancelUnscheduledHooks()`, then saves (reference:
  `examples/hosting-integration-demo/main.go`). No event-schema or `ContractChangeType`
  changes; hook interface total goes from 20 to 22.

### Documentation

- **Cross-links to the `adapters` repository (#199)** — README (EN/JA), the integration
  guide, and the Postgres payment repository guide now point BYO-DB integrators to
  [contract-to-cash/adapters](https://github.com/contract-to-cash/adapters)
  (PostgreSQL/MySQL persistence, Stripe/fincode payment gateways) as the primary starting
  point, with a compatibility note: adapters is versioned separately (currently targeting
  core v0.2.0) — check its README/`go.mod` for the supported core version and pin
  compatible tags together.

## [0.3.0] - 2026-07-12

Implements field-driven requests from platform integration (#219, #221, #223,
#220 docs) plus review fixes. Contains one **BREAKING** plugin-API change
(`OnPaymentProcessedHook`), marked below per the pre-v1.0 convention.

### Added

- **Integrator-defined `metadata` on Price and ContractAggregate (#219)** — Stripe-style
  `map[string]string` metadata so integrators can attach their own keys (`creator_id`,
  external refs) instead of overloading display names. `pricing.NewPrice` /
  `NewPriceWithInterval` accept a new variadic `PriceOption` (`pricing.WithMetadata`,
  additive — existing call sites compile unchanged) and expose `Price.Metadata()`;
  `CreateContractCommand.Metadata` is recorded on `ContractCreatedEvent` (schema v4,
  with a v3→v4 upcaster; replay of historical events and legacy snapshots is unaffected)
  and exposed via `ContractAggregate.Metadata()`. All maps are defensively copied at
  every boundary. `Product` already supported metadata; it is unchanged.
- **Coupon getters for persistence round-trips (#221)** — `Coupon.Currency()`,
  `ApplicableContractTypes()`, `AllowedAccountIDs()`, `BlockedAccountIDs()`, so a
  `CouponRepository.Save` implementation can persist and faithfully reconstruct a coupon
  via `NewCoupon` + `With*` builders without a side table. Slice-returning getters
  (including the existing `ApplicableTo()`) now return defensive copies.

### Changed

- **BREAKING — `OnPaymentProcessedHook` now receives `*plugin.PaymentContext` (#223)** —
  the signature changed from `OnPaymentProcessed(ctx *Context, payment *payment.Payment)`
  to `OnPaymentProcessed(ctx *PaymentContext)`, aligning it with the other payment hooks.
  `ctx.Payment()` is the processed payment; `ctx.Invoice()` / `ctx.ContractID()` /
  `ctx.AccountID()` let metrics plugins attribute `payment.processed` events to a
  contract/account without an extra invoice lookup per event. Note that because Go
  interface satisfaction is structural, an integrator plugin still implementing the old
  signature will NOT fail to compile — it silently stops satisfying
  `OnPaymentProcessedHook` and drops out of the `Registry`, so its metrics go quiet;
  add a compile-time assertion such as
  `var _ plugin.OnPaymentProcessedHook = (*MyPlugin)(nil)` to surface this at build time.

### Fixed

- **Zero-amount settlement re-fetches the invoice before firing hooks after a
  raced-loser convergence (#97, found during the #223 review)** —
  `settleZeroAmountPayment` now mirrors the gateway path's #97 handling: when its
  payment `Save` loses a duplicate-idempotency-key race and converges on the winner's
  payment, the invoice is re-fetched from the repository before `AfterCharge` /
  `OnPaymentProcessed` fire. Previously the hooks on this path could observe a
  locally-mutated but never-persisted invoice (the loser's clone, whose
  `RecordPayment` mutation was rolled back with the transaction).

### Docs

- **`ContractChangeExpired` semantics reconciled with code (#220)** — the hook-constant
  comment, `docs/internals/plugin-system.md` §3.8/§5.3, and a `batch/contract_renewal.go`
  comment claimed a scheduled cancellation (`cancelAtPeriodEnd`) ends in `Expired`; the
  actual (and intended) behaviour is that `RenewWithInterval` resolves it to `Cancelled`
  at the period boundary and it is reported as `ContractChangeCancelled` (user-initiated
  churn), while `Expired` is reserved for `autoRenew=false` natural term end. No behaviour
  change.

## [0.2.0] - 2026-07-11

First curated release. Everything below was previously accumulated under
[Unreleased]; consumers pinning pseudo-versions of `main` should move to this
tag. Pre-v1.0: minor versions may contain breaking changes, each marked
**BREAKING** in its entry.

(Note: a `v0.1.0` tag exists from the 2026-07-03 introduction of the release
workflow itself (PR #127); it predates this changelog's curation and carries
no release notes. `v0.2.0` is the first release with tracked contents.)

### Docs

- **Reconciled `docs/internals/plugin-system.md` and `docs/architecture.md` with the current code (#198)** —
  the plugin-system spec and architecture overview had drifted from post-#185/#188/#189/#193/#210 code.
  Corrected: §3.8 now lists `ContractChangeExpired`; §4.1 registry pseudo-code now shows
  priority-ordered `sortedCopy` getters, priority-ordered `InitializeAll`/reverse-order `ShutdownAll`
  with `SafeInvoke` and first-error-abort, and the real `sortByPriority`/`sortedCopy` helpers; §5.1
  and §8.1 now document the minor-unit rounding steps (#189), `SetBillingPeriod`, and `SafeInvoke`
  wrapping; §5.3 firing table now reflects `expired`; §6.1 coupon example matches the actual plugin
  (implements `DiscountHook`+`InvoiceLifecycleHook`, defaults `MaxCouponsPerInvoice=1`, `CouponQuery`,
  first-valid-wins, no persistence in `CalculateDiscount`); §6.2 adds the `CouponQuery` struct; §7
  tax-priority comment no longer implies cross-type ordering comes from `Priority`. `architecture.md`
  §2.2 adds the `domain/contract → eventstore` edge and both `architecture.md` and `CLAUDE.md` now
  state precisely that `domain/` may depend on the same-module `eventstore/` interfaces (no cycle).

### Fixed

- **Domain-layer validation & immutability gaps (#196)** — a batch of
  commercial-readiness hardening across the domain layer. Several sub-items are
  **BREAKING** (pre-v1.0) as marked.
  - **BREAKING — `pricing.NewPrice` / `pricing.NewPriceWithInterval` now return
    `(*Price, error)`.** A `Price` is immutable, so the constructors now reject a
    negative base amount, a base amount whose currency disagrees with the
    `currency` argument (when non-zero), a zero-value `BillingInterval`
    (`NewPriceWithInterval`), and an **unknown `billingCycle`** — the latter uses
    the Strict converter and fails loudly instead of silently coercing to Monthly,
    mirroring the event-upcaster policy. All callers must handle the error.
  - **`pricing.Price.PricingModel()` now returns a defensive copy.** Mutating the
    returned model (e.g. the exported `TieredPrice.Tiers` backing array) no longer
    reaches into the Price's internal model or changes subsequent `CalculatePrice`
    results. New `TieredPrice.Clone()`.
  - **New `balance.BalanceEntry.ConsumeAt(amount, now)`** enforces expiry:
    consuming from an entry that has expired as of `now` is rejected with a
    `business_rule` error. `Consume(amount)` remains as the expiry-agnostic
    primitive (documented; used only where expiry was already filtered). The
    billing pipeline's `applyBalances` now consumes via `ConsumeAt`.
  - **New `shared.Money.GreaterThanStrict(other) (bool, error)`** reports a
    currency mismatch as an error instead of the legacy `GreaterThan`'s silent
    `false`. The three warned financial guards
    (`CreditNote.validateAdjustmentAmount`, `Invoice.WithAmountDue`,
    `CreditNoteService` cumulative-credit check) now use it; `GreaterThan` is
    retained and documented for same-currency comparisons.
  - **`contract.ContractAggregate.UnscheduleChange`** now rejects terminal
    (cancelled/expired) contracts, and the `ContractCancelledEvent` `Apply` now
    clears `pendingPriceID` / `trialConfig` so a cancelled contract no longer
    reports `HasPendingChange()==true` or retains a live trial config. Clearing in
    `Apply` is replay-safe (deterministic, idempotent).
  - **`invoice.NewCreditNote`** now rejects a **negative item `taxAmount`** (it
    would understate the note's tax and total).
  - **`contract.ContractAggregate.Create`** now validates its command against the
    immutable event stream: a nil `Clock` (from `NewContractAggregate(id, nil)`)
    returns a validation error instead of panicking; empty `AccountID`, a command
    with neither `PriceID` nor a non-zero `Price`, and a `Price`/`BasePrice`
    currency mismatch (both non-zero) are rejected.
  - **`contract.ContractAggregate.LoadFromSnapshot`** legacy branch now uses the
    Strict `billing_cycle` converter and fails loudly on an unknown/absent cycle
    instead of silently coercing to Monthly (mirrors the upcaster; genuinely-valid
    legacy snapshots still load).
  - **`invoice.WithAmountDue`** now also syncs `balance` (previously only
    `amountDue` was set, leaving `balance` stuck at the full total), and its
    over-total check uses `GreaterThanStrict`.
  - **`contract.ContractAggregate.StartTrial`** now validates its config against
    the aggregate's clock: a zero/past `TrialEndDate` and negative
    `ConversionReminderDays` are rejected.
- **Application / eventstore / batch consistency gaps (#197)** — a batch of
  correctness fixes across the service, plugin, batch, and in-memory layers:
  - **Monotonic ULID generation**: `generateULID()` (all `NewXxxID()` constructors
    and `GenerateID()`) now uses a process-wide `ulid.LockedMonotonicReader` over
    crypto/rand, so IDs minted within the same millisecond are strictly increasing
    in creation order. Previously same-millisecond IDs had random relative order,
    breaking the documented "IDs are lexicographically sortable by creation order"
    guarantee (and making the latest-voided-invoice selection below nondeterministic
    on fast machines). Behavioral only — the ULID format is unchanged, and
    crypto/rand entropy is retained (deliberately not switched to `ulid.Make()`,
    whose default entropy is a time-seeded math/rand PRNG).
  - **Upcaster order-dependence**: `ContractCreatedIdempotencyKeyUpcaster.CanUpcast`
    now matches `fromVersion == 2` (was `<= 2`). Registered before
    `ContractCreatedEventUpcaster`, the old guard let a v1 payload jump straight to
    v3 and skip the v1→v2 `billing_cycle`→`interval` migration; the exact-version
    guard makes the fixpoint chain run 1→2→3 regardless of registration order
    (mirrors `ContractSuspendedEventUpcaster`). Added a chain-order-permutation test.
  - **Snapshot of a dirty aggregate**: `SnapshotService.CreateSnapshot` now errors
    (`business_rule`) when the aggregate has uncommitted events, instead of writing
    a state/version-inconsistent snapshot. Persist (append + `ClearUncommittedEvents`)
    before snapshotting.
  - **Non-deterministic revision chain**: `BillingService.RegenerateInvoice` links
    the revision chain to the voided invoice with the greatest ID (ULID creation
    order) rather than a map-ordered "last seen" invoice, so regeneration is
    reproducible when a period was void-and-recreated more than once.
  - **Inconsistent usage line items**: usage line items now record the exact average
    per-unit price (`metricPrice / billableUsage` over `big.Rat`) so
    `quantity × unitPrice == amount`; previously `unitPrice` held the whole metric
    charge, breaking the identity for any quantity ≠ 1.
  - **Refund gateway/ledger disagreement**: `PaymentService.Refund` sends the
    resolved refund amount (`&refundAmount`) to the gateway on the full-refund path
    instead of `nil`, so the gateway and the local ledger always agree on one figure.
  - **`FindByID` nil-guards**: `PaymentService` and `CreditNoteService` now defend
    against a repository returning `(nil, nil)` for a missing entity (BYO-DB
    defensiveness), returning a clean `not_found` error instead of nil-panicking.
    The `FindByID` not-found convention (return `ErrCodeNotFound`, never `(nil, nil)`)
    is now documented on the `contract` / `invoice` / `payment` / `CreditNote`
    repository interfaces.
  - **Credit note ledger posting**: `CreditNoteService.ApplyCreditNote` posts the
    applied amount to the account's credit ledger as a spendable `BalanceEntry`
    (inside the same transaction) when a balance repo is wired via the new
    `WithCreditNoteBalanceRepo(...)` option, so "apply to account" is a real credit
    rather than a status-only change. Backward-compatible: no balance repo → status
    transition only. (No new plugin hook was added — the ledger entry is the record
    of the application; the 20-hook surface is unchanged.)
  - **Plugin lifecycle robustness**: `Registry.InitializeAll` now rolls back the
    already-initialized plugins (Shutdown, reverse order) when a later Initialize
    fails, instead of leaking half-initialized plugins; `Registry.ShutdownAll` now
    attempts every plugin and returns all errors joined instead of aborting on the
    first (matching `docs/internals/plugin-system.md` §4.1).
  - **Zero-amount settlement**: `PaymentService.ProcessPayment` settles a
    zero-amount invoice (fully discounted / credited) directly without calling the
    gateway — real gateways reject a zero-value charge. It skips `BeforeCharge` (a
    gateway pre-flight) but fires `AfterCharge` + `OnPaymentProcessed`, and requires
    no payment method. Idempotency is honoured.
  - **In-memory repository isolation**: `InMemoryProductRepository` now clones
    products via a snapshot round-trip (Product is a mutable entity) instead of
    sharing raw pointers; `InMemoryBalanceRepository.SaveApplication` /
    `SaveRefund` now upsert by ID (idempotent under tx retry) and return isolated
    copies.
  - **BREAKING (repository implementors)**: the batch finder methods gained a
    trailing `limit int` parameter —
    `contract.Repository.FindDueForRenewal(ctx, asOf, limit)`,
    `contract.Repository.FindTrialsEndingBefore(ctx, before, limit)`, and
    `balance.Repository.FindExpired(ctx, asOf, limit)` — threaded from the new
    `batch.BatchOptions.Limit` field so a run against a large due-set does not load
    the entire backlog. `limit <= 0` means unbounded (preserves prior behaviour);
    a positive limit returns the oldest-eligible rows first. BYO-DB adapters must
    add the parameter and push it into the query.

- **Voiding an invoice now restores the credit balance it consumed (#184)** —
  previously, when an invoice that had drawn down account credit (via FIFO
  `applyBalances`) was voided, the consumed `BalanceEntry` amounts were never
  returned to the ledger: the credit stayed consumed against the dead invoice
  forever (silent customer money loss). This affected
  `CreditNoteService.ReissueInvoice` (the replacement billed full price while the
  credit stayed spent) and `plugins/invoicecleanup` (account-scoped credit
  destroyed on contract cancellation). The fix:
  - New `balance.BalanceEntry.Restore(amount)` — the inverse of `Consume`:
    returns consumed credit, rejects negative amounts and restoring more than was
    consumed, bumps the optimistic-lock version, and deliberately ignores expiry
    (a restored-but-expired entry is swept later by
    `batch.BalanceExpirationProcessor`).
  - New `BillingService.RestoreBalancesForVoidedInvoice(ctx, invoiceID)` and the
    internal reversal it wraps: reads `FindApplicationsByInvoice`, restores each
    consumed entry, and records a `BalanceRefund` audit row. Idempotent — a
    double void / retry restores each application at most once, guarded by the
    new refund records. The public method loads the invoice through the
    transaction-scoped repository and rejects restoration with a `business_rule`
    `DomainError` unless the invoice is actually voided (restoring a live
    invoice's applications would fabricate spendable balance); a missing invoice
    errors instead of silently no-oping.
  - Wired into the void paths in the same transaction:
    `CreditNoteService.ReissueInvoice` restores before generating the
    replacement, and `BillingService.RegenerateInvoice` restores the voided
    invoice's credit before re-applying it.
  - `plugins/invoicecleanup` now **skips** (does not void) Draft/Finalized
    invoices with `AppliedBalance() > 0`, since it cannot restore credit
    atomically; such invoices are left for a credit-restoring void path.
  - **BREAKING**: `balance.Repository` gains
    `FindRefundsByInvoice(ctx, invoiceID)`, and `balance.BalanceRefund` gains
    `InvoiceID` / `ApplicationID` fields. Custom `balance.Repository`
    implementations must implement the new method.
- **Month-end billing-anchor drift (#186)** — `pricing.BillingInterval.AddTo` used
  `time.AddDate`, whose month-end overflow normalization drifted the billing anchor
  permanently for contracts starting on the 29th–31st (`Monthly().AddTo(Jan 31)` →
  `Mar 3`, next → `Apr 3`, …) and for yearly contracts starting on Feb 29
  (→ Mar 1). Two-layer fix:
  - `AddTo` now performs calendar-correct month/year addition, clamping the
    day-of-month to the last valid day of the target month (`Jan 31 + 1mo` →
    `Feb 28`/`Feb 29`; `Feb 29 + 1yr` → `Feb 28`). Day/week addition is unchanged.
  - New `AddToWithAnchorDay` plus a derived `ContractAggregate.billingAnchorDay`
    (exposed via `BillingAnchorDay()`) preserve the original anchor across
    successive renewals so a month-end subscription bills `Jan 31 → Feb 28 →
    Mar 31 → Apr 30 → May 31` instead of drifting downward. `RenewWithInterval`
    now uses this anchor.
  - Replay-safe: the anchor is reconstructed from the initial period's start day
    on `ContractActivatedEvent` / `TrialEndedEvent`, so no event-schema change or
    upcaster is needed and existing streams rehydrate identically. The contract
    snapshot gains `billing_anchor_day` (`schema_version` 3); legacy snapshots
    fall back to the current period's start day. Already-drifted historical
    contracts self-heal back to their anchor on the next renewal.
- **Coupon usage limits are now enforced atomically at redemption confirmation
  (#195)** — after #185 moved redemption confirmation into `AfterCalculation`,
  the usage-limit check was still check-then-act: two concurrent `GenerateInvoice`
  runs for DIFFERENT contracts could both read "under limit" in
  `CalculateDiscount`, both apply the discount, and both insert distinct
  redemption keys — over-redeeming a `usageLimit`-capped promo (101 redemptions of
  a 100-use coupon), with the same TOCTOU for `perAccountUsageLimit` across two
  contracts/periods of one account.
  - `CalculateDiscount`'s usage-limit checks are now explicitly **advisory** (a
    best-effort read). The authoritative gate is `SaveRedemption`, which enforces
    the limits **atomically together with the insert**: (a) an existing
    same-idempotency-key row is an idempotent success; (b) otherwise, if inserting
    would exceed a limit, it rejects with the new sentinel
    `coupon.ErrUsageLimitReached` and inserts nothing; (c) otherwise it inserts.
  - `CouponPlugin.AfterCalculation` passes the coupon's limits to
    `SaveRedemption` and, on `ErrUsageLimitReached`, returns a descriptive error
    (wrapping the sentinel) so the billing transaction rolls back — the invoice is
    never persisted with a discount whose use could not be recorded. A retry
    recalculates: `CalculateDiscount` now counts the winner's committed redemption
    and skips the exhausted coupon, converging on a discount-free invoice.
  - Race-tested (`-race`): N concurrent confirmations of DISTINCT keys against a
    limit L < N let exactly L through; same-key concurrency still collapses to
    one; a per-account-limit variant; plus an end-to-end concurrent
    `GenerateInvoice` test (limit 1, two contracts → one discounted invoice, the
    other fails-and-retries to a clean discount-free invoice).
  - **BREAKING (pre-v1.0)**: `coupon.CouponRepository.SaveRedemption` gained a
    `limits coupon.RedemptionLimits` parameter and MUST now enforce it atomically
    with the insert (see the interface doc and `docs/internals/plugin-system.md`
    §6.3). New exported symbols: `coupon.RedemptionLimits` and
    `coupon.ErrUsageLimitReached`. Real databases implement the gate with a UNIQUE
    index on the idempotency key plus a serialized conditional insert (advisory
    lock / serializable tx / `ON CONFLICT DO NOTHING` + counted re-check).

- **Coupon redemption is no longer persisted outside the billing transaction
  (#185)** — `coupon.CouponPlugin.CalculateDiscount` previously wrote a
  `Redemption` and incremented a usage counter as side effects of the discount
  *calculation* hook, which the billing pipeline fires BEFORE its `tx.Run`. A
  pipeline that rolled back after that hook (e.g. a failed invoice save, or an
  in-transaction duplicate re-check) permanently burned a single-use coupon, and
  a retry or `RegenerateInvoice` for the same period double-redeemed it
  (exhausting `usageLimit`-capped promos and denying `perAccountUsageLimit=1`
  coupons on the retry that actually succeeded).
  - `CalculateDiscount` now has **no persistence side effects**: it only computes
    the discount and records it on the `CalculationContext`.
  - `CouponPlugin` now also implements `plugin.InvoiceLifecycleHook`; the
    redemption is confirmed in `AfterCalculation`, which runs inside the billing
    transaction after the invoice is created. Redemptions are **idempotent**,
    keyed by `(couponID, contractID, billingPeriod)` (`Redemption.IdempotencyKey()`),
    so a rolled-back pipeline's redemption is reused by the retry instead of
    burning an extra use, and a retry / regeneration for the same period consumes
    exactly one use.
  - Usage limits are now reconciled from redemption rows (there is no separate
    usage counter). Both the global and per-account checks exclude the in-flight
    `(contract, period)` so a retry is never denied by its own rolled-back
    redemption.
  - **BREAKING (pre-v1.0)**: `coupon.CouponRepository` changed. Removed
    `RecordUsage` and `FindUsageByAccount`; `SaveRedemption` must now be
    idempotent on `Redemption.IdempotencyKey()`; `NewRedemption` takes the
    billing period and invoice ID. Redemption records gained `BillingPeriod()`,
    `InvoiceID()`, and `IdempotencyKey()`.
  - `plugin.CalculationContext` gained `BillingPeriod()` / `SetBillingPeriod()`
    (additive); the billing pipeline sets it before any calculation hook runs.
  - **Upgrade note — existing redemption data double-counts without a one-time
    migration.** `Coupon.usedCount` is now strictly a **migration baseline**: it
    covers only uses that predate redemption rows and is **never incremented by
    the plugin**. The global-limit check is `usedCount + count(redemption rows)`.
    The OLD code wrote BOTH a redemption row (`SaveRedemption`) AND incremented
    the usage counter (`RecordUsage`) for every use, so on pre-existing data each
    historical use is counted twice and coupons hit their global limit early —
    e.g. a 100-use promo with 40 historical uses blocks after only 20 new uses
    (40 baseline + 40 rows + 20 new = 100). Integrators upgrading with existing
    redemption data must do ONE of the following before deploying:
    - reset each coupon's `usedCount` baseline to exclude uses that already have
      a redemption row (typically `usedCount -= count(redemption rows)`, i.e. 0
      when every historical use produced a row), or
    - delete — or exclude from `FindRedemptions` results — the historical
      redemption rows that are already reflected in `usedCount`.
    Fresh deployments (no pre-existing redemption data) need no action:
    `usedCount` starts at 0 and stays there.

### Added

- **Projection checkpointing (#192)** — new `projection.CheckpointStore` port
  (`Load(ctx, projectionName) (int64, error)` / `Save(ctx, projectionName, position) error`)
  with an in-memory reference implementation `inmemory.InMemoryCheckpointStore`.
  `ProjectionOptions` gains `CheckpointStore` and `ProjectionName`: when a
  checkpoint store is set, `ProjectionService.Start` loads the last processed
  global position on entry, subscribes from there, and saves after each
  successfully-processed event. This closes the "events appended while the
  projector was down are never delivered" gap: a restart resumes exactly where
  it left off. The checkpoint is never advanced past a failed event.
- **Loud signal for the silent no-transaction default (#187)** — write-side
  services (`BillingService`, `PaymentService`, `CreditNoteService`) and batch
  processors (`ContractRenewalProcessor`, `TrialExpirationProcessor`,
  `BalanceExpirationProcessor`) now emit a **`Warn`-level log once at
  construction** when they fall back to the default `NoopTxManager`, which runs
  multi-write flows without atomicity. Wiring the manager (`WithBillingTxManager`
  etc.) or opting into no-transactions explicitly suppresses it.
  - New `tx` helpers: `tx.NewNoopTxManagerExplicit(repos)` (deliberate opt-in
    that does not warn), `tx.IsNoop`, `tx.IsExplicitNoop`, and
    `tx.WarnIfDefaultNoop(logger, txm, component, remedy)`.
  - New service options `service.WithoutTransactions()`,
    `service.WithoutPaymentTransactions()`, and
    `service.WithoutCreditNoteTransactions()` for intentional in-memory/test/demo
    use without triggering the warning.
  - `docs/guides/integration.md` gains a "Transaction Manager (REQUIRED for
    production)" section enumerating the concrete corruption shapes (credits
    consumed with no invoice in the billing pipeline; void-without-replacement in
    `ReissueInvoice`).
- **Currency minor-unit rounding across the billing pipeline (#189)**:
  - `shared.Currency.MinorUnitExponent()` returns a currency's minor-unit decimal
    count (JPY=0, USD/EUR=2). Backed by an extensible registry:
    `shared.RegisterCurrencyMinorUnit(currency, exponent)` adds/overrides entries,
    and unregistered currencies fall back to `shared.DefaultMinorUnitExponent` (2).
  - `shared.Money.RoundToMinorUnit(mode)` quantises an amount to its currency's
    minor unit; `shared.Money.IsIntegralMinorUnit()` reports whether it already is.
    Both reuse the existing `shared.RoundingMode`.
  - `shared.Money.Int64Checked() (int64, error)` truncates toward zero and reports
    an error on int64 overflow instead of silently wrapping.
  - `BillingConfig.TaxRoundingMode` (option `WithTaxRoundingMode`) selects the
    pipeline's minor-unit rounding mode; default `shared.RoundDown`.
- **Plugin hook panic isolation (#193)** — every plugin hook the core fires is
  now invoked through the new `plugin.SafeInvoke` / `plugin.SafeInvokeMoney`
  helpers, which `recover()` a panicking plugin, capture its stack
  (`runtime/debug.Stack`), and convert it to a structured
  `*plugin.PluginPanicError` (plugin name + hook type + recovered value +
  stack). A recovered panic is now handled with the **same fatality policy as a
  returned error**: veto-capable hooks (`BeforeCalculation`, `DiscountHook`,
  `TaxHook`, `BeforeCharge`) abort the operation cleanly; `AfterCalculation`
  (which runs inside the billing transaction) converts to an error so the tx
  aborts cleanly instead of the panic unwinding through `tx.Run`; non-fatal
  hooks (`AfterCharge`, `OnInvoiceIssued`, `OnPaymentProcessed`,
  `OnPaymentFailed`, `OnRefund`, `OnCreditNoteIssued`, `OnInvoiceRevised`,
  `OnContractRenew`, `OnContractTrialEnd`, `OnContractChange`) are logged at
  Error level with the stack (via `plugin.LogNonFatalHookError`) and skipped so
  the flow — and the remaining hooks — continue. `Registry.InitializeAll` /
  `ShutdownAll` likewise turn a panicking `Initialize` / `Shutdown` into an
  error rather than an unrecoverable crash. This closes the gap where a single
  bad plugin could corrupt in-flight billing/payment state (e.g. a panic in
  `AfterCharge` after a successful gateway charge leaving a charged-but-
  unrecorded payment). See `docs/internals/plugin-system.md` §5.4. New public
  API: `plugin.SafeInvoke`, `plugin.SafeInvokeMoney`, `plugin.AsPanic`,
  `plugin.LogNonFatalHookError`, `plugin.PluginPanicError`.
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
  off by default so legitimate old redeliveries always flow. On trigger, the
  drop leaves an operator trail instead of causing pointless gateway redelivery:
  with a DLQ configured the event is sent to the DLQ with a distinct reason
  (`LastError` explains the over-age drop, `RetryCount` 0 because the handler
  never ran), a warning is logged, and the delivery is acknowledged (nil) so the
  gateway stops redelivering; without a DLQ a typed `*port.WebhookError` with the
  new code `port.WebhookErrorCodeEventTooOld` is returned (gateway redelivery is
  then the only recovery channel). A DLQ send failure returns an error so the
  gateway retries and a later attempt can record the drop.

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

- **Projection delivery is now lossless end-to-end (#192)** — several reliability
  gaps in projection delivery were fixed:
  - `inmemory.InMemoryEventStore.Subscribe` now **honours `fromPosition`**:
    it replays stored events with `GlobalPosition > fromPosition` (backfill) then
    switches to the live tail with **no gap and no duplicate** at the handover
    (backfill snapshot + subscriber registration happen atomically under the
    store lock, plus a monotonic position guard). Previously `fromPosition` was
    ignored (live-only feed).
  - Slow subscribers **no longer lose events**: `Append` buffers into an
    unbounded per-subscriber queue and a pump goroutine delivers with a blocking
    hand-off (escaped by context cancellation) instead of silently dropping when
    a 100-slot channel filled. Delivery is now at-least-once; `Projector.Project`
    is documented as requiring idempotency.
  - `Subscribe` now honours context cancellation: it unregisters the subscriber
    and closes the channel, so there is no goroutine/channel leak.
  - `ProjectionService.Start` async-mode failures no longer advance the
    checkpoint past a failed event (checkpoint is frozen for the rest of the run
    so a restart redelivers it).
  - **BREAKING (pre-v1.0) — `ProjectionOptions.MaxRetries` semantics**: it now
    means the number of RETRIES *in addition to* the initial attempt (total
    attempts = `MaxRetries+1`). Previously it was mis-implemented as a total
    attempt count, so `MaxRetries=1` performed zero retries while the error
    message claimed "failed after 1 retries". `MaxRetries=0` (single attempt, no
    retry) is unchanged; `MaxRetries=N>0` now performs one more attempt than
    before. The error message now reads "failed after N attempts (M retries)".
  - The `eventstore.Store.Subscribe` interface signature is **unchanged**
    (backward compatible); only its documented contract and the in-memory
    implementation's behaviour changed.
- **BEHAVIORAL CHANGE — invoice amounts are now rounded to the currency's minor
  unit (#189).** Previously the billing pipeline and tax plugin produced exact
  rational amounts, so e.g. ¥101 at 10% tax persisted a `¥10.1` tax and a
  `¥111.1` total that an integer-only gateway could never settle, causing a
  perpetual 0.1 residue and reconciliation drift. `BillingService`'s pipeline now
  quantises the subtotal, total discount, and total tax to the invoice currency's
  minor unit (via `BillingConfig.TaxRoundingMode`, default `shared.RoundDown`), so
  the persisted subtotal/discount/tax/total/amountDue are all integral in minor
  units. Consumers who previously relied on fractional invoice amounts will see
  rounded figures; set `WithTaxRoundingMode(shared.RoundHalfUp)` (or `RoundUp`) if
  a jurisdiction requires it.

### Fixed

- **`shared.Money.Int64()` floored negative amounts (#189).** It documented
  "truncating any fractional part" but used floor division (`big.Int.Div`), so
  `Int64(-1.5)` returned `-2` instead of `-1`, overstating negative fractional
  amounts by one minor unit. It now truncates toward zero as documented. Overflow
  behavior is documented (low-order bits); use the new `Int64Checked()` when the
  amount's range is not guaranteed.

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

### Fixed

- **Payment optimistic locking (#190)** — `payment.Payment` now carries a
  `version` / `loadedVersion` optimistic-locking pair, mirroring `invoice.Invoice`
  and `invoice.CreditNote`. Every state transition that changes persisted state
  (`Complete`, `Fail`, `MarkRefunded`, `MarkPartiallyRefunded`, `MarkChargedBack`,
  `RecordRefund`) bumps the version. Previously `Payment` had none, so two
  operators who each loaded a completed payment and called `RecordRefund` could
  both save last-writer-wins — booking one refund while the gateway moved money
  twice — and a concurrent `Pending→Completed` (3DS) vs `Pending→Failed` (webhook)
  pair silently lost one transition.
  - **BREAKING (adapter implementors)**: `payment.Repository.Save` now documents an
    optimistic-locking concurrency contract. Implementations MUST reject a stale
    same-ID write by returning an error that `tx.IsVersionConflict` recognizes
    (the `tx.ErrVersionConflict` sentinel or a `*shared.DomainError` with code
    `shared.ErrCodeVersionConflict`) and persist `Version()` on success — or
    serialize the read (row lock / `SELECT ... FOR UPDATE` / `SERIALIZABLE`). An
    unconditional last-writer-wins upsert reintroduces the double-refund window.
    The first save of a given ID (fresh payment, `LoadedVersion` 0) is unaffected,
    so this is source-compatible for existing callers.
  - `PaymentSnapshot` gained a `Version` field; `ToSnapshot` / `FromSnapshot`
    round-trip it (with `FromSnapshot` restoring both `version` and
    `loadedVersion` from the single field).
  - `PaymentService.Refund` now wraps its local bookkeeping transaction in
    `tx.RetryOnConflict`, mirroring `CreditNoteService.RefundCreditNote`: a version
    conflict retries against the winner's freshly-persisted state, where
    `RecordRefund` re-validates and surfaces a clean over-refund domain error
    instead of a spurious reconciliation alert. `ProcessPayment`'s existing
    idempotency-key race machinery is unchanged.
  - The reference `infrastructure/inmemory` payment repository now enforces the
    version check.

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

### Fixed

- **`tx.RetryOnConflict` no longer silently succeeds without running `fn` (#187)**
  — a `maxAttempts <= 0` previously returned `nil` without ever invoking the
  closure, reporting success while doing no work. It is now clamped to `1`, so
  `fn` always runs at least once and its result (or error) is surfaced.

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

- **`ContractSuspendedEvent` dropped `ExtendContract`/`SuspendedAt` through the
  event round-trip (#194)**. `Apply(ContractSuspendedEvent)` reconstructed the
  `SuspensionConfiguration` from `BillingBehavior`/`ResumeDate`/`Reason` only, so a
  suspension configured with `ExtendContract: true` replayed (and snapshotted from
  replayed state) as `ExtendContract=false` with a zero `SuspendedAt`.
  - `ContractSuspendedEvent` now carries `ExtendContract` (and its already-present
    `SuspendedAt` is now restored into the config); `Apply` reconstructs the full
    configuration, so live mutation and replay agree.
  - Event schema bumped to **SchemaVersion 2** with `ContractSuspendedEventUpcaster`
    for legacy v1 payloads: `extend_contract` defaults to `false` (pre-#194
    suspensions never extended the period), and `suspended_at` falls back to the
    event's `OccurredAt` when absent or zero-valued (defensive — a zero anchor would
    corrupt the resume-time extension math). `CanUpcast` matches only the exact
    `fromVersion == 1` so the fixpoint chain stays order-independent.
  - `Resume` now honors `ExtendContract`: it extends `currentPeriod.End` by the
    suspension duration (resume time − `SuspendedAt`) inside
    `Apply(ContractResumedEvent)`. The extension is reconstructed deterministically
    from event data plus the still-present suspension config, so `ContractResumedEvent`
    needs no new field and no snapshot-schema bump (the existing
    `SuspensionConfiguration` already persists `SuspendedAt`/`ExtendContract`).
- **Negative / inverted invoice amounts are now rejected at the boundary (#188)**.
  The billing pipeline accepted negative discounts and taxes from plugins, and
  `invoice.NewInvoice` accepted a negative subtotal/discount/tax or a discount
  exceeding the subtotal — any of which produced a negative or over-billed total.
  A subtotal ¥100 with a ¥200 discount yielded `Total() = -100`, after which
  `ValidatePayment` rejected *every* payment ("would exceed amount due -100"),
  leaving a permanently unsettleable invoice.
  - `BillingService.executeBillingPipeline` now validates each `DiscountHook` /
    `TaxHook` return value inside the hook loop: a negative amount aborts the
    pipeline with a `shared.DomainError` (`business_rule_violation`) that names
    the offending plugin and the amount; a foreign-currency return is attributed
    to the plugin as a `currency_mismatch` error.
  - `invoice.NewInvoice` now enforces its monetary invariants: `subtotal`,
    `discountAmount`, `taxAmount` must be non-negative and `discountAmount ≤
    subtotal` (`validation_error`), in addition to the existing currency-parity
    checks. The constructor already returned `(*Invoice, error)`, so this is a
    behavioral tightening, **not** a signature change. Validation lives on the
    constructor only — `InvoiceFromSnapshot` is unchanged, so snapshots of
    legacy invoices persisted under looser rules still load (replay-safe).
  - `WithAppliedBalance` now rejects a negative applied balance or one greater
    than the total; `WithAmountDue` now rejects a negative amount due, one
    greater than the total, or a currency mismatch — all via the existing
    deferred-`optErr` option-validation path surfaced by `NewInvoice`.
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
