---
sidebar_position: 6
---

# Postgres Payment Repository — Reference Implementation

This guide shows how to implement `payment.Repository` against Postgres in a
way that satisfies the library's concurrency contract (issue #97). It
complements the generic [Integration Guide](./integration.md) with the
specific error-translation pattern that `PaymentService.ProcessPayment`
relies on to handle concurrent-success races without refunding legitimate
gateway charges.

> The [adapters](https://github.com/contract-to-cash/adapters) repository already ships
> production `payment.Repository` implementations (`postgres/` and `mysql/`) that follow
> this pattern — use them directly, or read on to build your own.

## Why this matters

`PaymentService.ProcessPayment` may be invoked concurrently with the same
`IdempotencyKey` — for example, when a hasty user clicks "Pay" twice, when
an upstream retry fires in parallel with the original request, or during
a multi-region active/active deployment. Without storage-level
serialization, two goroutines can both observe "no existing payment",
both persist a fresh record, and leave the invoice double-paid.

The library's application layer (`FindByIdempotencyKey` → `Save` inside
`RunInTx`) cannot serialize on its own: the check and the write are two
statements. The guarantee **must** come from the repository's storage
backend — typically a `UNIQUE INDEX` on `idempotency_key`.

When that index fires, `Save` must surface the collision as a
`*payment.DuplicateIdempotencyKeyError` (or any error that satisfies
`errors.Is(err, payment.ErrDuplicateIdempotencyKey)`). `PaymentService`
catches the sentinel, reads the winning record, and returns it to the
race-losing goroutine as an idempotent replay — **no saga compensation,
no refund of the winner's charge**.

A repository that returns raw driver errors (e.g. `pgconn.PgError{Code:
"23505"}` without translation) will route the race loser through
`saga.Compensate()`, issuing a `Refund` against the shared gateway
transaction and undoing the winner's legitimate payment. This is a silent
regression; the code compiles cleanly.

## Postgres aborted-transaction handling

A note specific to Postgres-backed adapters: when the partial UNIQUE INDEX
fires inside `RunInTx`, Postgres aborts the surrounding transaction
(`in_failed_sql_transaction`, SQLSTATE `25P02`). Every subsequent query on
the same connection — including a "look up the winner" `SELECT` — fails
until the tx is rolled back.

`PaymentService.ProcessPayment` knows about this and is structured so
adapters do **not** need to defend with `SAVEPOINT`s:

1. The `RunInTx` closure returns an internal sentinel
   (`errDuplicateKeyRaceSignal`) the moment your `Save` returns
   `payment.ErrDuplicateIdempotencyKey`.
2. `RunInTx` rolls the failed transaction back. The connection leaves
   `in_failed_sql_transaction` and is safe to use again.
3. `PaymentService` then re-reads the winner via the OUTER context — a
   fresh transaction — using `s.paymentRepo.FindByIdempotencyKey(ctx, key)`.

What this means for your adapter implementation:

- **Required**: translate the unique-violation to the sentinel and
  return it as quickly as possible from `Save`. Do not perform any
  follow-up queries on the failed tx.
- **Not required**: `SAVEPOINT save_payment` / `RELEASE` / `ROLLBACK TO
  SAVEPOINT` around the conflicting `INSERT`. Adding a SAVEPOINT is
  harmless but does not change correctness.
- **Not required**: a separate "fetch the winner" query inside `Save`.
  `PaymentService` performs the convergence read on a fresh tx after
  `RunInTx` returns.

## Schema

```sql
CREATE TABLE payments (
    id                    TEXT PRIMARY KEY,
    invoice_id            TEXT NOT NULL,
    account_id            TEXT NOT NULL,
    amount                NUMERIC NOT NULL,
    currency              TEXT NOT NULL,
    payment_method        TEXT NOT NULL,
    gateway_transaction_id TEXT NOT NULL,
    status                TEXT NOT NULL,
    idempotency_key       TEXT,
    paid_at               TIMESTAMPTZ NOT NULL,
    refunded_amount       NUMERIC NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Critical: partial unique index on non-empty idempotency_key.
-- Using a partial index lets legacy payments (empty key) coexist
-- and matches the application-layer contract that empty keys are
-- not uniqueness-enforced.
CREATE UNIQUE INDEX idx_payments_idempotency_key
  ON payments (idempotency_key)
  WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX idx_payments_invoice_id ON payments (invoice_id);
```

## `Save` implementation (`pgx/v5`)

```go
package postgrespayment

import (
    "context"
    "errors"
    "fmt"

    "github.com/contract-to-cash/core/domain/payment"
    "github.com/contract-to-cash/core/domain/shared"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgconn"
    "github.com/jackc/pgx/v5/pgxpool"
)

// idempotencyKeyUniqueConstraint is the name of the UNIQUE INDEX
// declared in the schema above. Matching by constraint name (not just
// error code 23505) is essential: other unique indexes on payments
// (e.g. on gateway_transaction_id) must NOT be translated to the
// idempotency-key sentinel.
const idempotencyKeyUniqueConstraint = "idx_payments_idempotency_key"

type Repo struct {
    pool *pgxpool.Pool
}

const upsertSQL = `
INSERT INTO payments (
    id, invoice_id, account_id, amount, currency, payment_method,
    gateway_transaction_id, status, idempotency_key, paid_at,
    refunded_amount, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), $10, $11, NOW(), NOW()
)
ON CONFLICT (id) DO UPDATE SET
    status                = EXCLUDED.status,
    refunded_amount       = EXCLUDED.refunded_amount,
    gateway_transaction_id = EXCLUDED.gateway_transaction_id,
    paid_at               = EXCLUDED.paid_at,
    updated_at            = NOW()
`

// Save persists a payment, enforcing the idempotency-key uniqueness
// contract required by payment.Repository.
//
// The ON CONFLICT (id) clause handles the same-ID re-save path (e.g.
// the 3DS Pending → Completed upgrade) — it silently updates the
// existing row. A different-ID collision on idempotency_key is caught
// by the partial unique index and translated to the sentinel error.
func (r *Repo) Save(ctx context.Context, p *payment.Payment) error {
    _, err := r.execOnTx(ctx).Exec(ctx, upsertSQL,
        p.ID(),
        p.InvoiceID(),
        p.AccountID(),
        p.Amount().Amount().FloatString(10),
        string(p.Amount().Currency()),
        string(p.Method()),
        p.GatewayTransactionID(),
        string(p.Status()),
        p.IdempotencyKey(),
        p.PaidAt(),
        p.RefundedAmount().Amount().FloatString(10),
    )
    if err == nil {
        return nil
    }

    // Translate the Postgres unique-violation into the sentinel the
    // application layer expects. Match on the constraint NAME — a
    // plain 23505 check would also fire for e.g. the primary-key
    // conflict, which ON CONFLICT (id) already handles and should
    // never reach this branch, but defensive matching prevents
    // future indexes from being misinterpreted.
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) &&
        pgErr.Code == "23505" &&
        pgErr.ConstraintName == idempotencyKeyUniqueConstraint {
        return &payment.DuplicateIdempotencyKeyError{
            Key:         p.IdempotencyKey(),
            AttemptedID: p.ID(),
            // ExistingID is optional. Leave zero — once the INSERT
            // fails, the surrounding tx is in
            // in_failed_sql_transaction state and any follow-up query
            // on the same connection errors with 25P02. Do NOT try to
            // fill ExistingID via a SELECT here; PaymentService reads
            // the winner via the outer ctx after RunInTx rolls this
            // tx back, which is the correct place to surface the
            // winner's PaymentID. If you need the ExistingID for
            // diagnostics, expose it through your application logs
            // from the convergence path, not from Save.
        }
    }
    return fmt.Errorf("save payment %s: %w", p.ID(), err)
}

// execOnTx returns the pgx.Tx bound into ctx by TxManager if present,
// falling back to the pool. See your TxManager implementation for
// the context-key convention.
func (r *Repo) execOnTx(ctx context.Context) interface {
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
} {
    if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
        return tx
    }
    return r.pool
}

type txKey struct{}
```

## `FindByIdempotencyKey` implementation

The read-side is unchanged by the #97 work, but correctness still matters:
the value must be `nil, nil` when the key is absent (NOT an error) so
`PaymentService`'s in-tx existence check can distinguish "no record" from
"DB unreachable".

```go
const findByKeySQL = `
SELECT id, invoice_id, account_id, amount, currency, payment_method,
       gateway_transaction_id, status, idempotency_key, paid_at,
       refunded_amount
FROM payments
WHERE idempotency_key = $1
LIMIT 1
`

func (r *Repo) FindByIdempotencyKey(ctx context.Context, key string) (*payment.Payment, error) {
    if key == "" {
        return nil, nil
    }
    row := r.execOnTx(ctx).QueryRow(ctx, findByKeySQL, key)
    p, err := scanPayment(row)
    if errors.Is(err, pgx.ErrNoRows) {
        return nil, nil
    }
    if err != nil {
        return nil, fmt.Errorf("find by idempotency key %q: %w", key, err)
    }
    return p, nil
}
```

## DynamoDB equivalent (sketch)

For DynamoDB-backed adapters, enforce the constraint via a conditional
`PutItem`:

```go
_, err := r.client.PutItem(ctx, &dynamodb.PutItemInput{
    TableName: aws.String("payments"),
    Item:      /* marshaled payment */,
    ConditionExpression: aws.String(
        "attribute_not_exists(idempotency_key) OR id = :id",
    ),
    ExpressionAttributeValues: map[string]types.AttributeValue{
        ":id": &types.AttributeValueMemberS{Value: string(p.ID())},
    },
})

var ccf *types.ConditionalCheckFailedException
if errors.As(err, &ccf) {
    return &payment.DuplicateIdempotencyKeyError{
        Key:         p.IdempotencyKey(),
        AttemptedID: p.ID(),
    }
}
```

Note: DynamoDB enforces item-level conditions but has no cross-item unique
index. Deployments that require global uniqueness on `idempotency_key`
typically add a sibling table keyed by `idempotency_key` and perform a
transactional `TransactWriteItems` across the two tables.

## MySQL equivalent (sketch)

```go
var mysqlErr *mysql.MySQLError
if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 /* ER_DUP_ENTRY */ {
    // Inspect mysqlErr.Message to disambiguate which unique key fired.
    if strings.Contains(mysqlErr.Message, "idx_payments_idempotency_key") {
        return &payment.DuplicateIdempotencyKeyError{
            Key:         p.IdempotencyKey(),
            AttemptedID: p.ID(),
        }
    }
}
```

## Testing your adapter

1. **Unit test** `Save` returns `errors.Is(err, payment.ErrDuplicateIdempotencyKey)`
   when the same non-empty key is saved under two different IDs.
2. **Unit test** `Save` allows same-ID re-saves (update path).
3. **Unit test** `Save` does NOT translate unrelated unique-constraint
   violations (if you add other unique indexes in the future).
4. **Aborted-tx test (Postgres-specific)**: inside a single `RunInTx`
   closure, intentionally provoke a duplicate-key `Save`, then verify
   that subsequent queries on the same `txCtx` would fail (25P02).
   `PaymentService` works around this by exiting the closure
   immediately on the sentinel — your adapter only has to make sure
   `Save` returns the sentinel without first issuing any follow-up
   queries that would also fail. The library covers this contract end-
   to-end with `TestPaymentIdempotency_ConcurrentSuccess_AbortedTxSimulation_Integration`.
5. **Concurrency test**: spin up two goroutines that both call
   `PaymentService.ProcessPayment` with the same `IdempotencyKey`.
   Assert:
   - Both return the same `*Payment` (same ID).
   - Exactly one row exists in `payments` for that key.
   - The invoice is paid exactly once at the full amount.
   - No `Refund` was issued at the gateway.

The in-memory test wrapper `isolatingInvoiceRepo` in
`tests/integration/payment_idempotency_test.go` may be useful as a
reference for "what per-tx snapshot isolation looks like from the
application's perspective".

## Related

- `domain/payment/errors.go` — sentinel and typed error definitions.
- `application/service/payment_service.go` — `ProcessPayment`'s
  concurrency contract and the in-tx convergence path.
- `docs/internals/payment-gateway.md` §6.1.8 — companion issue #87
  compensation-marker flow.
- Issue [#97](https://github.com/contract-to-cash/core/issues/97) — the
  original report and discussion.
- [github.com/contract-to-cash/adapters](https://github.com/contract-to-cash/adapters) —
  production Postgres/MySQL implementations of this repository contract.
