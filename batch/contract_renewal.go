package batch

import (
	"context"
	"fmt"
	"sync"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// ContractRenewalProcessor processes contract renewals for contracts whose
// billing period has ended.
type ContractRenewalProcessor struct {
	contractRepo contract.Repository
	registry     *plugin.Registry
	clock        shared.Clock
}

// NewContractRenewalProcessor creates a new ContractRenewalProcessor.
func NewContractRenewalProcessor(
	contractRepo contract.Repository,
	registry *plugin.Registry,
	clock shared.Clock,
) *ContractRenewalProcessor {
	return &ContractRenewalProcessor{
		contractRepo: contractRepo,
		registry:     registry,
		clock:        clock,
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
	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex

	for _, agg := range contracts {
		sem <- struct{}{}
		go func(a *contract.ContractAggregate) {
			defer func() { <-sem }()

			if err := p.processOne(ctx, a, opts.DryRun); err != nil {
				mu.Lock()
				result.Failed++
				result.Errors = append(result.Errors, fmt.Errorf("contract %s: %w", a.ContractID(), err))
				mu.Unlock()
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
		// In dry run mode, just validate that renewal would succeed
		// by checking the guards without actually applying
		if agg.Status() != contract.ContractStatusActive {
			return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
				fmt.Sprintf("cannot renew: status is %s", agg.Status()))
		}
		return nil
	}

	if err := agg.Renew(metadata); err != nil {
		return err
	}

	// Fire OnContractRenewHooks
	if p.registry != nil {
		pluginCtx := plugin.NewContext(ctx)
		for _, hook := range p.registry.GetOnContractRenewHooks() {
			if err := hook.OnContractRenew(pluginCtx, agg); err != nil {
				return fmt.Errorf("renew hook %q failed: %w", hook.Name(), err)
			}
		}

		// Fire OnContractChangeHooks with ContractChangeRenewed
		activeStatus := contract.ContractStatusActive
		changeEvent := plugin.ContractChangeEvent{
			ContractID: agg.ContractID(),
			ChangeType: plugin.ContractChangeRenewed,
			OldStatus:  &activeStatus,
			NewStatus:  &activeStatus,
			Timestamp:  p.clock.Now(),
		}
		for _, hook := range p.registry.GetOnContractChangeHooks() {
			if err := hook.OnContractChange(pluginCtx, changeEvent); err != nil {
				return fmt.Errorf("change hook %q failed: %w", hook.Name(), err)
			}
		}
	}

	if err := p.contractRepo.Save(ctx, agg); err != nil {
		return fmt.Errorf("failed to save renewed contract: %w", err)
	}

	return nil
}
