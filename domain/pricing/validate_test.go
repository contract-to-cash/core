package pricing

import (
	"errors"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

// Validate() is the error-returning form of the constructor invariants, added
// so a model reconstructed from persistence (which bypasses the constructors
// for replay safety) can be checked gracefully instead of panicking on first
// CalculatePrice use.

func TestTieredPrice_Validate(t *testing.T) {
	validTiers := func() []PriceTier {
		return []PriceTier{
			{UpTo: 100, UnitPrice: jpy(10), FlatFee: jpy(0)},
			{UpTo: 0, UnitPrice: jpy(5), FlatFee: jpy(0)},
		}
	}

	tests := []struct {
		name     string
		price    TieredPrice
		wantCode shared.ErrorCode // "" = expect nil error
	}{
		{
			name:  "valid graduated",
			price: TieredPrice{Tiers: validTiers(), Mode: TieredPricingGraduated},
		},
		{
			name:  "valid volume",
			price: TieredPrice{Tiers: validTiers(), Mode: TieredPricingVolume},
		},
		{
			name:     "no tiers",
			price:    TieredPrice{Mode: TieredPricingGraduated},
			wantCode: shared.ErrCodeValidation,
		},
		{
			name:     "unknown mode",
			price:    TieredPrice{Tiers: validTiers(), Mode: TieredPricingMode("bogus")},
			wantCode: shared.ErrCodeValidation,
		},
		{
			name: "unsorted tiers",
			price: TieredPrice{Mode: TieredPricingGraduated, Tiers: []PriceTier{
				{UpTo: 200, UnitPrice: jpy(10), FlatFee: jpy(0)},
				{UpTo: 100, UnitPrice: jpy(5), FlatFee: jpy(0)},
			}},
			wantCode: shared.ErrCodeValidation,
		},
		{
			name: "unlimited sentinel not on last tier",
			price: TieredPrice{Mode: TieredPricingGraduated, Tiers: []PriceTier{
				{UpTo: 0, UnitPrice: jpy(10), FlatFee: jpy(0)},
				{UpTo: 100, UnitPrice: jpy(5), FlatFee: jpy(0)},
			}},
			wantCode: shared.ErrCodeValidation,
		},
		{
			name: "negative UpTo",
			price: TieredPrice{Mode: TieredPricingGraduated, Tiers: []PriceTier{
				{UpTo: -1, UnitPrice: jpy(10), FlatFee: jpy(0)},
				{UpTo: 0, UnitPrice: jpy(5), FlatFee: jpy(0)},
			}},
			wantCode: shared.ErrCodeValidation,
		},
		{
			name: "mixed-currency unit price",
			price: TieredPrice{Mode: TieredPricingGraduated, Tiers: []PriceTier{
				{UpTo: 100, UnitPrice: jpy(10), FlatFee: jpy(0)},
				{UpTo: 0, UnitPrice: usd(5), FlatFee: jpy(0)},
			}},
			wantCode: shared.ErrCodeCurrencyMismatch,
		},
		{
			name: "mixed-currency flat fee",
			price: TieredPrice{Mode: TieredPricingGraduated, Tiers: []PriceTier{
				{UpTo: 100, UnitPrice: jpy(10), FlatFee: usd(3)},
				{UpTo: 0, UnitPrice: jpy(5), FlatFee: jpy(0)},
			}},
			wantCode: shared.ErrCodeCurrencyMismatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.price.Validate()
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var de *shared.DomainError
			if !errors.As(err, &de) || de.Code != tc.wantCode {
				t.Fatalf("expected DomainError with code %s, got %v", tc.wantCode, err)
			}
		})
	}
}

// TestTieredPrice_Validate_MatchesConstructor pins the contract that Validate
// returns the SAME errors the constructor produces: for every violation class,
// NewTieredPrice's rejection and Validate()'s result must agree.
func TestTieredPrice_Validate_MatchesConstructor(t *testing.T) {
	cases := []struct {
		name  string
		tiers []PriceTier
		mode  TieredPricingMode
	}{
		{"no tiers", nil, TieredPricingGraduated},
		{"unknown mode", []PriceTier{{UpTo: 0, UnitPrice: jpy(10), FlatFee: jpy(0)}}, TieredPricingMode("bogus")},
		{"unsorted", []PriceTier{
			{UpTo: 200, UnitPrice: jpy(10), FlatFee: jpy(0)},
			{UpTo: 100, UnitPrice: jpy(5), FlatFee: jpy(0)},
		}, TieredPricingVolume},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ctorErr := NewTieredPrice(tc.tiers, tc.mode)
			valErr := TieredPrice{Tiers: tc.tiers, Mode: tc.mode}.Validate()
			if ctorErr == nil || valErr == nil {
				t.Fatalf("expected both constructor and Validate to fail, got ctor=%v validate=%v", ctorErr, valErr)
			}
			if ctorErr.Error() != valErr.Error() {
				t.Errorf("constructor and Validate disagree:\n  ctor:     %v\n  validate: %v", ctorErr, valErr)
			}
		})
	}
}

func TestUsagePrice_Validate(t *testing.T) {
	ptr := func(m shared.Money) *shared.Money { return &m }

	tests := []struct {
		name     string
		price    UsagePrice
		wantCode shared.ErrorCode // "" = expect nil error
	}{
		{
			name:  "no clamps",
			price: UsagePrice{UnitPrice: jpy(10)},
		},
		{
			name:  "matching clamps",
			price: UsagePrice{UnitPrice: jpy(10), Minimum: ptr(jpy(100)), Maximum: ptr(jpy(500))},
		},
		{
			name:     "mismatched minimum",
			price:    UsagePrice{UnitPrice: jpy(10), Minimum: ptr(usd(100))},
			wantCode: shared.ErrCodeCurrencyMismatch,
		},
		{
			name:     "mismatched maximum",
			price:    UsagePrice{UnitPrice: jpy(10), Maximum: ptr(usd(500))},
			wantCode: shared.ErrCodeCurrencyMismatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.price.Validate()
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var de *shared.DomainError
			if !errors.As(err, &de) || de.Code != tc.wantCode {
				t.Fatalf("expected DomainError with code %s, got %v", tc.wantCode, err)
			}
		})
	}
}

// TestUsagePrice_Validate_MatchesConstructor mirrors the tiered pin: the
// constructor and Validate must reject a wrong-currency clamp identically.
func TestUsagePrice_Validate_MatchesConstructor(t *testing.T) {
	badMax := usd(500)
	_, ctorErr := NewUsagePrice(jpy(10), nil, &badMax)
	valErr := UsagePrice{UnitPrice: jpy(10), Maximum: &badMax}.Validate()
	if ctorErr == nil || valErr == nil {
		t.Fatalf("expected both constructor and Validate to fail, got ctor=%v validate=%v", ctorErr, valErr)
	}
	if ctorErr.Error() != valErr.Error() {
		t.Errorf("constructor and Validate disagree:\n  ctor:     %v\n  validate: %v", ctorErr, valErr)
	}
}
