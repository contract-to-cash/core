---
sidebar_position: 7
---

# Data Protection & Privacy

This guide explains how `github.com/contract-to-cash/core` handles personal data and
what responsibilities fall to you as the integrator. It is guidance, not legal advice —
your data-protection obligations (GDPR, Japan's APPI, CCPA, etc.) depend on your
jurisdiction and use case.

The short version: **this library is designed so that personal data never has to enter
the event store.** The core identifies parties by opaque IDs and leaves personal data
in systems you own.

## What personal data flows through the library

The domain works almost entirely in **opaque identifiers and money**, not personal
data:

- `AccountID`, `ContractID`, `InvoiceID`, `PaymentID`, `PriceID`, `ProductID`,
  `BalanceEntryID` — ULIDs / opaque strings with no personal data encoded in them.
- `shared.Money` amounts, currencies, dates, statuses.
- Payment idempotency keys and gateway transaction IDs (integrator-supplied /
  gateway-supplied references).

An **`AccountID` is an opaque reference to a customer**, not the customer's data. The
library never asks for a name, email, address, or payment instrument — those live in
the gateway and in your own customer store.

**Where PII could leak in if you are not careful:**

- Free-form `metadata map[string]interface{}` fields on entities (e.g. payment
  metadata). Do not put names, emails, or card data here.
- Consumer-supplied idempotency keys, if you derive them from personal data. Derive
  them from opaque IDs instead.
- Coupon codes or invoice line-item descriptions, if you populate them with personal
  data.

**Recommendation:** keep direct PII (names, emails, addresses, tax IDs, payment
instruments) **out of events, metadata, and line items**. Store it in a consumer-owned
system keyed by `AccountID`, and join at the presentation layer.

## Erasure vs. an append-only event store

Event sourcing is **append-only**: events are immutable, and contract state is
reconstructed by replaying them. This is what gives you a full audit trail — but it is
in direct tension with a "right to erasure" request that expects data to be deleted.

There are two standard patterns. This library is built for the first and does not
implement the second.

### Pattern 1 (primary recommendation): keep PII outside events

If personal data never enters the event store, an erasure request is satisfied by
deleting the record in your **consumer-owned customer store** keyed by `AccountID`. The
event history then contains only an opaque ID that no longer resolves to a person —
effectively anonymized — while your billing audit trail stays intact.

This is the intended design of the library (opaque `AccountID` references), and it is
the pattern we recommend building around from day one.

### Pattern 2 (fallback): crypto-shredding

If you have already persisted PII inside events (or cannot avoid it), the standard
fallback is **crypto-shredding**: encrypt per-subject PII with a per-subject key before
it is written into an event, and erase by destroying that key. Once the key is gone the
ciphertext is unrecoverable, which satisfies erasure without rewriting the append-only
log.

> **The library does not implement crypto-shredding.** There is no built-in
> encryption, key registry, or key-destruction hook. If you adopt this pattern you own
> key generation, storage, rotation, and destruction entirely — encrypt before handing
> data to the library and decrypt after reading it back. Treat this as a fallback for
> data that should not have been in events in the first place, not as a substitute for
> Pattern 1.

## Personal data in logs

The library logs through the standard-library `log/slog`. Services accept a logger
(e.g. `WithBillingLogger`) and default to `slog.Default()`.

**What the library actually logs** (audited from the service, batch, and webhook code):

- **Entity IDs** — `contractID`, `invoiceID`, `paymentID`, `accountID`,
  `balanceEntryID`, `event_id`, hook names.
- **Monetary amounts** — e.g. `refundAmount`, and `forfeitedAmount` + `currency` when a
  balance entry expires (`batch/balance_expiration.go`).
- **Idempotency keys** — `originalKey` / `effectiveKey` are logged in a few payment
  reconciliation paths (`application/service/payment_service.go`).
- **Errors** from gateway calls, hook failures, and save conflicts.

So logs contain **account IDs and monetary amounts** (and, in the payment paths,
idempotency keys). They do **not** contain names, emails, addresses, or card data —
because the library never receives those. The library also does not log at `Debug`
level by default; the messages above are `Info` / `Warn` / `Error`.

Even opaque IDs plus amounts can be sensitive in aggregate (they reveal that an account
was billed a given amount at a given time). If that matters for your jurisdiction:

- **Redact via your slog handler.** Route the library's logger through a custom
  `slog.Handler` that drops or hashes attributes such as `accountID`, `refundAmount`,
  `originalKey`, and `effectiveKey` before they reach your log sink. Because the library
  emits structured key/value attributes, an attribute-filtering handler can redact them
  reliably without parsing message strings.
- **Control verbosity.** Pass a logger with a higher minimum level if you want to
  suppress the `Info`-level reconciliation notes.

## Data retention responsibilities

This is a **BYO-DB** library: it defines interfaces and in-memory implementations only.
Every durable store is **consumer-owned**, and so is its retention, encryption-at-rest,
access control, and deletion policy:

- **Event store** — your append-only log (`eventstore.Store` implementation). Its
  retention drives how long history is reconstructable.
- **Snapshots** — periodic aggregate snapshots (`SnapshotService`,
  `DefaultSnapshotInterval`). These embed reconstructed state, so treat them with the
  same retention/erasure rules as events.
- **Webhook dead-letter queue (DLQ)** — failed webhook events you persist for later
  retry. May contain gateway payloads; apply retention and access controls.
- **Idempotency store** — payment idempotency keys and webhook dedup markers
  (`IsDuplicate` / `MarkProcessed`, default `DeduplicationTTL` 72h). Use TTLs long
  enough to cover gateway redelivery windows but no longer than you need.

The library holds none of this in memory beyond a single operation (except the
in-memory implementations, which are for tests and demos only). Backup, replication,
residency, and deletion are entirely under your control.

## Related documents

- [Security Policy](https://github.com/contract-to-cash/core/blob/main/SECURITY.md) —
  reporting channel and integrator hardening checklist.
- [Payment Gateway](../internals/payment-gateway.md) — webhook signature verification,
  idempotency, DLQ.
- [Integration Guide](./integration.md) — wiring stores and services.
