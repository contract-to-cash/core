package batch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
)

// StalePendingActionMarkFailed is the dry-run action label reported by
// StalePendingPaymentProcessor in BatchResult.DryRunActions for a payment the
// reconciler classified as an orphan (a real run would route it through
// PaymentService.MarkPaymentFailed). Derived from port.PendingPaymentMarkFailed
// so the two labels ("the reconciler's disposition" and "the dry-run action")
// cannot drift apart into two separately-maintained "mark_failed" literals.
const StalePendingActionMarkFailed = string(port.PendingPaymentMarkFailed)

// DefaultStalePendingAfter is the staleness threshold used when
// NewStalePendingPaymentProcessor receives a non-positive staleAfter. 24h is a
// conservative floor: it exceeds every shipped gateway's synchronous-settlement
// horizon and typical 3DS challenge windows, so a Pending record younger than
// this is plausibly still in flight and not worth a reconciliation lookup.
const DefaultStalePendingAfter = 24 * time.Hour

// PendingPaymentFailer is the narrow seam through which the processor applies
// the MarkFailed disposition. *service.PaymentService satisfies it — pass the
// SAME PaymentService the rest of your integration uses, so the Pending→Failed
// transition keeps its established semantics (issue #98): the transition runs
// in the service's own transaction (tx.RetryOnConflict + tx.Run), an
// already-Failed record is an idempotent no-op, a Completed/Refunded/
// PartiallyRefunded/ChargedBack record is rejected with
// invalid_state_transition, and OnPaymentFailed hooks fire post-commit,
// non-fatally, ONLY on the real Pending→Failed transition. The batch never
// reimplements any of that.
type PendingPaymentFailer interface {
	MarkPaymentFailed(ctx context.Context, paymentID shared.PaymentID, reason string) (*payment.Payment, error)
}

// StalePendingFailureReason is the failure reason recorded on payments the
// reconciler classified as orphans (passed as the reason string to
// PaymentService.MarkPaymentFailed, which is what reaches integrator
// OnPaymentFailedHook implementations via the returned error's message).
//
// Exported so hook implementations can distinguish this batch-driven cleanup
// from a genuine payment failure without substring-matching an unexported
// sentence: check strings.Contains(err.Error(), batch.StalePendingFailureReason)
// (or errors.As to a *shared.DomainError and inspect its message) to, for
// example, suppress dunning/paging for these orphan reconciliations while
// still alerting on real OnPaymentFailed events.
const StalePendingFailureReason = "stale pending payment reconciled as orphaned: gateway shows the transaction was refunded/voided/expired (batch.StalePendingPaymentProcessor, issue #98)"

// StalePendingPaymentProcessor cleans up stale Pending payment records
// (issue #98).
//
// Compensation-after-3DS leaves orphan Pending records: call 1 saves a Pending
// payment for a 3DS requires_action charge, call 2 captures but its local tx
// fails, saga compensation reverses the gateway transaction and burns the
// idempotency key, and call 3 succeeds under a fresh effective key — call 1's
// Pending record is now referenced by nothing and pollutes listings and
// metrics forever (payment-gateway.md §6.1.8). The same shape arises when an
// async payment instruction (konbini slip, bank-transfer window) silently
// lapses without a gateway webhook.
//
// The core deliberately does NOT decide which stale Pending records are
// orphans — that requires gateway-side knowledge (was the transaction voided?
// is a 3DS challenge still open?) that lives on the consumer's side of the
// BYO-Gateway boundary. The split is:
//
//   - core scans: payment.Repository.FindStalePending selects Pending
//     payments whose ProcessedAt is older than the staleness threshold;
//   - consumer classifies: the wired [port.PendingPaymentReconciler] returns
//     Keep (still genuinely in flight → counted as Skipped, re-evaluated next
//     run) or MarkFailed (orphan);
//   - core transitions: MarkFailed dispositions are routed through the
//     EXISTING PaymentService.MarkPaymentFailed (see PendingPaymentFailer for
//     the semantics that reuse preserves — real-transition-only OnPaymentFailed
//     hooks, idempotent replay, terminal-state rejection).
//
// The processor itself performs no writes and needs no TxManager: the only
// write path is MarkPaymentFailed, which manages its own transaction.
//
// Like the other batch processors, scheduling is the consumer's concern — run
// Process from your cron / CronJob / Cloud Scheduler. Re-runs are idempotent:
// records failed by a previous run are no longer Pending and never re-selected.
type StalePendingPaymentProcessor struct {
	paymentRepo payment.Repository
	invoiceRepo invoice.Repository // optional; nil → reconciler receives a nil invoice
	reconciler  port.PendingPaymentReconciler
	failer      PendingPaymentFailer
	staleAfter  time.Duration
	clock       shared.Clock
	logger      *slog.Logger
}

var _ BatchProcessor = (*StalePendingPaymentProcessor)(nil)

// NewStalePendingPaymentProcessor creates a new StalePendingPaymentProcessor.
//
// invoiceRepo is optional (nil allowed): when wired, the payment's invoice is
// loaded best-effort and passed to the reconciler for cheap context; a lookup
// failure is logged at Warn and the reconciler receives nil. staleAfter is the
// minimum age (now − ProcessedAt) before a Pending payment is scanned; a
// non-positive value falls back to DefaultStalePendingAfter. reconciler and
// failer are required — pass your PendingPaymentReconciler implementation and
// the PaymentService instance (which satisfies PendingPaymentFailer).
func NewStalePendingPaymentProcessor(
	paymentRepo payment.Repository,
	invoiceRepo invoice.Repository,
	reconciler port.PendingPaymentReconciler,
	failer PendingPaymentFailer,
	staleAfter time.Duration,
	clock shared.Clock,
	logger *slog.Logger,
) *StalePendingPaymentProcessor {
	if logger == nil {
		logger = slog.Default()
	}
	if staleAfter <= 0 {
		staleAfter = DefaultStalePendingAfter
	}
	return &StalePendingPaymentProcessor{
		paymentRepo: paymentRepo,
		invoiceRepo: invoiceRepo,
		reconciler:  reconciler,
		failer:      failer,
		staleAfter:  staleAfter,
		clock:       clock,
		logger:      logger,
	}
}

// Process finds stale Pending payments, asks the reconciler for a disposition
// per payment, and fails the orphans via PaymentService.MarkPaymentFailed.
//
// Result accounting (Total == Succeeded + Failed + Skipped, issue #242):
// MarkFailed dispositions applied (or, in a dry run, reported) count as
// Succeeded; Keep dispositions — and defensive skips for records that changed
// state between scan and processing — count as Skipped; reconciler and
// transition errors count as Failed, honoring ContinueOnError. Dry runs report
// one DryRunActions entry (action "mark_failed") per would-fail payment and
// perform no writes — the reconciler is still consulted (it is read-only by
// contract).
func (p *StalePendingPaymentProcessor) Process(ctx context.Context, opts BatchOptions) (*BatchResult, error) {
	olderThan := p.clock.Now().Add(-p.staleAfter)
	payments, err := p.paymentRepo.FindStalePending(ctx, olderThan, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("failed to find stale pending payments: %w", err)
	}

	result := &BatchResult{
		Total: len(payments),
	}

	if len(payments) == 0 {
		return result, nil
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	if concurrency == 1 || opts.DryRun {
		// Sequential processing
		for _, pmt := range payments {
			// External cancellation aborts the run: the remainder is skipped
			// and the cancellation is surfaced as the run's error, so a
			// cancelled run is never mistaken for a clean one (issue #242).
			if ctxErr := ctx.Err(); ctxErr != nil {
				result.Skipped = result.Total - result.Succeeded - result.Failed
				return result, ctxErr
			}
			disposition, err := p.processOne(ctx, pmt, olderThan, opts.DryRun)
			switch {
			case err != nil:
				result.Failed++
				result.Errors = append(result.Errors, fmt.Errorf("payment %s: %w", pmt.ID(), err))
				if !opts.ContinueOnError {
					// Items never attempted because of the early stop are
					// skipped, not failed, so Total == Succeeded+Failed+Skipped
					// (issue #242).
					result.Skipped = result.Total - result.Succeeded - result.Failed
					return result, ctx.Err()
				}
			case disposition == port.PendingPaymentMarkFailed:
				result.Succeeded++
				if opts.DryRun {
					result.DryRunActions = append(result.DryRunActions, DryRunAction{
						ItemID: string(pmt.ID()),
						Action: StalePendingActionMarkFailed,
					})
				}
			default:
				// Keep (and defensive skips): deliberately not processed.
				result.Skipped++
			}
		}
		return result, ctx.Err()
	}

	// Concurrent processing
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	// stopped marks the internal early stop (ContinueOnError=false after a
	// genuine failure). It is set under mu BEFORE cancel() so an in-flight
	// item failing with context.Canceled can tell an internal early stop
	// (benign: counted as Skipped) apart from an external caller cancellation
	// (abnormal: counted as Failed and surfaced via the returned error)
	// (issue #242).
	stopped := false
	launched := 0

	for _, pmt := range payments {
		// External cancellation aborts the launch loop; unlaunched items are
		// counted as skipped below and the cancellation is surfaced as the
		// run's error.
		if ctx.Err() != nil {
			break
		}
		// Check if we should stop early (ContinueOnError=false and an error occurred)
		if !opts.ContinueOnError {
			mu.Lock()
			failed := result.Failed
			mu.Unlock()
			if failed > 0 {
				break
			}
		}
		launched++

		sem <- struct{}{}
		go func(stale *payment.Payment) {
			defer func() { <-sem }()

			disposition, err := p.processOne(cctx, stale, olderThan, opts.DryRun)
			switch {
			case err != nil:
				mu.Lock()
				if stopped && errors.Is(err, context.Canceled) {
					// The run was already stopped internally (the early stop
					// cancelled the shared context); an in-flight cancellation
					// is not a genuine per-item failure (issue #242).
					result.Skipped++
				} else {
					result.Failed++
					result.Errors = append(result.Errors, fmt.Errorf("payment %s: %w", stale.ID(), err))
				}
				mu.Unlock()
				if !opts.ContinueOnError {
					mu.Lock()
					stopped = true
					mu.Unlock()
					cancel()
				}
			case disposition == port.PendingPaymentMarkFailed:
				mu.Lock()
				result.Succeeded++
				mu.Unlock()
			default:
				mu.Lock()
				result.Skipped++
				mu.Unlock()
			}
		}(pmt)
	}

	// Wait for all goroutines to finish
	for i := 0; i < concurrency; i++ {
		sem <- struct{}{}
	}

	// Items never launched because of an early stop or an external
	// cancellation are skipped, not failed (issue #242).
	result.Skipped += result.Total - launched

	// Surface an external cancellation so a cancelled run is never mistaken
	// for a clean partial run (nil when the caller's context is intact).
	return result, ctx.Err()
}

// processOne classifies and (for real runs) applies the disposition for one
// stale Pending payment. It returns the applied disposition: MarkFailed means
// the payment was failed (or, in a dry run, would be) and counts as Succeeded;
// Keep means the record was deliberately left alone and counts as Skipped.
func (p *StalePendingPaymentProcessor) processOne(ctx context.Context, stale *payment.Payment, olderThan time.Time, dryRun bool) (port.PendingPaymentDisposition, error) {
	// Defense-in-depth guards against a permissive FindStalePending adapter
	// (mirroring ContractRenewalProcessor.skipNoInterval): a mis-selected
	// record is Skipped with a Warn rather than Failed with a confusing
	// transition error, and a compliant re-run stays clean.
	if stale.Status() != payment.PaymentStatusPending {
		p.logger.Warn("skipping non-pending payment selected by FindStalePending (adapter should only return Pending records)",
			"paymentID", stale.ID(),
			"status", stale.Status(),
		)
		return port.PendingPaymentKeep, nil
	}
	if !stale.ProcessedAt().Before(olderThan) {
		p.logger.Warn("skipping not-yet-stale payment selected by FindStalePending (adapter should apply the strict olderThan cutoff)",
			"paymentID", stale.ID(),
			"processedAt", stale.ProcessedAt(),
			"olderThan", olderThan,
		)
		return port.PendingPaymentKeep, nil
	}

	// Best-effort invoice context for the reconciler (nil is part of the port
	// contract; a lookup failure must not block reconciliation of the payment).
	var inv *invoice.Invoice
	if p.invoiceRepo != nil {
		loaded, invErr := p.invoiceRepo.FindByID(ctx, stale.InvoiceID())
		if invErr != nil {
			p.logger.Warn("invoice lookup failed for stale pending payment; reconciler receives nil invoice",
				"paymentID", stale.ID(),
				"invoiceID", stale.InvoiceID(),
				"error", invErr,
			)
		} else {
			inv = loaded
		}
	}

	disposition, err := p.reconciler.ReconcilePendingPayment(ctx, stale, inv)
	if err != nil {
		return "", fmt.Errorf("pending-payment reconciliation failed: %w", err)
	}

	switch disposition {
	case port.PendingPaymentKeep:
		return port.PendingPaymentKeep, nil
	case port.PendingPaymentMarkFailed:
		if dryRun {
			// All guards passed and the reconciler classified the record as an
			// orphan; report what a real run would do without side effects.
			return port.PendingPaymentMarkFailed, nil
		}
		if _, failErr := p.failer.MarkPaymentFailed(ctx, stale.ID(), StalePendingFailureReason); failErr != nil {
			// A payment that left Pending between the scan and this call (e.g.
			// a late settlement webhook completed it) is rejected by
			// MarkPaymentFailed with invalid_state_transition — that is the
			// guard doing its job, not a batch failure. Skip with a Warn; the
			// record is terminal and will not be re-selected.
			var de *shared.DomainError
			if errors.As(failErr, &de) && de.Code == shared.ErrCodeInvalidStateTransition {
				p.logger.Warn("stale pending payment left Pending between scan and transition; skipping (reconciler verdict was stale)",
					"paymentID", stale.ID(),
					"error", failErr,
				)
				return port.PendingPaymentKeep, nil
			}
			return "", fmt.Errorf("failed to mark stale pending payment as failed: %w", failErr)
		}
		p.logger.Info("stale pending payment marked failed (orphan reconciled, issue #98)",
			"paymentID", stale.ID(),
			"invoiceID", stale.InvoiceID(),
			"processedAt", stale.ProcessedAt(),
		)
		return port.PendingPaymentMarkFailed, nil
	default:
		return "", shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("reconciler returned unknown disposition %q for payment %s", disposition, stale.ID()))
	}
}
