package balance

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Guards added for issue #148: NewBalanceEntry rejects a negative amount (which
// would be applied as a debit and inflate an invoice's amount due) but permits
// zero (an inert, fully-consumed / zero-balance entry).
func TestNewBalanceEntry_AmountGuards(t *testing.T) {
	acct := shared.NewAccountID()
	createdAt := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	money := func(n int64) shared.Money {
		return shared.NewMoney(new(big.Rat).SetInt64(n), shared.CurrencyJPY)
	}

	tests := []struct {
		name    string
		amount  shared.Money
		wantErr bool
	}{
		{"negative rejected", money(-1), true},
		{"zero allowed", money(0), false},
		{"positive allowed", money(1000), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, err := NewBalanceEntry(acct, tc.amount, BalanceReasonGoodwill, createdAt)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var de *shared.DomainError
				if !errorsAsBalance(err, &de) || de.Code != shared.ErrCodeValidation {
					t.Fatalf("expected validation_error, got %v", err)
				}
				if entry != nil {
					t.Error("expected nil entry on rejection")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if entry == nil {
				t.Fatal("expected non-nil entry")
			}
			if entry.OriginalAmount().Amount().Cmp(tc.amount.Amount()) != 0 {
				t.Errorf("expected originalAmount %s, got %s",
					tc.amount.Amount().RatString(), entry.OriginalAmount().Amount().RatString())
			}
		})
	}
}
