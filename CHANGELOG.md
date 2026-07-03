# Changelog

All notable changes to `github.com/contract-to-cash/core` are documented in this
file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Release workflow (`.github/workflows/release.yml`): tags and publishes a
  GitHub Release when a versioned `## [x.y.z]` section lands in `CHANGELOG.md`
  on `main`, or on manual `workflow_dispatch` with an explicit tag input.
  Runs build + test + lint before tagging.

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

---

## Prior history

This project did not maintain a `CHANGELOG.md` before this entry. Refer to
the git log for the full commit history and the PR/issue tracker for
contextualized release notes.
