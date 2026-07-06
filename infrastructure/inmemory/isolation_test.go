package inmemory

import (
	"context"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// These tests pin down the isolation contract added for issue #152: the
// in-memory repositories must hand every reader an independent copy so that
// (a) concurrent load-modify does not race on shared aggregate/entity state,
// (b) the optimistic lock is observable through the raw repository, and
// (c) an unsaved mutation on one loaded copy is invisible to other readers.

// --- Invoice ---

func TestInMemoryInvoiceRepository_FindByIDReturnsIsolatedCopy(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	inv := newTestInvoice(t, shared.NewAccountID(), shared.NewContractID())
	if err := repo.Save(ctx, inv); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	a, err := repo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID(a) failed: %v", err)
	}
	b, err := repo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID(b) failed: %v", err)
	}
	if a == b {
		t.Fatal("FindByID returned the same pointer twice; reads are not isolated")
	}

	// Mutate a WITHOUT saving. b and a fresh read must not observe it (no dirty read).
	if err := a.Finalize(); err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}
	if b.Status() != invoice.InvoiceStatusDraft {
		t.Errorf("sibling read saw uncommitted mutation: status=%s", b.Status())
	}
	c, err := repo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID(c) failed: %v", err)
	}
	if c.Status() != invoice.InvoiceStatusDraft {
		t.Errorf("repository polluted by unsaved mutation: status=%s", c.Status())
	}
}

// Two independent loads that both mutate: the second Save must lose the
// optimistic-lock race. Before #152 the two loads aliased one pointer, so the
// version check could never fire through the raw repo.
func TestInMemoryInvoiceRepository_OptimisticLockThroughRawRepo(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	inv := newTestInvoice(t, shared.NewAccountID(), shared.NewContractID())
	if err := repo.Save(ctx, inv); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	a, err := repo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID(a) failed: %v", err)
	}
	b, err := repo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID(b) failed: %v", err)
	}

	if err := a.Finalize(); err != nil {
		t.Fatalf("a.Finalize failed: %v", err)
	}
	if err := repo.Save(ctx, a); err != nil {
		t.Fatalf("Save(a) should win, got %v", err)
	}

	if err := b.Finalize(); err != nil {
		t.Fatalf("b.Finalize failed: %v", err)
	}
	err = repo.Save(ctx, b)
	if !tx.IsVersionConflict(err) {
		t.Fatalf("Save(b) should lose with a version conflict, got %v", err)
	}
}

// -race guard: many goroutines load the same invoice and mutate their own
// isolated copy concurrently. If FindByID aliased the stored pointer this would
// be a write-write data race on the invoice internals.
func TestInMemoryInvoiceRepository_ConcurrentLoadModifyNoRace(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	inv := newTestInvoice(t, shared.NewAccountID(), shared.NewContractID())
	if err := repo.Save(ctx, inv); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	id := inv.ID()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				loaded, err := repo.FindByID(ctx, id)
				if err != nil {
					t.Errorf("FindByID failed: %v", err)
					return
				}
				// Never saved, so the stored copy stays draft and every load is
				// a fresh draft clone that Finalize mutates in place.
				_ = loaded.Finalize()
			}
		}()
	}
	wg.Wait()
}

// --- Contract (event-sourced) ---

func TestInMemoryContractRepository_FindByIDReturnsIsolatedCopy(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	store := NewInMemoryEventStore(clock)
	repo := NewInMemoryContractRepository(store, clock)
	ctx := context.Background()

	agg := newTestContractAggregate(t, clock, shared.NewAccountID())
	if err := repo.Save(ctx, agg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	a, err := repo.FindByID(ctx, agg.ContractID())
	if err != nil {
		t.Fatalf("FindByID(a) failed: %v", err)
	}
	b, err := repo.FindByID(ctx, agg.ContractID())
	if err != nil {
		t.Fatalf("FindByID(b) failed: %v", err)
	}
	if a == b {
		t.Fatal("FindByID returned the same aggregate pointer twice; reads are not isolated")
	}

	// Activate a (raises + applies an event) WITHOUT saving; b must not see it,
	// and neither must the repository.
	if err := a.Activate(eventstore.EventMetadata{UserID: "u"}); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	if b.Status() != contract.ContractStatusDraft {
		t.Errorf("sibling read saw uncommitted mutation: status=%s", b.Status())
	}
	c, err := repo.FindByID(ctx, agg.ContractID())
	if err != nil {
		t.Fatalf("FindByID(c) failed: %v", err)
	}
	if c.Status() != contract.ContractStatusDraft {
		t.Errorf("repository polluted by unsaved mutation: status=%s", c.Status())
	}
}

// The event store's version check must surface through the raw contract repo:
// two independent loads that both append must have the second Save rejected
// with the version_conflict DomainError (recognised by tx.IsVersionConflict).
func TestInMemoryContractRepository_OptimisticLockThroughRawRepo(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	store := NewInMemoryEventStore(clock)
	repo := NewInMemoryContractRepository(store, clock)
	ctx := context.Background()

	agg := newTestContractAggregate(t, clock, shared.NewAccountID())
	if err := repo.Save(ctx, agg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	id := agg.ContractID()

	a, err := repo.FindByID(ctx, id)
	if err != nil {
		t.Fatalf("FindByID(a) failed: %v", err)
	}
	b, err := repo.FindByID(ctx, id)
	if err != nil {
		t.Fatalf("FindByID(b) failed: %v", err)
	}

	pm := "pm-a"
	if err := a.ChangePaymentMethod(&pm, eventstore.EventMetadata{UserID: "u"}); err != nil {
		t.Fatalf("a.ChangePaymentMethod failed: %v", err)
	}
	if err := repo.Save(ctx, a); err != nil {
		t.Fatalf("Save(a) should win, got %v", err)
	}

	pm2 := "pm-b"
	if err := b.ChangePaymentMethod(&pm2, eventstore.EventMetadata{UserID: "u"}); err != nil {
		t.Fatalf("b.ChangePaymentMethod failed: %v", err)
	}
	err = repo.Save(ctx, b)
	if !tx.IsVersionConflict(err) {
		t.Fatalf("Save(b) should lose with a version conflict, got %v", err)
	}
}

// A realistic read-modify-save-with-retry loop over the raw contract repo: a
// concurrent writer advances the stream between our load and our save on the
// first attempt, so the first Save conflicts; RetryOnConflict must recognise the
// event store's DomainError and re-run the closure to success.
func TestInMemoryContractRepository_RetryOnConflictRealConflict(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	store := NewInMemoryEventStore(clock)
	repo := NewInMemoryContractRepository(store, clock)
	ctx := context.Background()

	agg := newTestContractAggregate(t, clock, shared.NewAccountID())
	if err := repo.Save(ctx, agg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	id := agg.ContractID()

	raced := false
	attempts := 0
	err := tx.RetryOnConflict(3, func() error {
		attempts++
		loaded, err := repo.FindByID(ctx, id)
		if err != nil {
			return err
		}
		if !raced {
			// Simulate a concurrent writer that commits an event on the same
			// stream, invalidating `loaded`'s version exactly once.
			raced = true
			other, err := repo.FindByID(ctx, id)
			if err != nil {
				return err
			}
			pm := "concurrent"
			if err := other.ChangePaymentMethod(&pm, eventstore.EventMetadata{UserID: "u"}); err != nil {
				return err
			}
			if err := repo.Save(ctx, other); err != nil {
				return err
			}
		}
		pm := "mine"
		if err := loaded.ChangePaymentMethod(&pm, eventstore.EventMetadata{UserID: "u"}); err != nil {
			return err
		}
		return repo.Save(ctx, loaded) // first attempt: version conflict; retry: succeeds
	})
	if err != nil {
		t.Fatalf("RetryOnConflict should converge, got %v", err)
	}
	if attempts < 2 {
		t.Fatalf("expected at least one retry (2+ attempts), got %d", attempts)
	}
}

// --- Balance ---

func TestInMemoryBalanceRepository_FindByIDReturnsIsolatedCopy(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	clock := shared.FixedClock{FixedTime: now}
	repo := NewInMemoryBalanceRepository(clock)
	ctx := context.Background()

	jpy := shared.CurrencyJPY
	entry, err := balance.NewBalanceEntry(shared.NewAccountID(),
		shared.NewMoney(new(big.Rat).SetInt64(1000), jpy), balance.BalanceReasonGoodwill, now)
	if err != nil {
		t.Fatalf("NewBalanceEntry failed: %v", err)
	}
	if err := repo.Save(ctx, entry); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	a, err := repo.FindByID(ctx, entry.ID())
	if err != nil {
		t.Fatalf("FindByID(a) failed: %v", err)
	}
	b, err := repo.FindByID(ctx, entry.ID())
	if err != nil {
		t.Fatalf("FindByID(b) failed: %v", err)
	}
	if a == b {
		t.Fatal("FindByID returned the same entry pointer twice; reads are not isolated")
	}

	// Consume from a without saving; b and a fresh read keep the full balance.
	if _, err := a.Consume(shared.NewMoney(new(big.Rat).SetInt64(400), jpy)); err != nil {
		t.Fatalf("Consume failed: %v", err)
	}
	if b.RemainingAmount().Amount().Cmp(new(big.Rat).SetInt64(1000)) != 0 {
		t.Errorf("sibling read saw uncommitted consume: remaining=%s", b.RemainingAmount().Amount())
	}
	c, err := repo.FindByID(ctx, entry.ID())
	if err != nil {
		t.Fatalf("FindByID(c) failed: %v", err)
	}
	if c.RemainingAmount().Amount().Cmp(new(big.Rat).SetInt64(1000)) != 0 {
		t.Errorf("repository polluted by unsaved consume: remaining=%s", c.RemainingAmount().Amount())
	}
}
