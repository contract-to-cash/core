package shared

import (
	"math/big"
	"testing"
)

func money(numerator, denominator int64, currency Currency) Money {
	return NewMoney(big.NewRat(numerator, denominator), currency)
}

func TestMoney_Round(t *testing.T) {
	tests := []struct {
		name      string
		amount    Money
		places    int
		mode      RoundingMode
		wantNum   int64
		wantDenom int64
	}{
		{"half_up rounds 1/3 down at 2dp", money(1, 3, CurrencyUSD), 2, RoundHalfUp, 33, 100},
		{"half_up rounds 2/3 up at 2dp", money(2, 3, CurrencyUSD), 2, RoundHalfUp, 67, 100},
		{"half_up rounds exact half away from zero", money(5, 1000, CurrencyUSD), 2, RoundHalfUp, 1, 100},
		{"down truncates toward zero", money(339, 1000, CurrencyUSD), 2, RoundDown, 33, 100},
		{"up rounds away from zero", money(331, 1000, CurrencyUSD), 2, RoundUp, 34, 100},
		{"up leaves exact value untouched", money(50, 100, CurrencyUSD), 2, RoundUp, 1, 2},
		{"negative half_up rounds away from zero", money(-2, 3, CurrencyUSD), 2, RoundHalfUp, -67, 100},
		{"negative down truncates toward zero", money(-339, 1000, CurrencyUSD), 2, RoundDown, -33, 100},
		{"negative up rounds away from zero", money(-331, 1000, CurrencyUSD), 2, RoundUp, -34, 100},
		{"zero decimal places (JPY) half_up", money(2505, 100, CurrencyJPY), 0, RoundHalfUp, 25, 1},
		{"zero decimal places down", money(2599, 100, CurrencyJPY), 0, RoundDown, 25, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.amount.Round(tt.places, tt.mode)
			want := big.NewRat(tt.wantNum, tt.wantDenom)
			if got.Amount().Cmp(want) != 0 {
				t.Errorf("Round(%d, %s) = %s, want %s",
					tt.places, tt.mode, got.Amount().RatString(), want.RatString())
			}
			if got.Currency() != tt.amount.Currency() {
				t.Errorf("Round changed currency: got %s, want %s", got.Currency(), tt.amount.Currency())
			}
		})
	}
}

func TestMoney_Round_DoesNotMutateReceiver(t *testing.T) {
	original := money(1, 3, CurrencyUSD)
	_ = original.Round(2, RoundHalfUp)
	if original.Amount().Cmp(big.NewRat(1, 3)) != 0 {
		t.Errorf("Round mutated receiver: got %s", original.Amount().RatString())
	}
}

func TestMoney_Round_NilAmountIsZero(t *testing.T) {
	var m Money
	got := m.Round(2, RoundHalfUp)
	if got.Amount().Sign() != 0 {
		t.Errorf("expected zero, got %s", got.Amount().RatString())
	}
}

func TestMoney_Round_UnknownModeDefaultsToHalfUp(t *testing.T) {
	got := money(2, 3, CurrencyUSD).Round(2, RoundingMode("bogus"))
	if got.Amount().Cmp(big.NewRat(67, 100)) != 0 {
		t.Errorf("expected half-up fallback 67/100, got %s", got.Amount().RatString())
	}
}
