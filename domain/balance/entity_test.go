package balance

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestNewBalanceEntry(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)

	entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	if entry.ID() == "" {
		t.Error("expected non-empty ID")
	}
	if entry.AccountID() != accountID {
		t.Errorf("expected accountID %s, got %s", accountID, entry.AccountID())
	}
	if entry.OriginalAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("expected original amount %s, got %s", amount.Amount().RatString(), entry.OriginalAmount().Amount().RatString())
	}
	if entry.RemainingAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("expected remaining amount %s, got %s", amount.Amount().RatString(), entry.RemainingAmount().Amount().RatString())
	}
	if entry.Reason() != BalanceReasonProration {
		t.Errorf("expected reason %s, got %s", BalanceReasonProration, entry.Reason())
	}
	if entry.CreatedAt().IsZero() {
		t.Error("expected non-zero createdAt")
	}
}

func TestBalanceEntry_IsExpired(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)

	t.Run("no expiration", func(t *testing.T) {
		entry := NewBalanceEntry(accountID, amount, BalanceReasonGoodwill, time.Now())
		if entry.IsExpired(time.Now()) {
			t.Error("expected not expired when no expiresAt is set")
		}
	})

	t.Run("not yet expired", func(t *testing.T) {
		entry := NewBalanceEntry(accountID, amount, BalanceReasonGoodwill, time.Now())
		future := time.Now().Add(24 * time.Hour)
		entry.expiresAt = &future
		if entry.IsExpired(time.Now()) {
			t.Error("expected not expired when expiresAt is in the future")
		}
	})

	t.Run("expired", func(t *testing.T) {
		entry := NewBalanceEntry(accountID, amount, BalanceReasonGoodwill, time.Now())
		past := time.Now().Add(-24 * time.Hour)
		entry.expiresAt = &past
		if !entry.IsExpired(time.Now()) {
			t.Error("expected expired when expiresAt is in the past")
		}
	})
}

func TestBalanceEntry_Version(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	if entry.Version() != 0 {
		t.Errorf("expected initial version 0, got %d", entry.Version())
	}

	entry.SetVersion(5)
	if entry.Version() != 5 {
		t.Errorf("expected version 5 after SetVersion, got %d", entry.Version())
	}
}

func TestBalanceEntry_Consume_IncrementsVersion(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	consumeAmt := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	_, err := entry.Consume(consumeAmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Version() != 1 {
		t.Errorf("expected version 1 after first consume, got %d", entry.Version())
	}

	_, err = entry.Consume(consumeAmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Version() != 2 {
		t.Errorf("expected version 2 after second consume, got %d", entry.Version())
	}
}

func TestBalanceEntry_Consume_ZeroAmountDoesNotIncrementVersion(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(0), shared.CurrencyJPY)
	entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	consumeAmt := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	consumed, err := entry.Consume(consumeAmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !consumed.IsZero() {
		t.Error("expected zero consumed from zero-balance entry")
	}
	if entry.Version() != 0 {
		t.Errorf("expected version 0 (no actual consumption), got %d", entry.Version())
	}
}

func TestBalanceEntry_IsFullyConsumed(t *testing.T) {
	accountID := shared.NewAccountID()

	t.Run("not consumed", func(t *testing.T) {
		amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
		entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())
		if entry.IsFullyConsumed() {
			t.Error("expected not fully consumed")
		}
	})

	t.Run("fully consumed", func(t *testing.T) {
		amount := shared.NewMoney(new(big.Rat).SetInt64(0), shared.CurrencyJPY)
		entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())
		if !entry.IsFullyConsumed() {
			t.Error("expected fully consumed when amount is zero")
		}
	})
}

func TestBalanceEntry_Consume_PartialConsumption(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	consumeAmt := shared.NewMoney(new(big.Rat).SetInt64(300), shared.CurrencyJPY)
	consumed, err := entry.Consume(consumeAmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedConsumed := new(big.Rat).SetInt64(300)
	if consumed.Amount().Cmp(expectedConsumed) != 0 {
		t.Errorf("expected consumed amount 300, got %s", consumed.Amount().RatString())
	}

	expectedRemaining := new(big.Rat).SetInt64(700)
	if entry.RemainingAmount().Amount().Cmp(expectedRemaining) != 0 {
		t.Errorf("expected remaining amount 700, got %s", entry.RemainingAmount().Amount().RatString())
	}

	if entry.OriginalAmount().Amount().Cmp(new(big.Rat).SetInt64(1000)) != 0 {
		t.Errorf("expected originalAmount to remain 1000, got %s", entry.OriginalAmount().Amount().RatString())
	}

	if entry.IsFullyConsumed() {
		t.Error("expected not fully consumed after partial consumption")
	}
}

func TestBalanceEntry_Consume_FullConsumption(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	consumeAmt := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	consumed, err := entry.Consume(consumeAmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedConsumed := new(big.Rat).SetInt64(1000)
	if consumed.Amount().Cmp(expectedConsumed) != 0 {
		t.Errorf("expected consumed amount 1000, got %s", consumed.Amount().RatString())
	}

	if !entry.RemainingAmount().IsZero() {
		t.Errorf("expected remaining amount to be zero, got %s", entry.RemainingAmount().Amount().RatString())
	}

	if entry.OriginalAmount().Amount().Cmp(new(big.Rat).SetInt64(1000)) != 0 {
		t.Errorf("expected originalAmount to remain 1000, got %s", entry.OriginalAmount().Amount().RatString())
	}

	if !entry.IsFullyConsumed() {
		t.Error("expected fully consumed after consuming entire balance")
	}
}

func TestBalanceEntry_Consume_Overconsumption(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	consumeAmt := shared.NewMoney(new(big.Rat).SetInt64(800), shared.CurrencyJPY)
	consumed, err := entry.Consume(consumeAmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only consume what was available (500), not the full 800
	expectedConsumed := new(big.Rat).SetInt64(500)
	if consumed.Amount().Cmp(expectedConsumed) != 0 {
		t.Errorf("expected consumed amount 500 (capped at available), got %s", consumed.Amount().RatString())
	}

	if !entry.RemainingAmount().IsZero() {
		t.Errorf("expected remaining amount to be zero after overconsumption, got %s", entry.RemainingAmount().Amount().RatString())
	}

	if !entry.IsFullyConsumed() {
		t.Error("expected fully consumed after overconsumption")
	}

	if entry.OriginalAmount().Amount().Cmp(new(big.Rat).SetInt64(500)) != 0 {
		t.Errorf("expected originalAmount to remain 500, got %s", entry.OriginalAmount().Amount().RatString())
	}
}

func TestBalanceEntry_Consume_ZeroAmount(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	consumeAmt := shared.NewMoney(new(big.Rat).SetInt64(0), shared.CurrencyJPY)
	consumed, err := entry.Consume(consumeAmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !consumed.IsZero() {
		t.Errorf("expected zero consumed, got %s", consumed.Amount().RatString())
	}
	if entry.RemainingAmount().Amount().Cmp(new(big.Rat).SetInt64(1000)) != 0 {
		t.Errorf("expected remaining amount to stay 1000, got %s", entry.RemainingAmount().Amount().RatString())
	}
	if entry.Version() != 0 {
		t.Errorf("expected version to stay 0, got %d", entry.Version())
	}
}

func TestBalanceEntry_Consume_AlreadyFullyConsumed(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	// Consume all
	_, err := entry.Consume(shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !entry.IsFullyConsumed() {
		t.Fatal("expected fully consumed")
	}
	versionAfterFull := entry.Version()

	// Try to consume again from empty balance
	consumed, err := entry.Consume(shared.NewMoney(new(big.Rat).SetInt64(100), shared.CurrencyJPY))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !consumed.IsZero() {
		t.Errorf("expected zero consumed from fully-consumed entry, got %s", consumed.Amount().RatString())
	}
	if !entry.RemainingAmount().IsZero() {
		t.Errorf("expected remaining to stay zero, got %s", entry.RemainingAmount().Amount().RatString())
	}
	if entry.Version() != versionAfterFull {
		t.Errorf("expected version to stay %d, got %d", versionAfterFull, entry.Version())
	}
}

func TestBalanceEntry_Consume_ExpiredEntryCanStillBeConsumed(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry := NewBalanceEntry(accountID, amount, BalanceReasonGoodwill, time.Now())

	// Set expiration in the past
	past := time.Now().Add(-1 * time.Hour)
	entry.expiresAt = &past

	// Verify it is expired
	if !entry.IsExpired(time.Now()) {
		t.Fatal("expected entry to be expired")
	}

	// Entity itself does not prevent consumption of expired entries; that is the caller's responsibility
	consumeAmt := shared.NewMoney(new(big.Rat).SetInt64(400), shared.CurrencyJPY)
	consumed, err := entry.Consume(consumeAmt)
	if err != nil {
		t.Fatalf("unexpected error consuming expired entry: %v", err)
	}

	expectedConsumed := new(big.Rat).SetInt64(400)
	if consumed.Amount().Cmp(expectedConsumed) != 0 {
		t.Errorf("expected consumed amount 400, got %s", consumed.Amount().RatString())
	}

	expectedRemaining := new(big.Rat).SetInt64(600)
	if entry.RemainingAmount().Amount().Cmp(expectedRemaining) != 0 {
		t.Errorf("expected remaining amount 600, got %s", entry.RemainingAmount().Amount().RatString())
	}
}

func TestBalanceEntry_IsExpired_ExactBoundary(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry := NewBalanceEntry(accountID, amount, BalanceReasonGoodwill, time.Now())

	boundary := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	entry.expiresAt = &boundary

	// At the exact boundary time, IsExpired uses After (strictly), so it should NOT be expired
	if entry.IsExpired(boundary) {
		t.Error("expected not expired at exact boundary time (After is strictly after)")
	}

	// One nanosecond after the boundary should be expired
	if !entry.IsExpired(boundary.Add(1 * time.Nanosecond)) {
		t.Error("expected expired one nanosecond after boundary")
	}
}

func TestBalanceEntry_LoadedVersion_Tracking(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	// New entry: both version and loadedVersion should be 0
	if entry.Version() != 0 {
		t.Errorf("expected initial version 0, got %d", entry.Version())
	}
	if entry.LoadedVersion() != 0 {
		t.Errorf("expected initial loadedVersion 0, got %d", entry.LoadedVersion())
	}

	// Simulate loading from persistence with version 3
	entry.SetVersion(3)
	if entry.Version() != 3 {
		t.Errorf("expected version 3 after SetVersion, got %d", entry.Version())
	}
	if entry.LoadedVersion() != 3 {
		t.Errorf("expected loadedVersion 3 after SetVersion, got %d", entry.LoadedVersion())
	}

	// Consume should increment version but NOT loadedVersion
	consumeAmt := shared.NewMoney(new(big.Rat).SetInt64(200), shared.CurrencyJPY)
	_, err := entry.Consume(consumeAmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Version() != 4 {
		t.Errorf("expected version 4 after Consume, got %d", entry.Version())
	}
	if entry.LoadedVersion() != 3 {
		t.Errorf("expected loadedVersion to remain 3 after Consume, got %d", entry.LoadedVersion())
	}

	// Second consume should increment version again, loadedVersion still unchanged
	_, err = entry.Consume(consumeAmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Version() != 5 {
		t.Errorf("expected version 5 after second Consume, got %d", entry.Version())
	}
	if entry.LoadedVersion() != 3 {
		t.Errorf("expected loadedVersion to remain 3 after second Consume, got %d", entry.LoadedVersion())
	}
}
