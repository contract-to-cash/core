package port

import (
	"context"

	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
)

// PaymentOutboxWriter is the integrator hook point for writing a durable
// notification atomically with a payment record (issue #248).
//
// # Why this exists (transactional outbox)
//
// The core-fired payment hooks (AfterCharge, OnPaymentProcessed) run AFTER the
// bookkeeping transaction commits. An integrator that enqueues a notification
// from those hooks cannot join that enqueue to the same transaction as the
// payment write, so a crash in the window between commit and enqueue silently
// drops events such as payment.charged / contract.first_payment (issue #248,
// platform#45). This port lets the core call the integrator INSIDE the payment
// bookkeeping transaction, immediately after the payment (and invoice) rows are
// saved and BEFORE the transaction commits, so the outbox row is written in the
// SAME transaction as the payment — the durable, at-least-once foundation of a
// transactional outbox.
//
// The core does not build the outbox row: the integrator owns its own webhook
// schema, event vocabulary, and any I/O needed to classify the event (e.g. a
// first-payment lookup). The core only guarantees WHEN OnPaymentRecorded is
// called (in-tx, post-save, pre-commit) and that its returned error rolls the
// transaction back.
//
// # Implementation contract — READ THIS
//
//   - The ctx argument is TRANSACTION-SCOPED. Take the current transaction off
//     it (e.g. a QuerierFromContext(ctx) helper wired by your repositories) and
//     perform a piggy-backed INSERT into your own outbox table using THAT
//     transaction. Using a separate connection or a separate transaction
//     silently breaks atomicity — the regression is quiet: it compiles, the
//     happy path passes, and only a crash between the two writes reveals the
//     lost event.
//   - Do LIGHTWEIGHT INSERTs ONLY. No external calls, heavy work, or real
//     webhook delivery here: holding the transaction open for network I/O
//     exhausts locks and connections. Actual delivery is done by a separate
//     relay/poller that reads the outbox table out of band.
//   - VETO SEMANTICS (IMPORTANT). Returning an error rolls back the calling
//     transaction. On the ProcessPayment path a SUCCESSFUL gateway charge is
//     then UNWOUND by saga compensation (Void, falling back to Refund). This
//     is the price of atomicity and is the exact same path as the existing
//     "payment save failed → charge reversed" behaviour. Because a transient
//     error triggers a real reversal, Append must be an idempotent, robust,
//     lightweight INSERT — never fail it for a recoverable reason. On the
//     SettlePayment path a veto involves NO gateway money movement (the funds
//     already arrived out-of-band); the rollback is harmless and the next
//     webhook redelivery retries — the same risk class as OnInvoiceFinalized
//     (see docs/internals/plugin-system.md §11.4/§11.5).
//   - AT-LEAST-ONCE / DEDUP. Idempotent-replay convergence and retries can call
//     this more than once for the same payment/invoice. Key your outbox row on
//     an idempotency discriminator (e.g. the payment ID) so duplicates collapse.
//   - With a NoopTxManager there is NO atomicity (the writes are not wrapped in
//     a transaction). Production deployments MUST wire a real TxManager via
//     service.WithPaymentTxManager; see the transactional-outbox section of
//     docs/internals/plugin-system.md.
type PaymentOutboxWriter interface {
	// OnPaymentRecorded is called by PaymentService.ProcessPayment and
	// PaymentService.SettlePayment inside the bookkeeping transaction,
	// immediately after the payment and invoice rows are saved and before the
	// transaction commits. It is invoked only on the paths that actually
	// persist a new payment state — the Pending→Completed promotion path and
	// the normal success path (including a zero-amount settlement) in
	// ProcessPayment, and the Pending→Completed settlement in SettlePayment —
	// and NOT on idempotent-replay / race-convergence paths that write nothing
	// new. A returned error rolls the transaction back (and, on the
	// ProcessPayment gateway path, triggers saga compensation of the charge;
	// a SettlePayment veto moves no money and is retried by the next webhook
	// redelivery).
	//
	// p is the persisted payment (Completed); inv is the invoice the payment
	// was recorded against. Neither is nil on this call.
	OnPaymentRecorded(ctx context.Context, p *payment.Payment, inv *invoice.Invoice) error
}

// InvoiceOutboxWriter is the integrator hook point for writing a durable
// notification atomically with an invoice finalization (issue #248).
//
// The core calls OnInvoiceFinalized from BillingService.FinalizeInvoice inside
// the finalize transaction, immediately after the finalized invoice row is
// saved and before the transaction commits, so the outbox row is written in the
// SAME transaction as the finalize.
//
// # Veto semantics and risk asymmetry
//
// A returned error rolls the finalize transaction back. Unlike
// PaymentOutboxWriter, FinalizeInvoice moves NO money, so a rollback is a
// harmless "re-finalize on the next attempt" rather than a charge reversal —
// the two ports are deliberately asymmetric in blast radius. FinalizeInvoice
// wraps its closure in RetryOnConflict; a writer error is not a version
// conflict, so it is not retried and propagates as-is. On a genuine
// optimistic-lock retry the whole closure re-runs and the writer runs again,
// but the prior attempt rolled back, so no double-write occurs.
//
// The rest of the contract is identical to PaymentOutboxWriter: the ctx is
// transaction-scoped (piggy-back the INSERT onto it), do lightweight INSERTs
// only, key rows for dedup (at-least-once), delegate real delivery to a
// separate relay, and note that a NoopTxManager gives no atomicity.
//
// This coexists with the post-commit OnInvoiceIssuedHook: an integrator that
// wires both fires two channels for one finalize. Use the outbox writer for
// guaranteed delivery (durable, in-tx) and OnInvoiceIssued for best-effort,
// non-fatal work such as metrics.
type InvoiceOutboxWriter interface {
	// OnInvoiceFinalized is called inside the finalize transaction after the
	// finalized invoice is saved and before commit. inv is the finalized
	// (non-nil) invoice. A returned error rolls the finalize back.
	OnInvoiceFinalized(ctx context.Context, inv *invoice.Invoice) error
}
