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

	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

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
		entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonGoodwill, time.Now())
		if entry.IsExpired(time.Now()) {
			t.Error("expected not expired when no expiresAt is set")
		}
	})

	t.Run("not yet expired", func(t *testing.T) {
		entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonGoodwill, time.Now())
		future := time.Now().Add(24 * time.Hour)
		entry.expiresAt = &future
		if entry.IsExpired(time.Now()) {
			t.Error("expected not expired when expiresAt is in the future")
		}
	})

	t.Run("expired", func(t *testing.T) {
		entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonGoodwill, time.Now())
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
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

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
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

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

// TestBalanceEntry_Consume_NegativeAmount_Rejected guards a financial invariant:
// a negative consume would otherwise subtract a negative and INFLATE the
// remaining balance (create credit out of thin air). See review C1.
func TestBalanceEntry_Consume_NegativeAmount_Rejected(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	neg := shared.NewMoney(new(big.Rat).SetInt64(-500), shared.CurrencyJPY)
	_, err := entry.Consume(neg)
	if err == nil {
		t.Fatal("expected error consuming a negative amount, got nil")
	}
	var domErr *shared.DomainError
	if !errorsAsBalance(err, &domErr) || domErr.Code != shared.ErrCodeValidation {
		t.Errorf("expected validation error, got %v", err)
	}
	// Balance must be unchanged.
	if entry.RemainingAmount().Amount().Cmp(big.NewRat(1000, 1)) != 0 {
		t.Errorf("balance changed after rejected consume: %s", entry.RemainingAmount().Amount().RatString())
	}
	if entry.Version() != 0 {
		t.Errorf("version changed after rejected consume: %d", entry.Version())
	}
}

func errorsAsBalance(err error, target **shared.DomainError) bool {
	for err != nil {
		if de, ok := err.(*shared.DomainError); ok {
			*target = de
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestBalanceEntry_Consume_ZeroAmountDoesNotIncrementVersion(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(0), shared.CurrencyJPY)
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

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
		entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())
		if entry.IsFullyConsumed() {
			t.Error("expected not fully consumed")
		}
	})

	t.Run("fully consumed", func(t *testing.T) {
		amount := shared.NewMoney(new(big.Rat).SetInt64(0), shared.CurrencyJPY)
		entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())
		if !entry.IsFullyConsumed() {
			t.Error("expected fully consumed when amount is zero")
		}
	})
}

func TestBalanceEntry_Consume_PartialConsumption(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

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
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

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
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

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
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

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
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

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
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonGoodwill, time.Now())

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

func TestBalanceSourceType_TypeSafety(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)

	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	// Default sourceType should be zero value
	if entry.SourceType() != "" {
		t.Errorf("expected empty sourceType by default, got %q", entry.SourceType())
	}

	// Set using typed constant
	entry.SetSourceType(BalanceSourceTypeProration)
	if entry.SourceType() != BalanceSourceTypeProration {
		t.Errorf("expected sourceType %q, got %q", BalanceSourceTypeProration, entry.SourceType())
	}

	// Verify constant values
	if BalanceSourceTypeProration != "proration" {
		t.Errorf("expected BalanceSourceTypeProration to be 'proration', got %q", BalanceSourceTypeProration)
	}
	if BalanceSourceTypeManual != "manual" {
		t.Errorf("expected BalanceSourceTypeManual to be 'manual', got %q", BalanceSourceTypeManual)
	}
	if BalanceSourceTypeRefundConversion != "refund_conversion" {
		t.Errorf("expected BalanceSourceTypeRefundConversion to be 'refund_conversion', got %q", BalanceSourceTypeRefundConversion)
	}
}

func TestBalanceEntry_IsExpired_ExactBoundary(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonGoodwill, time.Now())

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
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

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

// --- Restore (issue #184) ---

func TestBalanceEntry_Restore_ReturnsConsumedCredit(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())

	consumed, err := entry.Consume(shared.NewMoney(new(big.Rat).SetInt64(600), shared.CurrencyJPY))
	if err != nil {
		t.Fatalf("unexpected consume error: %v", err)
	}
	if consumed.Amount().Cmp(big.NewRat(600, 1)) != 0 {
		t.Fatalf("expected 600 consumed, got %s", consumed.Amount().RatString())
	}
	if entry.RemainingAmount().Amount().Cmp(big.NewRat(400, 1)) != 0 {
		t.Fatalf("expected remaining 400 after consume, got %s", entry.RemainingAmount().Amount().RatString())
	}
	versionAfterConsume := entry.Version()

	if err := entry.Restore(consumed); err != nil {
		t.Fatalf("unexpected restore error: %v", err)
	}
	if entry.RemainingAmount().Amount().Cmp(big.NewRat(1000, 1)) != 0 {
		t.Errorf("expected remaining restored to 1000, got %s", entry.RemainingAmount().Amount().RatString())
	}
	if entry.Version() != versionAfterConsume+1 {
		t.Errorf("expected version bumped to %d after restore, got %d", versionAfterConsume+1, entry.Version())
	}
}

func TestBalanceEntry_Restore_NegativeAmount_Rejected(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())
	_, _ = entry.Consume(shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY))

	neg := shared.NewMoney(new(big.Rat).SetInt64(-100), shared.CurrencyJPY)
	err := entry.Restore(neg)
	if err == nil {
		t.Fatal("expected error restoring a negative amount, got nil")
	}
	var domErr *shared.DomainError
	if !errorsAsBalance(err, &domErr) || domErr.Code != shared.ErrCodeValidation {
		t.Errorf("expected validation error, got %v", err)
	}
}

func TestBalanceEntry_Restore_ZeroAmount_NoVersionBump(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())
	_, _ = entry.Consume(shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY))
	versionBefore := entry.Version()

	if err := entry.Restore(shared.Zero(shared.CurrencyJPY)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Version() != versionBefore {
		t.Errorf("expected version unchanged on zero restore, got %d (was %d)", entry.Version(), versionBefore)
	}
	if entry.RemainingAmount().Amount().Cmp(big.NewRat(500, 1)) != 0 {
		t.Errorf("expected remaining unchanged at 500, got %s", entry.RemainingAmount().Amount().RatString())
	}
}

// TestBalanceEntry_Restore_ CannotExceedOriginal guards against fabricating
// credit: restoring more than was consumed would push remaining above original.
func TestBalanceEntry_Restore_CannotExceedOriginal(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())
	_, _ = entry.Consume(shared.NewMoney(new(big.Rat).SetInt64(300), shared.CurrencyJPY))

	// Only 300 was consumed; restoring 400 would exceed the original 1000.
	err := entry.Restore(shared.NewMoney(new(big.Rat).SetInt64(400), shared.CurrencyJPY))
	if err == nil {
		t.Fatal("expected error restoring more than consumed, got nil")
	}
	var domErr *shared.DomainError
	if !errorsAsBalance(err, &domErr) || domErr.Code != shared.ErrCodeBusinessRule {
		t.Errorf("expected business_rule error, got %v", err)
	}
	// Remaining must be untouched on rejection.
	if entry.RemainingAmount().Amount().Cmp(big.NewRat(700, 1)) != 0 {
		t.Errorf("expected remaining unchanged at 700 after rejected restore, got %s", entry.RemainingAmount().Amount().RatString())
	}
}

func TestBalanceEntry_Restore_CurrencyMismatch_Rejected(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())
	_, _ = entry.Consume(shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY))

	err := entry.Restore(shared.NewMoney(new(big.Rat).SetInt64(100), shared.CurrencyUSD))
	if err == nil {
		t.Fatal("expected currency mismatch error, got nil")
	}
}

// TestBalanceEntry_Restore_ExpiredEntryStillRestored documents the expiration
// semantics: restoration ignores expiry (the expiration batch sweeps it later).
func TestBalanceEntry_Restore_ExpiredEntryStillRestored(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry, _ := NewBalanceEntry(accountID, amount, BalanceReasonProration, time.Now())
	_, _ = entry.Consume(shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY))
	past := time.Now().Add(-24 * time.Hour)
	entry.expiresAt = &past

	if err := entry.Restore(shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)); err != nil {
		t.Fatalf("expected expired entry to still accept restore, got %v", err)
	}
	if entry.RemainingAmount().Amount().Cmp(big.NewRat(1000, 1)) != 0 {
		t.Errorf("expected remaining restored to 1000 on expired entry, got %s", entry.RemainingAmount().Amount().RatString())
	}
}
