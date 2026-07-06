package pricing

import (
	"errors"
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

// Guard added for issue #148: NewUsagePrice validates that any Min/Max clamp
// shares the unit price currency, so a misconfigured clamp surfaces as an error
// instead of being silently dropped by CalculatePrice (Money.GreaterThan returns
// false on a currency mismatch).
func TestNewUsagePrice_ClampCurrencyGuards(t *testing.T) {
	jpy := func(n int64) shared.Money { return shared.NewMoney(new(big.Rat).SetInt64(n), shared.CurrencyJPY) }
	usd := func(n int64) shared.Money { return shared.NewMoney(new(big.Rat).SetInt64(n), shared.CurrencyUSD) }
	ptr := func(m shared.Money) *shared.Money { return &m }

	tests := []struct {
		name    string
		minimum *shared.Money
		maximum *shared.Money
		wantErr bool
	}{
		{"no clamps", nil, nil, false},
		{"matching min and max", ptr(jpy(100)), ptr(jpy(200)), false},
		{"mismatched min", ptr(usd(100)), nil, true},
		{"mismatched max", nil, ptr(usd(200)), true},
		{"matching min, mismatched max", ptr(jpy(100)), ptr(usd(200)), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			up, err := NewUsagePrice(jpy(10), tc.minimum, tc.maximum)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var de *shared.DomainError
				if !errors.As(err, &de) || de.Code != shared.ErrCodeCurrencyMismatch {
					t.Fatalf("expected currency_mismatch, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Valid construction clamps as expected: 5 * 10 = 50, min 100 → 100.
			if tc.minimum != nil {
				if got := up.CalculatePrice(5); got.Amount().Cmp(tc.minimum.Amount()) != 0 {
					t.Errorf("expected min clamp %s, got %s",
						tc.minimum.Amount().RatString(), got.Amount().RatString())
				}
			}
		})
	}
}
