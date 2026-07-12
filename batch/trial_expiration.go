package batch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// TrialExpirationProcessor ends trials for trialing contracts whose
// TrialEndDate has passed.
//
// Whether the contract converts to a paid subscription follows
// TrialConfiguration.AutoConvert:
//   - AutoConvert=true  → EndTrial(converted=true)  → contract becomes Active
//   - AutoConvert=false → EndTrial(converted=false) → contract becomes Cancelled
//
// When AutoConvert=true and RequirePaymentMethod=true but the contract has no
// payment method registered, conversion is blocked and recorded as a failure
// in the BatchResult: the contract stays Trialing (no hooks fire) and will not
// convert until a payment method is registered or RequirePaymentMethod is
// cleared (design-decisions.md section 2.1).
//
// After a successful save, OnContractTrialEndHook and OnContractChangeHook
// (ChangeType=trial_end) fire as non-fatal post-commit notifications.
type TrialExpirationProcessor struct {
	contractRepo contract.Repository
	registry     *plugin.Registry
	clock        shared.Clock
	txManager    tx.TxManager
	logger       *slog.Logger
}

var _ BatchProcessor = (*TrialExpirationProcessor)(nil)

// NewTrialExpirationProcessor creates a new TrialExpirationProcessor.
// If txManager is nil, a default NoopTxManager is used and a Warn-level log is
// emitted (trial-end writes will NOT be atomic). Pass a real TxManager for
// production, or tx.NewNoopTxManagerExplicit(...) to acknowledge intentional
// non-atomic in-memory/test use and suppress the warning.
func NewTrialExpirationProcessor(
	contractRepo contract.Repository,
	registry *plugin.Registry,
	clock shared.Clock,
	txManager tx.TxManager,
	logger *slog.Logger,
) *TrialExpirationProcessor {
	if txManager == nil {
		txManager = tx.NewNoopTxManager(tx.Repos{
			Contracts: contractRepo,
		})
	}
	if logger == nil {
		logger = slog.Default()
	}
	tx.WarnIfDefaultNoop(logger, txManager, "TrialExpirationProcessor", "pass a real TxManager to NewTrialExpirationProcessor (or tx.NewNoopTxManagerExplicit(...) to acknowledge non-atomic in-memory use)")
	return &TrialExpirationProcessor{
		contractRepo: contractRepo,
		registry:     registry,
		clock:        clock,
		txManager:    txManager,
		logger:       logger,
	}
}

// Process finds trialing contracts whose trial has ended and ends their trials.
func (p *TrialExpirationProcessor) Process(ctx context.Context, opts BatchOptions) (*BatchResult, error) {
	now := p.clock.Now()
	// FindTrialsEndingBefore(ctx, now) returns trialing contracts whose
	// TrialEndDate is before `now` — i.e. trials that have already expired.
	contracts, err := p.contractRepo.FindTrialsEndingBefore(ctx, now, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("failed to find expired trials: %w", err)
	}

	result := &BatchResult{
		Total: len(contracts),
	}

	if len(contracts) == 0 {
		return result, nil
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	if concurrency == 1 || opts.DryRun {
		// Sequential processing
		for _, agg := range contracts {
			// External cancellation aborts the run: the remainder is skipped
			// and the cancellation is surfaced as the run's error, so a
			// cancelled run is never mistaken for a clean one (issue #242).
			if ctxErr := ctx.Err(); ctxErr != nil {
				result.Skipped = result.Total - result.Succeeded - result.Failed
				return result, ctxErr
			}
			if err := p.processOne(ctx, agg, opts.DryRun); err != nil {
				result.Failed++
				result.Errors = append(result.Errors, fmt.Errorf("contract %s: %w", agg.ContractID(), err))
				if !opts.ContinueOnError {
					// Items never attempted because of the early stop are
					// skipped, not failed, so Total == Succeeded+Failed+Skipped
					// (issue #242).
					result.Skipped = result.Total - result.Succeeded - result.Failed
					return result, ctx.Err()
				}
			} else {
				result.Succeeded++
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

	for _, agg := range contracts {
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
		go func(a *contract.ContractAggregate) {
			defer func() { <-sem }()

			if err := p.processOne(cctx, a, opts.DryRun); err != nil {
				mu.Lock()
				if stopped && errors.Is(err, context.Canceled) {
					// The run was already stopped internally (the early stop
					// cancelled the shared context); an in-flight cancellation
					// is not a genuine per-item failure (issue #242).
					result.Skipped++
				} else {
					result.Failed++
					result.Errors = append(result.Errors, fmt.Errorf("contract %s: %w", a.ContractID(), err))
				}
				mu.Unlock()
				if !opts.ContinueOnError {
					mu.Lock()
					stopped = true
					mu.Unlock()
					cancel()
				}
			} else {
				mu.Lock()
				result.Succeeded++
				mu.Unlock()
			}
		}(agg)
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

// evaluateTrialEnd validates the trial-end guards against the given aggregate
// and returns the conversion decision.
//
// Guards:
//   - the contract must be Trialing with a trial configuration;
//   - the trial must have ended (TrialEndDate not after clock.Now());
//   - RequirePaymentMethod gate (design-decisions 2.1: 支払い方法事前登録必須):
//     auto-conversion without a registered payment method is BLOCKED — the
//     contract stays Trialing, no hooks fire, and the batch records this
//     contract as a failure (subject to ContinueOnError). The contract will
//     keep failing on subsequent runs until the operator either registers a
//     payment method or clears RequirePaymentMethod.
//
// Conversion follows TrialConfiguration.AutoConvert (design-decisions 2.1):
// AutoConvert=true converts to a paid contract; otherwise the trial ends
// without conversion and the contract is cancelled.
//
// processOne evaluates this once against the scan-time aggregate (a cheap
// pre-filter that also serves as the dry-run validation) and AGAIN inside the
// transaction against the freshly loaded aggregate, so a state change between
// scan and tx — e.g. a payment method detached, the trial extended, or the
// contract cancelled — can never leak a stale decision into EndTrial
// (issue #242).
func (p *TrialExpirationProcessor) evaluateTrialEnd(agg *contract.ContractAggregate) (bool, error) {
	if agg.Status() != contract.ContractStatusTrialing {
		return false, shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot end trial: status is %s", agg.Status()))
	}
	cfg := agg.TrialConfig()
	if cfg == nil {
		return false, shared.NewDomainError(shared.ErrCodeBusinessRule,
			"trialing contract has no trial configuration")
	}
	if cfg.TrialEndDate.After(p.clock.Now()) {
		return false, shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("trial has not ended yet (ends at %s)", cfg.TrialEndDate.Format("2006-01-02T15:04:05Z07:00")))
	}

	converted := cfg.AutoConvert
	if converted && cfg.RequirePaymentMethod && agg.PaymentMethodID() == nil {
		return false, shared.NewDomainError(shared.ErrCodeBusinessRule,
			"cannot auto-convert trial: RequirePaymentMethod is set but no payment method is registered")
	}
	return converted, nil
}

func (p *TrialExpirationProcessor) processOne(ctx context.Context, agg *contract.ContractAggregate, dryRun bool) error {
	metadata := eventstore.EventMetadata{
		UserID: "system:batch:trial_expiration",
	}

	// Scan-time evaluation: a cheap pre-filter for real runs and the guard
	// validation for dry runs. The scan-time conversion decision is discarded —
	// the authoritative decision is re-evaluated inside the transaction against
	// fresh state (issue #242).
	if _, err := p.evaluateTrialEnd(agg); err != nil {
		return err
	}

	if dryRun {
		// All guards passed; report what would happen without side effects.
		return nil
	}

	// Load-mutate-save inside the transaction against a repository-loaded
	// instance (issue #151). EndTrial and Save run inside the tx boundary so a
	// failed Save never leaves a half-ended trial — carrying dangling uncommitted
	// events and an advanced status — behind for the next batch run. On a real
	// adapter FindByID materializes a fresh aggregate from history, so the
	// instance returned by FindTrialsEndingBefore is never mutated when the trial
	// end fails to persist. Save runs BEFORE hooks to prevent a "notified but not
	// persisted" inconsistency.
	var (
		ended     *contract.ContractAggregate
		oldStatus contract.ContractStatus
		newStatus contract.ContractStatus
		converted bool
	)
	if err := p.txManager.RunInTx(ctx, func(txCtx context.Context, repos tx.Repos) error {
		contractRepo := repos.Contracts
		if contractRepo == nil {
			contractRepo = p.contractRepo
		}
		loaded, findErr := contractRepo.FindByID(txCtx, agg.ContractID())
		if findErr != nil {
			return fmt.Errorf("failed to load contract for trial end: %w", findErr)
		}

		// Re-evaluate the conversion decision against the FRESHLY loaded
		// aggregate (issue #242): the scan-time decision above may be stale —
		// e.g. the payment method was detached between the scan and this tx,
		// in which case auto-converting with the stale decision would bypass
		// the RequirePaymentMethod gate.
		freshConverted, evalErr := p.evaluateTrialEnd(loaded)
		if evalErr != nil {
			return evalErr
		}
		converted = freshConverted

		oldStatus = loaded.Status()
		if endErr := loaded.EndTrial(converted, metadata); endErr != nil {
			return endErr
		}
		newStatus = loaded.Status()

		if saveErr := contractRepo.Save(txCtx, loaded); saveErr != nil {
			return saveErr
		}
		ended = loaded
		return nil
	}); err != nil {
		return err
	}

	// Subsequent post-commit hooks operate on the persisted,
	// repository-loaded aggregate.
	agg = ended

	// Post-commit hooks — non-fatal. Contract is already persisted;
	// hook failures are logged but do not fail the trial expiration.
	if p.registry != nil {
		pluginCtx := plugin.NewContext(ctx)

		for _, hook := range p.registry.GetOnContractTrialEndHooks() {
			if hookErr := plugin.SafeInvoke("OnContractTrialEndHook.OnContractTrialEnd", hook.Name(), func() error {
				return hook.OnContractTrialEnd(pluginCtx, agg, converted)
			}); hookErr != nil {
				plugin.LogNonFatalHookError(p.logger, "post-commit trial end hook failed", hookErr,
					"hook", hook.Name(),
					"contractID", agg.ContractID(),
				)
			}
		}

		changeEvent := plugin.ContractChangeEvent{
			ContractID: agg.ContractID(),
			ChangeType: plugin.ContractChangeTrialEnd,
			OldStatus:  &oldStatus,
			NewStatus:  &newStatus,
			Timestamp:  p.clock.Now(),
		}
		for _, hook := range p.registry.GetOnContractChangeHooks() {
			if hookErr := plugin.SafeInvoke("OnContractChangeHook.OnContractChange", hook.Name(), func() error {
				return hook.OnContractChange(pluginCtx, changeEvent)
			}); hookErr != nil {
				plugin.LogNonFatalHookError(p.logger, "post-commit change hook failed", hookErr,
					"hook", hook.Name(),
					"contractID", agg.ContractID(),
				)
			}
		}
	}

	return nil
}
