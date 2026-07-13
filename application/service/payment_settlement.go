package service

import (
	"context"
	"fmt"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// SettlePayment completes a Pending payment once its asynchronous funds have
// arrived (bank transfer received, konbini slip paid, carrier billing
// confirmed, ...). Integrators call it from their webhook handling when the
// gateway delivers a `payment.received` event ([port.WebhookEventPaymentReceived])
// for a payment that ProcessPayment previously returned with [ErrPaymentPending]
// — and it equally settles a Pending 3DS payment if the integrator confirms
// completion out-of-band.
//
// Semantics:
//
//   - Pending → Completed: the payment transitions via Complete(), the amount
//     is recorded on the invoice (RecordPayment), and both are saved inside a
//     single transaction (tx.Run). The [port.PaymentOutboxWriter], when wired,
//     fires INSIDE the transaction right after both saves and before commit —
//     the same §11 (plugin-system.md) contract as ProcessPayment; a writer
//     error or panic vetoes (rolls back) the settlement, which is harmless
//     here (no gateway money moves — the next webhook redelivery retries).
//   - Already Completed: idempotent no-op success — the payment is returned
//     unchanged, and no saves, outbox writes, or hooks fire. This makes
//     at-least-once webhook redelivery safe.
//   - Failed / Refunded / PartiallyRefunded / ChargedBack: returns an
//     invalid_state_transition [shared.DomainError] — a terminal payment must
//     not be silently resurrected as paid.
//
// Concurrency: the load → transition → save sequence runs inside
// tx.RetryOnConflict + tx.Run (the same pattern as Refund and
// BillingService.FinalizeInvoice). On an optimistic-locking backend a
// concurrent settlement makes the loser's Save fail with a version conflict;
// the retry re-reads the winner's Completed state and converges on the
// idempotent no-op path.
//
// Hooks: on an actual settlement (not the no-op replay), AfterCharge and
// OnPaymentProcessed hooks fire post-commit, non-fatal — the same fatality
// policy as ProcessPayment's success path. BeforeCharge does NOT fire: no
// gateway charge is being made here.
func (s *PaymentService) SettlePayment(ctx context.Context, paymentID shared.PaymentID) (*payment.Payment, error) {
	var settled *payment.Payment
	var settledInv *invoice.Invoice
	var settledNow bool
	err := tx.RetryOnConflict(paymentMaxRetries, func() error {
		// Reset per attempt so a conflicted attempt cannot leak state.
		settled = nil
		settledInv = nil
		settledNow = false
		return tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
			loaded, findErr := repos.Payments.FindByID(txCtx, paymentID)
			if findErr != nil {
				return fmt.Errorf("failed to load payment for settlement: %w", findErr)
			}
			// Defensive nil-guard (issue #197): FindByID is documented to error on
			// a missing payment, but a BYO-DB adapter returning (nil, nil) would
			// otherwise nil-panic below.
			if loaded == nil {
				return shared.NewDomainError(shared.ErrCodeNotFound,
					fmt.Sprintf("payment %s not found", paymentID))
			}

			// All PaymentStatus values are listed explicitly (no default) so the
			// `exhaustive` lint catches new states added in domain/payment.
			switch loaded.Status() {
			case payment.PaymentStatusCompleted:
				// Idempotent replay (e.g. webhook redelivery, or a concurrent
				// settlement won). No new save → no outbox, no hooks.
				settled = loaded
				return nil
			case payment.PaymentStatusPending:
				// Proceed to settle below.
			case payment.PaymentStatusFailed,
				payment.PaymentStatusRefunded,
				payment.PaymentStatusPartiallyRefunded,
				payment.PaymentStatusChargedBack:
				return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
					fmt.Sprintf("cannot settle payment %s: current status is %s", paymentID, loaded.Status()))
			}

			invoiceRepo := repos.Invoices
			if invoiceRepo == nil {
				invoiceRepo = s.invoiceRepo
			}
			inv, invErr := invoiceRepo.FindByID(txCtx, loaded.InvoiceID())
			if invErr != nil {
				return fmt.Errorf("failed to load invoice for settlement: %w", invErr)
			}
			if inv == nil {
				return shared.NewDomainError(shared.ErrCodeNotFound,
					fmt.Sprintf("invoice %s not found", loaded.InvoiceID()))
			}

			if completeErr := loaded.Complete(); completeErr != nil {
				return fmt.Errorf("failed to complete pending payment: %w", completeErr)
			}
			if recordErr := inv.RecordPayment(loaded.Amount(), s.clock.Now()); recordErr != nil {
				return fmt.Errorf("failed to record settled payment on invoice: %w", recordErr)
			}
			// Save errors are returned unwrapped so tx.IsVersionConflict recognizes
			// an optimistic-lock conflict and RetryOnConflict re-runs the closure
			// against the winner's fresh state.
			if saveErr := repos.Payments.Save(txCtx, loaded); saveErr != nil {
				return saveErr
			}
			if saveErr := repos.Invoices.Save(txCtx, inv); saveErr != nil {
				return saveErr
			}
			// Transactional outbox (issue #248): both rows saved — write the
			// durable notification row in this same transaction before commit.
			if outboxErr := s.firePaymentOutbox(txCtx, loaded, inv); outboxErr != nil {
				return outboxErr
			}
			settled = loaded
			settledInv = inv
			settledNow = true
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	if settledNow {
		// Post-commit hooks (non-fatal): the settlement is persisted, so a hook
		// error or panic must not unwind it (plugin panic policy §5.4).
		successCtx := plugin.NewPaymentContext(ctx, settled, settledInv)
		for _, hook := range s.registry.GetAfterChargeHooks() {
			if hookErr := plugin.SafeInvoke("AfterChargeHook.AfterCharge", hook.Name(), func() error {
				return hook.AfterCharge(successCtx)
			}); hookErr != nil {
				plugin.LogNonFatalHookError(s.logger, "AfterCharge hook failed", hookErr,
					"hook", hook.Name(),
					"paymentID", settled.ID(),
					"invoiceID", settled.InvoiceID(),
				)
			}
		}
		// Fresh context per hook category (issue #223): a mutation (SetContract)
		// by an AfterCharge plugin must not leak into metrics hooks.
		metricsCtx := plugin.NewPaymentContext(ctx, settled, settledInv)
		for _, hook := range s.registry.GetOnPaymentProcessedHooks() {
			if hookErr := plugin.SafeInvoke("OnPaymentProcessedHook.OnPaymentProcessed", hook.Name(), func() error {
				return hook.OnPaymentProcessed(metricsCtx)
			}); hookErr != nil {
				plugin.LogNonFatalHookError(s.logger, "OnPaymentProcessed hook failed", hookErr,
					"hook", hook.Name(),
					"paymentID", settled.ID(),
					"invoiceID", settled.InvoiceID(),
				)
			}
		}
	}

	return settled, nil
}

// MarkPaymentFailed transitions a Pending payment to Failed with the given
// reason. Integrators call it from their webhook handling when an
// asynchronous payment instruction expires or the gateway reports the
// out-of-band payment as failed (e.g. the konbini slip lapsed, the bank
// transfer never arrived before the deadline).
//
// Semantics:
//
//   - Pending → Failed: the payment transitions via Fail(reason) and is saved.
//     The invoice is NOT touched — nothing was ever recorded on it for a
//     pending payment, so there is nothing to unwind.
//   - Already Failed: idempotent no-op success (safe under at-least-once
//     webhook redelivery). The stored failure reason is kept; the new reason
//     is not overwritten.
//   - Completed / Refunded / PartiallyRefunded / ChargedBack: returns an
//     invalid_state_transition [shared.DomainError]. In particular, a late
//     expiry notification racing an already-settled payment must NOT knock a
//     Completed payment back to Failed.
//
// Hooks: on an actual transition (not the no-op replay), OnPaymentFailed
// hooks fire post-commit, non-fatal — the same fatality policy as
// ProcessPayment's gateway-failure path. The invoice is loaded best-effort
// for the hook context; a lookup failure is logged and hooks receive a nil
// invoice, mirroring Refund.
func (s *PaymentService) MarkPaymentFailed(ctx context.Context, paymentID shared.PaymentID, reason string) (*payment.Payment, error) {
	var failed *payment.Payment
	var failedNow bool
	err := tx.RetryOnConflict(paymentMaxRetries, func() error {
		failed = nil
		failedNow = false
		return tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
			loaded, findErr := repos.Payments.FindByID(txCtx, paymentID)
			if findErr != nil {
				return fmt.Errorf("failed to load payment for failure marking: %w", findErr)
			}
			if loaded == nil {
				return shared.NewDomainError(shared.ErrCodeNotFound,
					fmt.Sprintf("payment %s not found", paymentID))
			}

			// Exhaustive over PaymentStatus (no default) for the lint guarantee.
			switch loaded.Status() {
			case payment.PaymentStatusFailed:
				// Idempotent replay: already failed. No save, no hooks.
				failed = loaded
				return nil
			case payment.PaymentStatusPending:
				// Proceed to fail below.
			case payment.PaymentStatusCompleted,
				payment.PaymentStatusRefunded,
				payment.PaymentStatusPartiallyRefunded,
				payment.PaymentStatusChargedBack:
				return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
					fmt.Sprintf("cannot mark payment %s as failed: current status is %s", paymentID, loaded.Status()))
			}

			if failErr := loaded.Fail(reason); failErr != nil {
				return fmt.Errorf("failed to mark payment as failed: %w", failErr)
			}
			// Unwrapped so RetryOnConflict can recognize a version conflict.
			if saveErr := repos.Payments.Save(txCtx, loaded); saveErr != nil {
				return saveErr
			}
			failed = loaded
			failedNow = true
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	if failedNow {
		// Best-effort invoice lookup for the hook context (mirrors Refund).
		inv, invErr := s.invoiceRepo.FindByID(ctx, failed.InvoiceID())
		if invErr != nil {
			s.logger.Warn("invoice lookup failed on async payment failure",
				"paymentID", paymentID,
				"invoiceID", failed.InvoiceID(),
				"error", invErr,
			)
			inv = nil
		}
		failureErr := fmt.Errorf("asynchronous payment failed: %s", reason)
		failCtx := plugin.NewPaymentContext(ctx, failed, inv)
		for _, hook := range s.registry.GetOnPaymentFailedHooks() {
			if hookErr := plugin.SafeInvoke("OnPaymentFailedHook.OnPaymentFailed", hook.Name(), func() error {
				return hook.OnPaymentFailed(failCtx, failureErr)
			}); hookErr != nil {
				plugin.LogNonFatalHookError(s.logger, "OnPaymentFailed hook failed", hookErr,
					"hook", hook.Name(),
					"paymentID", paymentID,
					"invoiceID", failed.InvoiceID(),
				)
			}
		}
	}

	return failed, nil
}
