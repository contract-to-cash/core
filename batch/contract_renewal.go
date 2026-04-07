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
		return nil
	}

	// Resolve the billing interval for the next period.
	// If there is a pending price change, load the new Price to get its interval.
	interval, err := p.resolveInterval(ctx, agg)
	if err != nil {
		return err
	}

	oldStatus := agg.Status()
	if err := agg.RenewWithInterval(interval, metadata); err != nil {
		return err
	}
	newStatus := agg.Status()

	// Persist within transaction — save BEFORE hooks to prevent
	// "notified but not persisted" inconsistency.
	err = p.txManager.RunInTx(ctx, func(txCtx context.Context, repos tx.Repos) error {
		return repos.Contracts.Save(txCtx, agg)
	})
	if err != nil {
		return fmt.Errorf("failed to save renewed contract: %w", err)
	}

	// Post-commit hooks — non-fatal. Contract is already persisted;
	// hook failures are logged but do not fail the renewal.
	if p.registry != nil {
		pluginCtx := plugin.NewContext(ctx)

		if newStatus == contract.ContractStatusExpired || newStatus == contract.ContractStatusCancelled {
			changeEvent := plugin.ContractChangeEvent{
				ContractID: agg.ContractID(),
				ChangeType: plugin.ContractChangeCancelled,
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
