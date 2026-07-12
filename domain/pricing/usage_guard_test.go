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

// --- CalculatePrice invariant re-check on constructor bypass (issue #238) ---

// A struct-literal UsagePrice with a wrong-currency clamp previously had the
// clamp silently ignored (Money.GreaterThan no-ops on a currency mismatch) —
// e.g. a USD Maximum on a JPY unit price silently failed to cap the charge.
// CalculatePrice must panic instead, mirroring the mustAddTier policy.

func TestUsagePrice_CalculatePrice_MismatchedMinimumPanics(t *testing.T) {
	unitPrice := shared.NewMoney(new(big.Rat).SetInt64(10), shared.CurrencyJPY)
	min := shared.NewMoney(new(big.Rat).SetInt64(100), shared.CurrencyUSD)
	up := UsagePrice{UnitPrice: unitPrice, Minimum: &min}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for wrong-currency minimum clamp, got none")
		}
	}()
	up.CalculatePrice(5)
}

func TestUsagePrice_CalculatePrice_MismatchedMaximumPanics(t *testing.T) {
	unitPrice := shared.NewMoney(new(big.Rat).SetInt64(10), shared.CurrencyJPY)
	max := shared.NewMoney(new(big.Rat).SetInt64(200), shared.CurrencyUSD)
	up := UsagePrice{UnitPrice: unitPrice, Maximum: &max}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for wrong-currency maximum clamp, got none")
		}
	}()
	up.CalculatePrice(50)
}

// Zero usage takes the early return and never consults the clamps, so it stays
// panic-free even for an invalid literal — no wrong amount can be produced.
func TestUsagePrice_CalculatePrice_ZeroUsageDoesNotValidateClamps(t *testing.T) {
	unitPrice := shared.NewMoney(new(big.Rat).SetInt64(10), shared.CurrencyJPY)
	max := shared.NewMoney(new(big.Rat).SetInt64(200), shared.CurrencyUSD)
	up := UsagePrice{UnitPrice: unitPrice, Maximum: &max}

	if got := up.CalculatePrice(0); !got.IsZero() {
		t.Errorf("expected zero for zero usage, got %s", got.Amount().RatString())
	}
}
