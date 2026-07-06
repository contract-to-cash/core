package batch

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// ContractRenewalProcessor processes contract renewals for contracts whose
// billing period has ended.
type ContractRenewalProcessor struct {
	contractRepo contract.Repository
	priceRepo    pricing.PriceRepository
	registry     *plugin.Registry
	clock        shared.Clock
	txManager    tx.TxManager
	logger       *slog.Logger
}

// NewContractRenewalProcessor creates a new ContractRenewalProcessor.
// If txManager is nil, a NoopTxManager is used.
func NewContractRenewalProcessor(
	contractRepo contract.Repository,
	priceRepo pricing.PriceRepository,
	registry *plugin.Registry,
	clock shared.Clock,
	txManager tx.TxManager,
	logger *slog.Logger,
) *ContractRenewalProcessor {
	if txManager == nil {
		txManager = tx.NewNoopTxManager(tx.Repos{
			Contracts: contractRepo,
		})
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ContractRenewalProcessor{
		contractRepo: contractRepo,
		priceRepo:    priceRepo,
		registry:     registry,
		clock:        clock,
		txManager:    txManager,
		logger:       logger,
	}
}

// Process finds active contracts due for renewal and renews them.
func (p *ContractRenewalProcessor) Process(ctx context.Context, opts BatchOptions) (*BatchResult, error) {
	now := p.clock.Now()
	contracts, err := p.contractRepo.FindDueForRenewal(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("failed to find contracts due for renewal: %w", err)
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

func (p *ContractRenewalProcessor) processOne(ctx context.Context, agg *contract.ContractAggregate, dryRun bool) error {
	metadata := eventstore.EventMetadata{
		UserID: "system:batch:contract_renewal",
	}

	if dryRun {
		// In dry run mode, validate all guards without applying side effects.
		if agg.Status() != contract.ContractStatusActive {
			return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
				fmt.Sprintf("cannot renew: status is %s", agg.Status()))
		}
		// Report that this contract would expire rather than renew.
		if agg.CancelAtPeriodEnd() || !agg.AutoRenew() {
			return shared.NewDomainError(shared.ErrCodeBusinessRule,
				"contract would expire at period end (cancelAtPeriodEnd or autoRenew=false)")
		}
		// Validate the same interval resolution the real run performs so a
		// contract with a dangling PendingPriceID (a scheduled price change whose
		// Price cannot be loaded) fails the dry run instead of passing it and
		// then blowing up in production (issue #162 B2).
		if _, err := p.resolveInterval(ctx, agg); err != nil {
			return err
		}
		return nil
	}

	// Load-mutate-save inside the transaction against a repository-loaded
	// instance (issue #151). Resolving the interval, RenewWithInterval, and Save
	// all run inside the tx boundary so a failed Save never leaves a half-renewed
	// aggregate — carrying dangling uncommitted events and an advanced
	// period/status — behind for the next batch run. On a real adapter FindByID
	// materializes a fresh aggregate from history, so the instance returned by
	// FindDueForRenewal is never mutated when the renewal fails to persist. Save
	// runs BEFORE hooks to prevent a "notified but not persisted" inconsistency.
	var (
		renewed   *contract.ContractAggregate
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
			return fmt.Errorf("failed to load contract for renewal: %w", findErr)
		}

		interval, resolveErr := p.resolveInterval(txCtx, loaded)
		if resolveErr != nil {
			return resolveErr
		}

		oldStatus = loaded.Status()
		if renewErr := loaded.RenewWithInterval(interval, metadata); renewErr != nil {
			return renewErr
		}
		newStatus = loaded.Status()

		if saveErr := contractRepo.Save(txCtx, loaded); saveErr != nil {
			return saveErr
		}
		renewed = loaded
		return nil
	}); err != nil {
		return err
	}

	// Subsequent post-commit hooks operate on the persisted,
	// repository-loaded aggregate.
	agg = renewed

	// Post-commit hooks — non-fatal. Contract is already persisted;
	// hook failures are logged but do not fail the renewal.
	if p.registry != nil {
		pluginCtx := plugin.NewContext(ctx)

		if newStatus == contract.ContractStatusExpired || newStatus == contract.ContractStatusCancelled {
			// Distinguish natural term-end expiry from a deliberate cancellation
			// so churn metrics stay accurate (issue #162 B3). Expired means the
			// contract reached its term with autoRenew=false / cancelAtPeriodEnd;
			// Cancelled means it was cancelled outright.
			changeType := plugin.ContractChangeExpired
			if newStatus == contract.ContractStatusCancelled {
				changeType = plugin.ContractChangeCancelled
			}
			changeEvent := plugin.ContractChangeEvent{
				ContractID: agg.ContractID(),
				ChangeType: changeType,
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
		} else {
			for _, hook := range p.registry.GetOnContractRenewHooks() {
				if hookErr := hook.OnContractRenew(pluginCtx, agg); hookErr != nil {
					p.logger.Warn("post-commit renew hook failed",
						"hook", hook.Name(),
						"contractID", agg.ContractID(),
						"error", hookErr,
					)
				}
			}

			changeEvent := plugin.ContractChangeEvent{
				ContractID: agg.ContractID(),
				ChangeType: plugin.ContractChangeRenewed,
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
	}

	return nil
}

// resolveInterval determines the billing interval for the next period.
// If a pending price change exists and a PriceRepository is available,
// it loads the new Price to get its interval. Otherwise, it falls back
// to the contract's current interval.
func (p *ContractRenewalProcessor) resolveInterval(ctx context.Context, agg *contract.ContractAggregate) (pricing.BillingInterval, error) {
	if agg.PendingPriceID() != nil && p.priceRepo != nil {
		price, err := p.priceRepo.FindByID(ctx, *agg.PendingPriceID())
		if err != nil {
			return pricing.BillingInterval{}, fmt.Errorf("failed to load pending price %s: %w", *agg.PendingPriceID(), err)
		}
		return price.Interval(), nil
	}
	return agg.GetInterval(), nil
}
