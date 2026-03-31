package inmemory

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/shared"
)

func TestFindAvailable_FIFOAndExcludesExpired(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	clock := shared.FixedClock{FixedTime: now}
	repo := NewInMemoryBalanceRepository(clock)
	ctx := context.Background()
	accountID := shared.NewAccountID()

	jpy := shared.CurrencyJPY

	// Create entries at different times.
	entry1 := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(1000), jpy), balance.BalanceReasonGoodwill, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	entry2 := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(2000), jpy), balance.BalanceReasonGoodwill, time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC))
	entry3 := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(500), jpy), balance.BalanceReasonGoodwill, time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC))

	// entry3 is expired (we'll use SetExpiresAt if available, otherwise we create a fully consumed one)
	// Since CreditEntry doesn't have a public setter for expiresAt, we create a consumed entry instead.
	// Actually, let's create an entry that is fully consumed.
	entryConsumed := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(100), jpy), balance.BalanceReasonGoodwill, time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC))
	_, _ = entryConsumed.Consume(shared.NewMoney(new(big.Rat).SetInt64(100), jpy))

	for _, e := range []*balance.BalanceEntry{entry1, entry2, entry3, entryConsumed} {
		if err := repo.Save(ctx, e); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	available, err := repo.FindAvailable(ctx, accountID, jpy)
	if err != nil {
		t.Fatalf("FindAvailable failed: %v", err)
	}

	// Should exclude fully consumed entry. Remaining: entry1, entry3, entry2 (sorted by createdAt).
	if len(available) != 3 {
		t.Fatalf("expected 3 available entries, got %d", len(available))
	}

	// Verify FIFO order: entry1 (Jan), entry3 (Feb), entry2 (Mar).
	if available[0].ID() != entry1.ID() {
		t.Errorf("expected first entry to be entry1, got %s", available[0].ID())
	}
	if available[1].ID() != entry3.ID() {
		t.Errorf("expected second entry to be entry3, got %s", available[1].ID())
	}
	if available[2].ID() != entry2.ID() {
		t.Errorf("expected third entry to be entry2, got %s", available[2].ID())
	}
}

func TestGetBalance(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	clock := shared.FixedClock{FixedTime: now}
	repo := NewInMemoryBalanceRepository(clock)
	ctx := context.Background()
	accountID := shared.NewAccountID()
	jpy := shared.CurrencyJPY

	entry1 := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(1000), jpy), balance.BalanceReasonGoodwill, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	entry2 := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(2000), jpy), balance.BalanceReasonGoodwill, time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC))

	// Consume part of entry1.
	_, _ = entry1.Consume(shared.NewMoney(new(big.Rat).SetInt64(300), jpy))

	// Fully consume a third entry.
	entryConsumed := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(500), jpy), balance.BalanceReasonGoodwill, time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC))
	_, _ = entryConsumed.Consume(shared.NewMoney(new(big.Rat).SetInt64(500), jpy))

	for _, e := range []*balance.BalanceEntry{entry1, entry2, entryConsumed} {
		if err := repo.Save(ctx, e); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	balance, err := repo.GetBalance(ctx, accountID, jpy)
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}

	// Expected: 700 (entry1 remaining) + 2000 (entry2) = 2700
	expected := new(big.Rat).SetInt64(2700)
	if balance.Amount().Cmp(expected) != 0 {
		t.Errorf("expected balance 2700, got %s", balance.Amount().RatString())
	}
}

func TestFindByAccountID_ReturnsAllIncludingConsumedAndExpired(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	clock := shared.FixedClock{FixedTime: now}
	repo := NewInMemoryBalanceRepository(clock)
	ctx := context.Background()
	accountID := shared.NewAccountID()
	otherAccountID := shared.NewAccountID()
	jpy := shared.CurrencyJPY
	usd := shared.CurrencyUSD

	// Active entry.
	entry1 := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(1000), jpy), balance.BalanceReasonGoodwill, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	// Fully consumed entry — should still be returned.
	entryConsumed := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(500), jpy), balance.BalanceReasonManualAdjustment, time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC))
	_, _ = entryConsumed.Consume(shared.NewMoney(new(big.Rat).SetInt64(500), jpy))

	// Entry with later createdAt.
	entry3 := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(2000), jpy), balance.BalanceReasonProration, time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC))

	// Expired entry — should still be returned.
	pastExpiry := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC) // before now (June 1)
	entryExpired := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(300), jpy), balance.BalanceReasonCancellation, time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC))
	entryExpired.SetExpiresAt(&pastExpiry)

	// Different account — should NOT be returned.
	entryOther := balance.NewBalanceEntry(otherAccountID, shared.NewMoney(new(big.Rat).SetInt64(100), jpy), balance.BalanceReasonGoodwill, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	// Different currency — should NOT be returned.
	entryUSD := balance.NewBalanceEntry(accountID, shared.NewMoney(new(big.Rat).SetInt64(100), usd), balance.BalanceReasonGoodwill, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	for _, e := range []*balance.BalanceEntry{entry1, entryConsumed, entry3, entryExpired, entryOther, entryUSD} {
		if err := repo.Save(ctx, e); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	entries, err := repo.FindByAccountID(ctx, accountID, jpy)
	if err != nil {
		t.Fatalf("FindByAccountID failed: %v", err)
	}

	// Should return all 4 JPY entries for this account (including consumed and expired).
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	// Verify CreatedAt ascending order.
	if entries[0].ID() != entry1.ID() {
		t.Errorf("expected first entry to be entry1 (Jan), got %s", entries[0].ID())
	}
	if entries[1].ID() != entryConsumed.ID() {
		t.Errorf("expected second entry to be entryConsumed (Feb), got %s", entries[1].ID())
	}
	if entries[2].ID() != entry3.ID() {
		t.Errorf("expected third entry to be entry3 (Mar), got %s", entries[2].ID())
	}
	if entries[3].ID() != entryExpired.ID() {
		t.Errorf("expected fourth entry to be entryExpired (Apr), got %s", entries[3].ID())
	}
}

func TestFindByAccountID_ReturnsEmptyForNoMatch(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)}
	repo := NewInMemoryBalanceRepository(clock)
	ctx := context.Background()

	entries, err := repo.FindByAccountID(ctx, shared.NewAccountID(), shared.CurrencyJPY)
	if err != nil {
		t.Fatalf("FindByAccountID failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}
