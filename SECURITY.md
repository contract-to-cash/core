# Security Policy

`github.com/contract-to-cash/core` is an event-sourced billing library. Because it
handles contracts, invoices, and payments, we take security reports seriously and aim
to respond quickly.

## Supported Versions

This project is **pre-v1.0**. Tagged releases exist and start at
[v0.2.0](https://github.com/contract-to-cash/core/releases), the first curated
release (a `v0.1.0` tag predates the curated changelog and carries no release
notes). During this phase:

- Security fixes land on **`main`** and ship in the **next release tag**; only the
  **latest release tag** (plus `main`) is supported.
- There are no backported patches to older release tags.
- Once v1.0 is tagged, this policy will be updated with a concrete supported-version
  window.

Pin the [latest release tag](https://github.com/contract-to-cash/core/releases) if
you need reproducible builds, and upgrade to each new release for security fixes.
Please report vulnerabilities against a supported version — the latest release tag
or `main`.

## Reporting a Vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately through GitHub's **private vulnerability reporting**:

1. Go to the repository's **Security** tab.
2. Click **Report a vulnerability** (this opens a private security advisory).
3. Include a description, affected package/path, reproduction steps, and impact
   assessment.

If private vulnerability reporting is not available to you, open a minimal public
issue that says only "security report — requesting private contact" (no details) and
a maintainer will open a private advisory.

### Response expectations

- **Acknowledgement:** within 3 business days.
- **Initial assessment:** within 7 business days (severity and whether it is in scope).
- **Fix / disclosure:** coordinated with the reporter. We will credit reporters who
  wish to be named once a fix lands on `main`.

These are targets, not contractual guarantees — this is an open-source project.

## Scope

**In scope** (report these):

- The core library packages (`domain/`, `application/`, `eventstore/`, `plugin/`,
  `batch/`, `infrastructure/inmemory/`).
- The official plugins (`plugins/coupon`, `plugins/tax`, `plugins/invoicecleanup`).
- Anything that lets an attacker bypass a documented invariant — e.g. money/rounding
  errors, event-replay corruption, idempotency bypass, or optimistic-lock bypass that
  is reproducible against library code with no consumer bug involved.

**Out of scope** (not vulnerabilities in this library):

- **Consumer integrations.** This is a BYO-DB / BYO-Gateway library. Your repository
  implementations, payment-gateway adapters, webhook endpoints, transaction manager,
  key management, and deployment are your responsibility.
- Missing hardening in the **in-memory** implementations under
  `infrastructure/inmemory/` — these are explicitly for tests and demos, not
  production.
- Vulnerabilities that require a malicious first-party plugin or malicious
  configuration (the plugin system runs in-process and trusts registered plugins).
- Denial of service from unbounded consumer-supplied input that the consumer is
  responsible for bounding (e.g. webhook payload size, event batch size).

## Hardening Checklist for Integrators

The library provides the domain and pipeline; production safety depends on how you
wire it up. At minimum:

- **Verify webhook signatures (mandatory).** Every `port.WebhookHandler`
  implementation must verify the gateway signature and reject on mismatch before the
  event is processed. `WebhookProcessor` calls `ParseAndVerify` first and returns
  `WebhookErrorCodeInvalidSignature` on failure — do not stub this out. See
  `docs/internals/payment-gateway.md` (WebhookHandler / `ParseAndVerify`).
- **Use idempotency stores with bounded TTLs.** Payment idempotency keys and the
  webhook dedup markers (`IsDuplicate` / `MarkProcessed`, default `DeduplicationTTL`
  72h) must be backed by durable storage with a TTL long enough to cover gateway
  redelivery windows. Too-short TTLs allow double-processing.
- **Run a real transaction manager in production.** The default `NoopTxManager` does
  not provide atomicity. Supply a real `tx.TxManager` (via
  `WithBillingTxManager` / the equivalent service option) so credit-ledger
  application, invoice save, and payment records commit atomically.
- **Keep direct PII out of events and metadata.** Account IDs are opaque references;
  store personal data in consumer-owned systems keyed by `AccountID`. See
  `docs/guides/data-protection.md`.
- **Configure your slog handler for redaction.** The library logs entity IDs and
  monetary amounts. Route logs through a handler that redacts or drops fields you
  consider sensitive. See `docs/guides/data-protection.md`.
- **Own your key management and retention.** Event store, snapshots, webhook DLQ, and
  idempotency store are consumer-owned; apply your own encryption-at-rest, access
  control, and retention policy.
