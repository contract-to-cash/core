package batch

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
)

var expirationNow = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

func newExpirationClock() shared.Clock {
	return shared.FixedClock{FixedTime: expirationNow}
}

// saveBalanceEntry builds a credit entry and persists it.
func saveBalanceEntry(t *testing.T, repo balance.Repository, amount int64, expiresAt *time.Time, consume int64) *balance.BalanceEntry {
	t.Helper()
	entry, err := balance.NewBalanceEntry(
		shared.NewAccountID(),
		shared.NewMoney(big.NewRat(amount, 1), shared.CurrencyJPY),
		balance.BalanceReasonGoodwill,
		expirationNow.Add(-30*24*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewBalanceEntry: %v", err)
	}
	entry.SetExpiresAt(expiresAt)
	if consume > 0 {
		if _, err := entry.Consume(shared.NewMoney(big.NewRat(consume, 1), shared.CurrencyJPY)); err != nil {
			t.Fatalf("Consume: %v", err)
		}
	}
	if err := repo.Save(context.Background(), entry); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return entry
}

func pastExpiry() *time.Time {
	tm := expirationNow.Add(-24 * time.Hour)
	return &tm
}

func futureExpiry() *time.Time {
	tm := expirationNow.Add(24 * time.Hour)
	return &tm
}

func TestBalanceExpiration_ForfeitsExpiredEntries(t *testing.T) {
	repo := inmemory.NewInMemoryBalanceRepository(newExpirationClock())
	expired := saveBalanceEntry(t, repo, 1000, pastExpiry(), 0)
	partiallyConsumed := saveBalanceEntry(t, repo, 500, pastExpiry(), 200)

	p := NewBalanceExpirationProcessor(repo, newExpirationClock(), nil, nil)
	result, err := p.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Total != 2 || result.Succeeded != 2 || result.Failed != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}

	for _, id := range []shared.BalanceEntryID{expired.ID(), partiallyConsumed.ID()} {
		got, err := repo.FindByID(context.Background(), id)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if !got.RemainingAmount().IsZero() {
			t.Errorf("entry %s: expected zero remaining after batch, got %s",
				id, got.RemainingAmount().Amount().RatString())
		}
		if !got.IsFullyConsumed() {
			t.Errorf("entry %s: expected IsFullyConsumed after batch", id)
		}
	}

	// Re-running the batch is a clean no-op: forfeited entries are fully
	// consumed and no longer returned by FindExpired.
	again, err := p.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("second Process: %v", err)
	}
	if again.Total != 0 {
		t.Errorf("expected no candidates on re-run, got %d", again.Total)
	}
}

func TestBalanceExpiration_FiltersIneligibleEntries(t *testing.T) {
	repo := inmemory.NewInMemoryBalanceRepository(newExpirationClock())
	saveBalanceEntry(t, repo, 1000, futureExpiry(), 0) // not yet expired
	saveBalanceEntry(t, repo, 1000, nil, 0)            // no expiry at all
	saveBalanceEntry(t, repo, 500, pastExpiry(), 500)  // expired but already fully consumed
	eligible := saveBalanceEntry(t, repo, 700, pastExpiry(), 0)

	p := NewBalanceExpirationProcessor(repo, newExpirationClock(), nil, nil)
	result, err := p.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Total != 1 || result.Succeeded != 1 {
		t.Fatalf("expected exactly the one eligible entry to be processed, got %+v", result)
	}

	got, err := repo.FindByID(context.Background(), eligible.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !got.RemainingAmount().IsZero() {
		t.Errorf("eligible entry must be forfeited, remaining %s", got.RemainingAmount().Amount().RatString())
	}
}

func TestBalanceExpiration_DryRun_NoSideEffects(t *testing.T) {
	repo := inmemory.NewInMemoryBalanceRepository(newExpirationClock())
	entry := saveBalanceEntry(t, repo, 1000, pastExpiry(), 0)

	p := NewBalanceExpirationProcessor(repo, newExpirationClock(), nil, nil)
	result, err := p.Process(context.Background(), BatchOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Total != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}

	got, err := repo.FindByID(context.Background(), entry.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.RemainingAmount().IsZero() {
		t.Error("dry run must not forfeit the remaining amount")
	}
	if got.Version() != entry.Version() {
		t.Errorf("dry run must not bump the version: was %d, got %d", entry.Version(), got.Version())
	}
}

// failingBalanceRepo wraps a balance.Repository and fails FindByID for one
// specific entry, to exercise ContinueOnError.
type failingBalanceRepo struct {
	balance.Repository
	failID shared.BalanceEntryID
}

var errInjected = errors.New("injected load failure")

func (f *failingBalanceRepo) FindByID(ctx context.Context, id shared.BalanceEntryID) (*balance.BalanceEntry, error) {
	if id == f.failID {
		return nil, errInjected
	}
	return f.Repository.FindByID(ctx, id)
}

func TestBalanceExpiration_ContinueOnError(t *testing.T) {
	inner := inmemory.NewInMemoryBalanceRepository(newExpirationClock())
	// Creation times differ (FIFO order): make the failing entry the oldest so
	// it is processed first and the ContinueOnError flag decides whether the
	// second entry is still processed.
	failing, err := balance.NewBalanceEntry(
		shared.NewAccountID(),
		shared.NewMoney(big.NewRat(300, 1), shared.CurrencyJPY),
		balance.BalanceReasonGoodwill,
		expirationNow.Add(-60*24*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewBalanceEntry: %v", err)
	}
	failing.SetExpiresAt(pastExpiry())
	if err := inner.Save(context.Background(), failing); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ok := saveBalanceEntry(t, inner, 800, pastExpiry(), 0)

	repo := &failingBalanceRepo{Repository: inner, failID: failing.ID()}

	t.Run("ContinueOnError=true processes the rest", func(t *testing.T) {
		p := NewBalanceExpirationProcessor(repo, newExpirationClock(), nil, nil)
		result, err := p.Process(context.Background(), BatchOptions{ContinueOnError: true})
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if result.Total != 2 || result.Succeeded != 1 || result.Failed != 1 {
			t.Fatalf("unexpected result: %+v", result)
		}
		if len(result.Errors) != 1 || !errors.Is(result.Errors[0], errInjected) {
			t.Errorf("expected the injected error to be reported, got %v", result.Errors)
		}
		got, err := inner.FindByID(context.Background(), ok.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if !got.RemainingAmount().IsZero() {
			t.Error("healthy entry must still be forfeited when ContinueOnError=true")
		}
	})

	t.Run("ContinueOnError=false stops at the first failure", func(t *testing.T) {
		inner2 := inmemory.NewInMemoryBalanceRepository(newExpirationClock())
		failing2, err := balance.NewBalanceEntry(
			shared.NewAccountID(),
			shared.NewMoney(big.NewRat(300, 1), shared.CurrencyJPY),
			balance.BalanceReasonGoodwill,
			expirationNow.Add(-60*24*time.Hour),
		)
		if err != nil {
			t.Fatalf("NewBalanceEntry: %v", err)
		}
		failing2.SetExpiresAt(pastExpiry())
		if err := inner2.Save(context.Background(), failing2); err != nil {
			t.Fatalf("Save: %v", err)
		}
		ok2 := saveBalanceEntry(t, inner2, 800, pastExpiry(), 0)
		repo2 := &failingBalanceRepo{Repository: inner2, failID: failing2.ID()}

		p := NewBalanceExpirationProcessor(repo2, newExpirationClock(), nil, nil)
		result, err := p.Process(context.Background(), BatchOptions{ContinueOnError: false})
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if result.Failed != 1 || result.Succeeded != 0 {
			t.Fatalf("expected stop after first failure, got %+v", result)
		}
		got, err := inner2.FindByID(context.Background(), ok2.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if got.RemainingAmount().IsZero() {
			t.Error("second entry must NOT be processed when ContinueOnError=false")
		}
	})
}

func TestBalanceExpiration_ConcurrentProcessing(t *testing.T) {
	repo := inmemory.NewInMemoryBalanceRepository(newExpirationClock())
	var ids []shared.BalanceEntryID
	for i := 0; i < 10; i++ {
		e := saveBalanceEntry(t, repo, int64(100*(i+1)), pastExpiry(), 0)
		ids = append(ids, e.ID())
	}

	p := NewBalanceExpirationProcessor(repo, newExpirationClock(), nil, nil)
	result, err := p.Process(context.Background(), BatchOptions{Concurrency: 4, ContinueOnError: true})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Total != 10 || result.Succeeded != 10 || result.Failed != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	for _, id := range ids {
		got, err := repo.FindByID(context.Background(), id)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if !got.RemainingAmount().IsZero() {
			t.Errorf("entry %s not forfeited", id)
		}
	}
}

func TestBalanceExpiration_ConsumedBetweenScanAndTx_IsNoOpSuccess(t *testing.T) {
	// A credit fully consumed between the scan and the per-entry transaction
	// (e.g. the billing pipeline applied it concurrently) leaves nothing to
	// forfeit: the processor treats it as an idempotent success and does not
	// save.
	repo := inmemory.NewInMemoryBalanceRepository(newExpirationClock())
	entry := saveBalanceEntry(t, repo, 1000, pastExpiry(), 0)

	// Simulate the concurrent consumption AFTER the scan by consuming through
	// a wrapper that intercepts the processor's in-tx FindByID.
	consumed := false
	wrapped := &consumeOnFirstLoadRepo{Repository: repo, target: entry.ID(), consumed: &consumed}

	p := NewBalanceExpirationProcessor(wrapped, newExpirationClock(), nil, nil)
	result, err := p.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Total != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// consumeOnFirstLoadRepo consumes the target entry's full remaining amount on
// the processor's in-tx FindByID, simulating a billing-pipeline Consume that
// committed between the scan and the expiration transaction.
type consumeOnFirstLoadRepo struct {
	balance.Repository
	target   shared.BalanceEntryID
	consumed *bool
}

func (c *consumeOnFirstLoadRepo) FindByID(ctx context.Context, id shared.BalanceEntryID) (*balance.BalanceEntry, error) {
	if id == c.target && !*c.consumed {
		*c.consumed = true
		loaded, err := c.Repository.FindByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if _, err := loaded.Consume(loaded.RemainingAmount()); err != nil {
			return nil, err
		}
		if err := c.Repository.Save(ctx, loaded); err != nil {
			return nil, err
		}
	}
	return c.Repository.FindByID(ctx, id)
}
