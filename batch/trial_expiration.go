package batch

import (
	"context"
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
// If txManager is nil, a NoopTxManager is used.
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
	// FindTrialsEndingSoon(ctx, now) returns trialing contracts whose
	// TrialEndDate is before `now` — i.e. trials that have already expired.
	contracts, err := p.contractRepo.FindTrialsEndingSoon(ctx, now)
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
			if err := p.processOne(ctx, agg, opts.DryRun); err != nil {
				result.Failed++
				result.Errors = append(result.Errors, fmt.Errorf("contract %s: %w", agg.ContractID(), err))
				if !opts.ContinueOnError {
					return result, nil
				}
			} else {
				result.Succeeded++
			}
		}
		return result, nil
	}

	// Concurrent processing
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex

	for _, agg := range contracts {
		// Check if we should stop early (ContinueOnError=false and an error occurred)
		if !opts.ContinueOnError {
			mu.Lock()
			failed := result.Failed
			mu.Unlock()
			if failed > 0 {
				break
			}
		}

		sem <- struct{}{}
		go func(a *contract.ContractAggregate) {
			defer func() { <-sem }()

			if err := p.processOne(cctx, a, opts.DryRun); err != nil {
				mu.Lock()
				result.Failed++
				result.Errors = append(result.Errors, fmt.Errorf("contract %s: %w", a.ContractID(), err))
				mu.Unlock()
				if !opts.ContinueOnError {
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

	return result, nil
}

func (p *TrialExpirationProcessor) processOne(ctx context.Context, agg *contract.ContractAggregate, dryRun bool) error {
	metadata := eventstore.EventMetadata{
		UserID: "system:batch:trial_expiration",
	}

	// Guards — validated for both dry-run and real runs.
	if agg.Status() != contract.ContractStatusTrialing {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot end trial: status is %s", agg.Status()))
	}
	cfg := agg.TrialConfig()
	if cfg == nil {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			"trialing contract has no trial configuration")
	}
	if cfg.TrialEndDate.After(p.clock.Now()) {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("trial has not ended yet (ends at %s)", cfg.TrialEndDate.Format("2006-01-02T15:04:05Z07:00")))
	}

	// Conversion follows TrialConfiguration.AutoConvert (design-decisions 2.1):
	// AutoConvert=true converts to a paid contract; otherwise the trial ends
	// without conversion and the contract is cancelled.
	converted := cfg.AutoConvert

	// RequirePaymentMethod gate (design-decisions 2.1: 支払い方法事前登録必須):
	// auto-conversion without a registered payment method is BLOCKED — the
	// contract stays Trialing, no hooks fire, and the batch records this
	// contract as a failure (subject to ContinueOnError). The contract will
	// keep failing on subsequent runs until the operator either registers a
	// payment method or clears RequirePaymentMethod.
	if converted && cfg.RequirePaymentMethod && agg.PaymentMethodID() == nil {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			"cannot auto-convert trial: RequirePaymentMethod is set but no payment method is registered")
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
	// instance returned by FindTrialsEndingSoon is never mutated when the trial
	// end fails to persist. Save runs BEFORE hooks to prevent a "notified but not
	// persisted" inconsistency.
	var (
		ended     *contract.ContractAggregate
		oldStatus contract.ContractStatus
		newStatus contract.ContractStatus
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
			if hookErr := hook.OnContractTrialEnd(pluginCtx, agg, converted); hookErr != nil {
				p.logger.Warn("post-commit trial end hook failed",
					"hook", hook.Name(),
					"contractID", agg.ContractID(),
					"error", hookErr,
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
			if hookErr := hook.OnContractChange(pluginCtx, changeEvent); hookErr != nil {
				p.logger.Warn("post-commit change hook failed",
					"hook", hook.Name(),
					"contractID", agg.ContractID(),
					"error", hookErr,
				)
			}
		}
	}

	return nil
}
