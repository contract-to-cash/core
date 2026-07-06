package pricing

import (
	"errors"
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

func makeTiers() []PriceTier {
	return []PriceTier{
		{
			UpTo:      10,
			UnitPrice: shared.NewMoney(new(big.Rat).SetInt64(100), shared.CurrencyJPY),
			FlatFee:   shared.Zero(shared.CurrencyJPY),
		},
		{
			UpTo:      20,
			UnitPrice: shared.NewMoney(new(big.Rat).SetInt64(80), shared.CurrencyJPY),
			FlatFee:   shared.Zero(shared.CurrencyJPY),
		},
	}
}

// mustTieredPrice constructs a TieredPrice via the validating constructor and
// fails the test on error. Used by the calculation tests that assume a valid
// configuration.
func mustTieredPrice(t *testing.T, tiers []PriceTier, mode TieredPricingMode) TieredPrice {
	t.Helper()
	tp, err := NewTieredPrice(tiers, mode)
	if err != nil {
		t.Fatalf("NewTieredPrice(%v): unexpected error: %v", mode, err)
	}
	return tp
}

func TestTieredPrice_Graduated(t *testing.T) {
	tp := mustTieredPrice(t, makeTiers(), TieredPricingGraduated)

	// usage=15 -> 10*100 + 5*80 = 1400
	result := tp.CalculatePrice(15)
	expected := new(big.Rat).SetInt64(1400)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("graduated: expected 1400, got %s", result.Amount().RatString())
	}
}

func TestTieredPrice_Graduated_ExactBoundary(t *testing.T) {
	tp := mustTieredPrice(t, makeTiers(), TieredPricingGraduated)

	// usage=10 -> 10*100 = 1000
	result := tp.CalculatePrice(10)
	expected := new(big.Rat).SetInt64(1000)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("graduated boundary: expected 1000, got %s", result.Amount().RatString())
	}
}

// TestTieredPrice_Graduated_OverflowAboveLastFiniteTier guards review M2: when
// usage exceeds the capacity of all (finite) tiers, graduated pricing must still
// charge the overflow units at the last tier's rate rather than silently drop
// them (revenue loss). This mirrors volume mode's last-tier fallback.
func TestTieredPrice_Graduated_OverflowAboveLastFiniteTier(t *testing.T) {
	tp := mustTieredPrice(t, makeTiers(), TieredPricingGraduated) // tier1: <=10 @100, tier2: <=20 @80

	// usage=25 -> 10*100 + 10*80 + 5*80(overflow at last tier) = 1000+800+400 = 2200
	result := tp.CalculatePrice(25)
	expected := new(big.Rat).SetInt64(2200)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("graduated overflow: expected 2200, got %s", result.Amount().RatString())
	}
}

func TestTieredPrice_Volume(t *testing.T) {
	tp := mustTieredPrice(t, makeTiers(), TieredPricingVolume)

	// usage=15 -> falls in tier 2 (UpTo=20), 15*80 = 1200
	result := tp.CalculatePrice(15)
	expected := new(big.Rat).SetInt64(1200)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("volume: expected 1200, got %s", result.Amount().RatString())
	}
}

func TestTieredPrice_Volume_FirstTier(t *testing.T) {
	tp := mustTieredPrice(t, makeTiers(), TieredPricingVolume)

	// usage=5 -> falls in tier 1 (UpTo=10), 5*100 = 500
	result := tp.CalculatePrice(5)
	expected := new(big.Rat).SetInt64(500)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("volume first tier: expected 500, got %s", result.Amount().RatString())
	}
}

func TestTieredPrice_ZeroUsage(t *testing.T) {
	tp := mustTieredPrice(t, makeTiers(), TieredPricingGraduated)

	result := tp.CalculatePrice(0)
	if !result.IsZero() {
		t.Errorf("expected zero for zero usage, got %s", result.Amount().RatString())
	}
}

func TestTieredPrice_NegativeUsagePanics(t *testing.T) {
	tp := mustTieredPrice(t, makeTiers(), TieredPricingGraduated)

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for negative usage, got none")
		}
	}()
	tp.CalculatePrice(-1)
}

// --- NewTieredPrice validation (issue #156) ---

func TestNewTieredPrice_Valid_FiniteTiers(t *testing.T) {
	if _, err := NewTieredPrice(makeTiers(), TieredPricingGraduated); err != nil {
		t.Fatalf("unexpected error for valid finite tiers: %v", err)
	}
}

func TestNewTieredPrice_Valid_UnlimitedLastTier(t *testing.T) {
	tiers := []PriceTier{
		{UpTo: 10, UnitPrice: shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyJPY)},
		{UpTo: 0, UnitPrice: shared.NewMoney(big.NewRat(80, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyJPY)},
	}
	if _, err := NewTieredPrice(tiers, TieredPricingGraduated); err != nil {
		t.Fatalf("unexpected error for valid unlimited last tier: %v", err)
	}
}

func TestNewTieredPrice_EmptyTiers(t *testing.T) {
	_, err := NewTieredPrice(nil, TieredPricingGraduated)
	assertDomainErrorCode(t, err, shared.ErrCodeValidation)
}

func TestNewTieredPrice_UnknownMode(t *testing.T) {
	_, err := NewTieredPrice(makeTiers(), TieredPricingMode("bogus"))
	assertDomainErrorCode(t, err, shared.ErrCodeValidation)
}

func TestNewTieredPrice_UnsortedTiers(t *testing.T) {
	tiers := []PriceTier{
		{UpTo: 20, UnitPrice: shared.NewMoney(big.NewRat(80, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyJPY)},
		{UpTo: 10, UnitPrice: shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyJPY)},
	}
	_, err := NewTieredPrice(tiers, TieredPricingGraduated)
	assertDomainErrorCode(t, err, shared.ErrCodeValidation)
}

func TestNewTieredPrice_DuplicateUpTo(t *testing.T) {
	tiers := []PriceTier{
		{UpTo: 10, UnitPrice: shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyJPY)},
		{UpTo: 10, UnitPrice: shared.NewMoney(big.NewRat(80, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyJPY)},
	}
	_, err := NewTieredPrice(tiers, TieredPricingGraduated)
	assertDomainErrorCode(t, err, shared.ErrCodeValidation)
}

func TestNewTieredPrice_MidChainUnlimited(t *testing.T) {
	tiers := []PriceTier{
		{UpTo: 0, UnitPrice: shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyJPY)},
		{UpTo: 10, UnitPrice: shared.NewMoney(big.NewRat(80, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyJPY)},
	}
	_, err := NewTieredPrice(tiers, TieredPricingGraduated)
	assertDomainErrorCode(t, err, shared.ErrCodeValidation)
}

func TestNewTieredPrice_NegativeUpTo(t *testing.T) {
	tiers := []PriceTier{
		{UpTo: -5, UnitPrice: shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyJPY)},
	}
	_, err := NewTieredPrice(tiers, TieredPricingGraduated)
	assertDomainErrorCode(t, err, shared.ErrCodeValidation)
}

func TestNewTieredPrice_MixedUnitPriceCurrency(t *testing.T) {
	tiers := []PriceTier{
		{UpTo: 10, UnitPrice: shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyJPY)},
		{UpTo: 20, UnitPrice: shared.NewMoney(big.NewRat(80, 1), shared.CurrencyUSD), FlatFee: shared.Zero(shared.CurrencyJPY)},
	}
	_, err := NewTieredPrice(tiers, TieredPricingGraduated)
	assertDomainErrorCode(t, err, shared.ErrCodeCurrencyMismatch)
}

func TestNewTieredPrice_MixedFlatFeeCurrency(t *testing.T) {
	tiers := []PriceTier{
		{UpTo: 10, UnitPrice: shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyUSD)},
	}
	_, err := NewTieredPrice(tiers, TieredPricingGraduated)
	assertDomainErrorCode(t, err, shared.ErrCodeCurrencyMismatch)
}

// TestTieredPrice_CalculatePrice_MixedCurrencyPanics verifies that a TieredPrice
// built by bypassing NewTieredPrice with mixed-currency tiers panics loudly at
// CalculatePrice rather than silently billing zero (issue #156). Graduated mode
// crosses tiers at usage=15, forcing the cross-currency Money.Add.
func TestTieredPrice_CalculatePrice_MixedCurrencyPanics(t *testing.T) {
	tp := TieredPrice{
		Tiers: []PriceTier{
			{UpTo: 10, UnitPrice: shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY), FlatFee: shared.Zero(shared.CurrencyJPY)},
			{UpTo: 20, UnitPrice: shared.NewMoney(big.NewRat(80, 1), shared.CurrencyUSD), FlatFee: shared.Zero(shared.CurrencyUSD)},
		},
		Mode: TieredPricingGraduated,
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for mixed-currency tiers, got none")
		}
	}()
	tp.CalculatePrice(15)
}

func assertDomainErrorCode(t *testing.T, err error, code shared.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected domain error with code %q, got nil", code)
	}
	var de *shared.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected *shared.DomainError, got %T: %v", err, err)
	}
	if de.Code != code {
		t.Fatalf("expected code %q, got %q", code, de.Code)
	}
}
