package credit

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestNewCreditEntry(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)

	entry := NewCreditEntry(accountID, amount, CreditReasonProration, time.Now())

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
	if entry.Reason() != CreditReasonProration {
		t.Errorf("expected reason %s, got %s", CreditReasonProration, entry.Reason())
	}
	if entry.CreatedAt().IsZero() {
		t.Error("expected non-zero createdAt")
	}
}

func TestCreditEntry_IsExpired(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)

	t.Run("no expiration", func(t *testing.T) {
		entry := NewCreditEntry(accountID, amount, CreditReasonGoodwill, time.Now())
		if entry.IsExpired(time.Now()) {
			t.Error("expected not expired when no expiresAt is set")
		}
	})

	t.Run("not yet expired", func(t *testing.T) {
		entry := NewCreditEntry(accountID, amount, CreditReasonGoodwill, time.Now())
		future := time.Now().Add(24 * time.Hour)
		entry.expiresAt = &future
		if entry.IsExpired(time.Now()) {
			t.Error("expected not expired when expiresAt is in the future")
		}
	})

	t.Run("expired", func(t *testing.T) {
		entry := NewCreditEntry(accountID, amount, CreditReasonGoodwill, time.Now())
		past := time.Now().Add(-24 * time.Hour)
		entry.expiresAt = &past
		if !entry.IsExpired(time.Now()) {
			t.Error("expected expired when expiresAt is in the past")
		}
	})
}

func TestCreditEntry_IsFullyConsumed(t *testing.T) {
	accountID := shared.NewAccountID()

	t.Run("not consumed", func(t *testing.T) {
		amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
		entry := NewCreditEntry(accountID, amount, CreditReasonProration, time.Now())
		if entry.IsFullyConsumed() {
			t.Error("expected not fully consumed")
		}
	})

	t.Run("fully consumed", func(t *testing.T) {
		amount := shared.NewMoney(new(big.Rat).SetInt64(0), shared.CurrencyJPY)
		entry := NewCreditEntry(accountID, amount, CreditReasonProration, time.Now())
		if !entry.IsFullyConsumed() {
			t.Error("expected fully consumed when amount is zero")
		}
	})
}
