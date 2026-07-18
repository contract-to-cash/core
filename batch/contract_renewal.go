package batch

import (
	"context"
	"errors"
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

// Dry-run action labels reported by ContractRenewalProcessor in
// BatchResult.DryRunActions (issue #242). They mirror the branches of
// ContractAggregate.RenewWithInterval, which the real run calls.
const (
	// RenewalActionRenew: the contract would renew into the next period.
	RenewalActionRenew = "renew"
	// RenewalActionExpire: autoRenew=false — the contract would expire at the
	// period boundary (a real run reports ContractChangeExpired).
	RenewalActionExpire = "expire"
	// RenewalActionCancel: a scheduled cancellation (cancelAtPeriodEnd) would
	// resolve to Cancelled at the period boundary (a real run reports
	// ContractChangeCancelled).
	RenewalActionCancel = "cancel"
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
// If txManager is nil, a default NoopTxManager is used and a Warn-level log is
// emitted (renewal writes will NOT be atomic). Pass a real TxManager for
// production, or tx.NewNoopTxManagerExplicit(...) to acknowledge intentional
// non-atomic in-memory/test use and suppress the warning.
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
	tx.WarnIfDefaultNoop(logger, txManager, "ContractRenewalProcessor", "pass a real TxManager to NewContractRenewalProcessor (or tx.NewNoopTxManagerExplicit(...) to acknowledge non-atomic in-memory use)")
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
	contracts, err := p.contractRepo.FindDueForRenewal(ctx, now, opts.Limit)
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
			// External cancellation aborts the run: the remainder is skipped
			// and the cancellation is surfaced as the run's error, so a
			// cancelled run is never mistaken for a clean one (issue #242).
			if ctxErr := ctx.Err(); ctxErr != nil {
				result.Skipped = result.Total - result.Succeeded - result.Failed
				return result, ctxErr
			}
			if p.skipNoInterval(agg) {
				result.Skipped++
				continue
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
				if opts.DryRun {
					result.DryRunActions = append(result.DryRunActions, DryRunAction{
						ItemID: string(agg.ContractID()),
						Action: dryRunRenewalAction(agg),
					})
				}
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

		if p.skipNoInterval(agg) {
			mu.Lock()
			result.Skipped++
			mu.Unlock()
			continue
		}

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

// skipNoInterval reports whether the contract must be skipped because it has
// no billing interval (a zero-interval one_time contract, issue #218) and thus
// no billing period to renew. Repository implementations are expected to
// exclude such contracts from FindDueForRenewal (their period end is the zero
// time), but a custom DB adapter may not replicate that guard — this is
// defense-in-depth so a mis-selected contract is counted as Skipped with a
// Warn log rather than Failed with a confusing renewal error.
func (p *ContractRenewalProcessor) skipNoInterval(agg *contract.ContractAggregate) bool {
	if !agg.GetInterval().IsZero() {
		return false
	}
	p.logger.Warn("skipping contract with no billing interval (nothing to renew)",
		"contractID", agg.ContractID(),
		"contractType", agg.GetContractType(),
	)
	return true
}

// dryRunRenewalAction classifies the action a real run would take for a
// contract that passed the dry-run guards, in the same order
// RenewWithInterval branches (issue #242).
func dryRunRenewalAction(agg *contract.ContractAggregate) string {
	switch {
	case agg.CancelAtPeriodEnd():
		return RenewalActionCancel
	case !agg.AutoRenew():
		return RenewalActionExpire
	default:
		return RenewalActionRenew
	}
}

// processOne validates and (for real runs) executes the renewal of a single
// contract. For dry runs it only validates the guards; the would-be action is
// classified separately by dryRunRenewalAction in the sequential dry-run path.
func (p *ContractRenewalProcessor) processOne(ctx context.Context, agg *contract.ContractAggregate, dryRun bool) error {
	metadata := eventstore.EventMetadata{
		UserID: "system:batch:contract_renewal",
	}

	if dryRun {
		// In dry run mode, validate all guards without applying side effects,
		// mirroring the real run's branching (issue #242): cancelAtPeriodEnd
		// and autoRenew=false contracts are processed SUCCESSFULLY by the real
		// run (RenewWithInterval resolves them to Cancelled / Expired and the
		// post-commit hooks fire), so the dry run treats them as would-succeed
		// (Process reports the action via dryRunRenewalAction) instead of
		// reporting them as failures.
		if agg.Status() != contract.ContractStatusActive {
			return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
				fmt.Sprintf("cannot renew: status is %s", agg.Status()))
		}
		// Validate the same interval resolution the real run performs. The
		// real run resolves the interval BEFORE RenewWithInterval branches, so
		// a contract with a dangling PendingPriceID (a scheduled price change
		// whose Price cannot be loaded) fails every real-run path — including
		// the expire/cancel branches — and must fail the dry run the same way
		// instead of passing it and then blowing up in production (issue #162 B2).
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
		renewed    *contract.ContractAggregate
		oldStatus  contract.ContractStatus
		newStatus  contract.ContractStatus
		oldPriceID shared.PriceID
		newPriceID shared.PriceID
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
		oldPriceID = loaded.PriceID()
		if renewErr := loaded.RenewWithInterval(interval, metadata); renewErr != nil {
			return renewErr
		}
		newStatus = loaded.Status()
		newPriceID = loaded.PriceID()

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
			// contract reached its term with autoRenew=false; Cancelled covers a
			// scheduled cancellation (cancelAtPeriodEnd), which RenewWithInterval
			// resolves to Cancelled at the period boundary — user-initiated churn,
			// not natural expiry.
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
				if hookErr := plugin.SafeInvoke("OnContractChangeHook.OnContractChange", hook.Name(), func() error {
					return hook.OnContractChange(pluginCtx, changeEvent)
				}); hookErr != nil {
					plugin.LogNonFatalHookError(p.logger, "post-commit change hook failed", hookErr,
						"hook", hook.Name(),
						"contractID", agg.ContractID(),
					)
				}
			}
		} else {
			for _, hook := range p.registry.GetOnContractRenewHooks() {
				if hookErr := plugin.SafeInvoke("OnContractRenewHook.OnContractRenew", hook.Name(), func() error {
					return hook.OnContractRenew(pluginCtx, agg)
				}); hookErr != nil {
					plugin.LogNonFatalHookError(p.logger, "post-commit renew hook failed", hookErr,
						"hook", hook.Name(),
						"contractID", agg.ContractID(),
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
			// A renewal that applied a pending price change reports the price
			// transition so metrics plugins can attribute the change without
			// re-loading the contract (issue #242). MRRChange stays nil: the
			// processor never loads the OLD Price entity, so the amounts are
			// not cheaply available here.
			if oldPriceID != newPriceID {
				changeEvent.OldPriceID = &oldPriceID
				changeEvent.NewPriceID = &newPriceID
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
