package port

import (
	"context"

	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
)

// PendingPaymentDisposition is the consumer's verdict on a stale Pending
// payment surfaced by batch.StalePendingPaymentProcessor (issue #98).
type PendingPaymentDisposition string

const (
	// PendingPaymentKeep: the payment is still genuinely in flight (e.g. the
	// customer has not finished 3DS, a konbini slip is still within its payment
	// window, an async settlement webhook has simply not arrived yet). The
	// batch leaves the record untouched and counts it as Skipped; a later run
	// re-evaluates it.
	PendingPaymentKeep PendingPaymentDisposition = "keep"

	// PendingPaymentMarkFailed: the gateway shows the payment's transaction was
	// refunded / voided / expired — the local Pending record is an orphan (its
	// money flow was already reversed, typically by saga compensation after a
	// 3DS split-brain; see payment-gateway.md §6.1.8). The batch routes the
	// record through PaymentService.MarkPaymentFailed, which performs the
	// Pending→Failed transition with its usual semantics (idempotent replay on
	// already-Failed, OnPaymentFailed hooks post-commit and only on the real
	// transition, invoice untouched).
	PendingPaymentMarkFailed PendingPaymentDisposition = "mark_failed"
)

// PendingPaymentReconciler is the integrator port that classifies a stale
// Pending payment for batch.StalePendingPaymentProcessor (issue #98).
//
// # Why this is a port (BYO boundary)
//
// Deciding whether a stale Pending record is an orphan requires knowing the
// GATEWAY-side state of its transaction (was it voided/refunded by saga
// compensation? did the payment instruction expire? is a 3DS challenge still
// open?). That knowledge is gateway-specific and lives entirely on the
// consumer's side of the BYO-Gateway boundary: the CONSUMER's implementation
// performs whatever gateway lookup / reconciliation-report matching it needs
// and returns only the verdict. The core never queries the gateway here — it
// only provides the scan (payment.Repository.FindStalePending), the loop, and
// the state transition (PaymentService.MarkPaymentFailed).
//
// # Contract
//
//   - p is a stale Pending payment (status Pending at scan time, ProcessedAt
//     older than the processor's staleness threshold). inv is the payment's
//     invoice when it could be cheaply loaded, or nil when the processor has
//     no invoice repository wired or the lookup failed — implementations must
//     tolerate a nil invoice.
//   - Return PendingPaymentKeep to leave the record alone (counted as
//     Skipped; re-evaluated on the next run) or PendingPaymentMarkFailed to
//     have the batch fail it via PaymentService.MarkPaymentFailed.
//   - A returned error fails that item (honoring BatchOptions.ContinueOnError)
//     without touching the record.
//   - Implementations should be read-only: the state transition belongs to
//     the batch (via MarkPaymentFailed) so hook firing rules stay intact. A
//     dry run (BatchOptions.DryRun) still calls this method to classify, so
//     side effects here would leak out of dry runs.
//   - The batch may call this for the same payment on every run until a
//     non-Keep disposition is applied; implementations should be idempotent
//     and reasonably cheap (they run once per stale payment per run).
type PendingPaymentReconciler interface {
	ReconcilePendingPayment(ctx context.Context, p *payment.Payment, inv *invoice.Invoice) (PendingPaymentDisposition, error)
}
