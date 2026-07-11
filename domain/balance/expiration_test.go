package balance

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Tests for BalanceEntry.MarkExpired (issue #159): forfeiting the remaining
// amount of an expired credit entry.

func newExpiringEntry(t *testing.T, amount int64, expiresAt time.Time) *BalanceEntry {
	t.Helper()
	created := expiresAt.Add(-30 * 24 * time.Hour)
	entry, err := NewBalanceEntry(
		shared.NewAccountID(),
		shared.NewMoney(big.NewRat(amount, 1), shared.CurrencyJPY),
		BalanceReasonGoodwill,
		created,
	)
	if err != nil {
		t.Fatalf("NewBalanceEntry: %v", err)
	}
	entry.SetExpiresAt(&expiresAt)
	return entry
}

func assertBalanceDomainCode(t *testing.T, err error, want shared.ErrorCode) {
	t.Helper()
	var de *shared.DomainError
	if !errors.As(err, &de) || de.Code != want {
		t.Errorf("expected DomainError %s, got %v", want, err)
	}
}

// TestConsumeAt_EnforcesExpiry verifies that ConsumeAt refuses to spend an
// expired entry while still allowing consumption before expiry (issue #196).
func TestConsumeAt_EnforcesExpiry(t *testing.T) {
	expiry := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)

	t.Run("before expiry consumes", func(t *testing.T) {
		entry := newExpiringEntry(t, 1000, expiry)
		consumed, err := entry.ConsumeAt(shared.NewMoney(big.NewRat(400, 1), shared.CurrencyJPY), expiry.Add(-time.Hour))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if consumed.Amount().Cmp(big.NewRat(400, 1)) != 0 {
			t.Errorf("expected 400 consumed, got %s", consumed.Amount().RatString())
		}
		if entry.RemainingAmount().Amount().Cmp(big.NewRat(600, 1)) != 0 {
			t.Errorf("expected 600 remaining, got %s", entry.RemainingAmount().Amount().RatString())
		}
	})

	t.Run("at or after expiry is rejected and does not consume", func(t *testing.T) {
		entry := newExpiringEntry(t, 1000, expiry)
		_, err := entry.ConsumeAt(shared.NewMoney(big.NewRat(400, 1), shared.CurrencyJPY), expiry.Add(time.Hour))
		if err == nil {
			t.Fatal("expected error consuming expired entry")
		}
		assertBalanceDomainCode(t, err, shared.ErrCodeBusinessRule)
		if entry.RemainingAmount().Amount().Cmp(big.NewRat(1000, 1)) != 0 {
			t.Errorf("expected remaining unchanged at 1000, got %s", entry.RemainingAmount().Amount().RatString())
		}
		if entry.Version() != 0 {
			t.Errorf("expected version unchanged (0) after rejected consume, got %d", entry.Version())
		}
	})

	t.Run("no expiry set consumes normally", func(t *testing.T) {
		entry, err := NewBalanceEntry(shared.NewAccountID(),
			shared.NewMoney(big.NewRat(500, 1), shared.CurrencyJPY), BalanceReasonGoodwill, expiry)
		if err != nil {
			t.Fatalf("NewBalanceEntry: %v", err)
		}
		consumed, err := entry.ConsumeAt(shared.NewMoney(big.NewRat(500, 1), shared.CurrencyJPY), expiry.Add(365*24*time.Hour))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if consumed.Amount().Cmp(big.NewRat(500, 1)) != 0 {
			t.Errorf("expected 500 consumed, got %s", consumed.Amount().RatString())
		}
	})
}

func TestMarkExpired_ForfeitsRemainingAmount(t *testing.T) {
	expiry := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	entry := newExpiringEntry(t, 1000, expiry)
	beforeVersion := entry.Version()

	forfeited, err := entry.MarkExpired(expiry.Add(time.Hour))
	if err != nil {
		t.Fatalf("MarkExpired: %v", err)
	}
	if forfeited.Amount().Cmp(big.NewRat(1000, 1)) != 0 {
		t.Errorf("expected forfeited 1000, got %s", forfeited.Amount().RatString())
	}
	if !entry.RemainingAmount().IsZero() {
		t.Errorf("expected zero remaining after expiry, got %s", entry.RemainingAmount().Amount().RatString())
	}
	if !entry.IsFullyConsumed() {
		t.Error("expired entry must report IsFullyConsumed")
	}
	// Original amount and expiry stay for audit.
	if entry.OriginalAmount().Amount().Cmp(big.NewRat(1000, 1)) != 0 {
		t.Errorf("original amount must be preserved, got %s", entry.OriginalAmount().Amount().RatString())
	}
	if entry.ExpiresAt() == nil || !entry.ExpiresAt().Equal(expiry) {
		t.Errorf("expiresAt must be preserved, got %v", entry.ExpiresAt())
	}
	// Optimistic-locking version bumps on a forfeiting expiry (like Consume).
	if entry.Version() != beforeVersion+1 {
		t.Errorf("expected version %d, got %d", beforeVersion+1, entry.Version())
	}
}

func TestMarkExpired_PartiallyConsumedEntry_ForfeitsRemainder(t *testing.T) {
	expiry := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	entry := newExpiringEntry(t, 1000, expiry)
	if _, err := entry.Consume(shared.NewMoney(big.NewRat(400, 1), shared.CurrencyJPY)); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	forfeited, err := entry.MarkExpired(expiry.Add(time.Hour))
	if err != nil {
		t.Fatalf("MarkExpired: %v", err)
	}
	if forfeited.Amount().Cmp(big.NewRat(600, 1)) != 0 {
		t.Errorf("expected forfeited 600 (the remainder), got %s", forfeited.Amount().RatString())
	}
	if !entry.RemainingAmount().IsZero() {
		t.Errorf("expected zero remaining, got %s", entry.RemainingAmount().Amount().RatString())
	}
}

func TestMarkExpired_NotYetExpired_Rejected(t *testing.T) {
	expiry := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	entry := newExpiringEntry(t, 1000, expiry)
	beforeVersion := entry.Version()

	// Exactly at the expiry instant is NOT expired (IsExpired uses After).
	for _, now := range []time.Time{expiry, expiry.Add(-time.Hour)} {
		_, err := entry.MarkExpired(now)
		if err == nil {
			t.Fatalf("expected error marking unexpired entry at %s, got nil", now)
		}
		assertBalanceDomainCode(t, err, shared.ErrCodeBusinessRule)
	}
	if entry.RemainingAmount().IsZero() {
		t.Error("remaining amount must not change on rejected expiry")
	}
	if entry.Version() != beforeVersion {
		t.Errorf("version must not change on rejected expiry: was %d, got %d", beforeVersion, entry.Version())
	}
}

func TestMarkExpired_NoExpirySet_Rejected(t *testing.T) {
	entry, err := NewBalanceEntry(
		shared.NewAccountID(),
		shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BalanceReasonGoodwill,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewBalanceEntry: %v", err)
	}

	_, err = entry.MarkExpired(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected error for entry without expiry, got nil")
	}
	assertBalanceDomainCode(t, err, shared.ErrCodeBusinessRule)
}

func TestMarkExpired_FullyConsumed_IdempotentNoOp(t *testing.T) {
	expiry := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	entry := newExpiringEntry(t, 1000, expiry)
	now := expiry.Add(time.Hour)

	if _, err := entry.MarkExpired(now); err != nil {
		t.Fatalf("first MarkExpired: %v", err)
	}
	versionAfterFirst := entry.Version()

	// Second run (e.g. the expiration batch re-running) is a no-op: zero
	// forfeit, no version bump, no error.
	forfeited, err := entry.MarkExpired(now)
	if err != nil {
		t.Fatalf("second MarkExpired must be idempotent, got error: %v", err)
	}
	if !forfeited.IsZero() {
		t.Errorf("second MarkExpired must forfeit zero, got %s", forfeited.Amount().RatString())
	}
	if entry.Version() != versionAfterFirst {
		t.Errorf("second MarkExpired must not bump version: was %d, got %d", versionAfterFirst, entry.Version())
	}
}
