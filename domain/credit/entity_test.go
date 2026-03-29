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

func TestCreditEntry_Version(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry := NewCreditEntry(accountID, amount, CreditReasonProration, time.Now())

	if entry.Version() != 0 {
		t.Errorf("expected initial version 0, got %d", entry.Version())
	}

	entry.SetVersion(5)
	if entry.Version() != 5 {
		t.Errorf("expected version 5 after SetVersion, got %d", entry.Version())
	}
}

func TestCreditEntry_Consume_IncrementsVersion(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	entry := NewCreditEntry(accountID, amount, CreditReasonProration, time.Now())

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

func TestCreditEntry_Consume_ZeroAmountDoesNotIncrementVersion(t *testing.T) {
	accountID := shared.NewAccountID()
	amount := shared.NewMoney(new(big.Rat).SetInt64(0), shared.CurrencyJPY)
	entry := NewCreditEntry(accountID, amount, CreditReasonProration, time.Now())

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
