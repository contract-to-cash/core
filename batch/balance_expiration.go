package batch

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/shared"
)

// BalanceExpirationProcessor forfeits expired credit-ledger entries
// (issue #159).
//
// BalanceEntry supports an expiry (SetExpiresAt / IsExpired) and
// balance.Repository.FindAvailable already filters expired entries out of the
// billing pipeline, but nothing transitioned them: expired credit kept a
// live-looking non-zero remainingAmount forever (visible through GetBalance
// on some adapters, FindByAccountID, and reporting built on the raw rows).
// This processor finds expired, not-yet-forfeited entries via
// Repository.FindExpired and zeroes their remaining amount via
// BalanceEntry.MarkExpired inside a transaction.
//
// Like the other batch processors, scheduling is the consumer's concern
// (design-decisions.md section 3.2) — run Process from your cron / CronJob /
// Cloud Scheduler.
type BalanceExpirationProcessor struct {
	balanceRepo balance.Repository
	clock       shared.Clock
	txManager   tx.TxManager
	logger      *slog.Logger
}

var _ BatchProcessor = (*BalanceExpirationProcessor)(nil)

// NewBalanceExpirationProcessor creates a new BalanceExpirationProcessor.
// If txManager is nil, a default NoopTxManager is used and a Warn-level log is
// emitted (balance-forfeiture writes will NOT be atomic). Pass a real TxManager
// for production, or tx.NewNoopTxManagerExplicit(...) to acknowledge intentional
// non-atomic in-memory/test use and suppress the warning.
func NewBalanceExpirationProcessor(
	balanceRepo balance.Repository,
	clock shared.Clock,
	txManager tx.TxManager,
	logger *slog.Logger,
) *BalanceExpirationProcessor {
	if txManager == nil {
		txManager = tx.NewNoopTxManager(tx.Repos{
			Balances: balanceRepo,
		})
	}
	if logger == nil {
		logger = slog.Default()
	}
	tx.WarnIfDefaultNoop(logger, txManager, "BalanceExpirationProcessor", "pass a real TxManager to NewBalanceExpirationProcessor (or tx.NewNoopTxManagerExplicit(...) to acknowledge non-atomic in-memory use)")
	return &BalanceExpirationProcessor{
		balanceRepo: balanceRepo,
		clock:       clock,
		txManager:   txManager,
		logger:      logger,
	}
}

// Process finds expired, not-yet-forfeited balance entries and forfeits their
// remaining amount.
func (p *BalanceExpirationProcessor) Process(ctx context.Context, opts BatchOptions) (*BatchResult, error) {
	now := p.clock.Now()
	entries, err := p.balanceRepo.FindExpired(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("failed to find expired balance entries: %w", err)
	}

	result := &BatchResult{
		Total: len(entries),
	}

	if len(entries) == 0 {
		return result, nil
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	if concurrency == 1 || opts.DryRun {
		// Sequential processing
		for _, entry := range entries {
			if err := p.processOne(ctx, entry, opts.DryRun); err != nil {
				result.Failed++
				result.Errors = append(result.Errors, fmt.Errorf("balance entry %s: %w", entry.ID(), err))
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

	for _, entry := range entries {
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
		go func(e *balance.BalanceEntry) {
			defer func() { <-sem }()

			if err := p.processOne(cctx, e, opts.DryRun); err != nil {
				mu.Lock()
				result.Failed++
				result.Errors = append(result.Errors, fmt.Errorf("balance entry %s: %w", e.ID(), err))
				mu.Unlock()
				if !opts.ContinueOnError {
					cancel()
				}
			} else {
				mu.Lock()
				result.Succeeded++
				mu.Unlock()
			}
		}(entry)
	}

	// Wait for all goroutines to finish
	for i := 0; i < concurrency; i++ {
		sem <- struct{}{}
	}

	return result, nil
}

func (p *BalanceExpirationProcessor) processOne(ctx context.Context, entry *balance.BalanceEntry, dryRun bool) error {
	now := p.clock.Now()

	// Guards — validated for both dry-run and real runs.
	if !entry.IsExpired(now) {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			"balance entry has not expired yet")
	}
	if entry.IsFullyConsumed() {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			"balance entry is already fully consumed")
	}

	if dryRun {
		// All guards passed; report what would happen without side effects.
		return nil
	}

	// Load-mutate-save inside the transaction against a repository-loaded
	// instance (issue #151). MarkExpired and Save run inside the tx boundary
	// so a failed Save never leaves a half-forfeited entry behind, and the
	// reload closes the window against a concurrent Consume (the billing
	// pipeline applying this credit): a Consume committed between the scan
	// and this tx either leaves nothing to forfeit (skipped as a no-op) or
	// bumps the stored version so this Save loses with
	// tx.ErrVersionConflict under a compliant repository.
	if err := p.txManager.RunInTx(ctx, func(txCtx context.Context, repos tx.Repos) error {
		balanceRepo := repos.Balances
		if balanceRepo == nil {
			balanceRepo = p.balanceRepo
		}
		loaded, findErr := balanceRepo.FindByID(txCtx, entry.ID())
		if findErr != nil {
			return fmt.Errorf("failed to load balance entry for expiration: %w", findErr)
		}

		forfeited, expireErr := loaded.MarkExpired(now)
		if expireErr != nil {
			return expireErr
		}
		if forfeited.IsZero() {
			// Fully consumed between the scan and this tx — nothing to
			// forfeit, nothing to save. Idempotent success.
			return nil
		}

		if saveErr := balanceRepo.Save(txCtx, loaded); saveErr != nil {
			return saveErr
		}

		p.logger.Info("expired balance entry forfeited",
			"balanceEntryID", loaded.ID(),
			"accountID", loaded.AccountID(),
			"forfeitedAmount", forfeited.Amount().RatString(),
			"currency", forfeited.Currency(),
		)
		return nil
	}); err != nil {
		return err
	}

	return nil
}
