package pricing

import (
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

func TestTieredPrice_Graduated(t *testing.T) {
	tp := TieredPrice{
		Tiers: makeTiers(),
		Mode:  TieredPricingGraduated,
	}

	// usage=15 -> 10*100 + 5*80 = 1400
	result := tp.CalculatePrice(15)
	expected := new(big.Rat).SetInt64(1400)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("graduated: expected 1400, got %s", result.Amount().RatString())
	}
}

func TestTieredPrice_Graduated_ExactBoundary(t *testing.T) {
	tp := TieredPrice{
		Tiers: makeTiers(),
		Mode:  TieredPricingGraduated,
	}

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
	tp := TieredPrice{
		Tiers: makeTiers(), // tier1: <=10 @100, tier2: <=20 @80
		Mode:  TieredPricingGraduated,
	}

	// usage=25 -> 10*100 + 10*80 + 5*80(overflow at last tier) = 1000+800+400 = 2200
	result := tp.CalculatePrice(25)
	expected := new(big.Rat).SetInt64(2200)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("graduated overflow: expected 2200, got %s", result.Amount().RatString())
	}
}

func TestTieredPrice_Volume(t *testing.T) {
	tp := TieredPrice{
		Tiers: makeTiers(),
		Mode:  TieredPricingVolume,
	}

	// usage=15 -> falls in tier 2 (UpTo=20), 15*80 = 1200
	result := tp.CalculatePrice(15)
	expected := new(big.Rat).SetInt64(1200)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("volume: expected 1200, got %s", result.Amount().RatString())
	}
}

func TestTieredPrice_Volume_FirstTier(t *testing.T) {
	tp := TieredPrice{
		Tiers: makeTiers(),
		Mode:  TieredPricingVolume,
	}

	// usage=5 -> falls in tier 1 (UpTo=10), 5*100 = 500
	result := tp.CalculatePrice(5)
	expected := new(big.Rat).SetInt64(500)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("volume first tier: expected 500, got %s", result.Amount().RatString())
	}
}

func TestTieredPrice_ZeroUsage(t *testing.T) {
	tp := TieredPrice{
		Tiers: makeTiers(),
		Mode:  TieredPricingGraduated,
	}

	result := tp.CalculatePrice(0)
	if !result.IsZero() {
		t.Errorf("expected zero for zero usage, got %s", result.Amount().RatString())
	}
}

func TestTieredPrice_NegativeUsagePanics(t *testing.T) {
	tp := TieredPrice{
		Tiers: makeTiers(),
		Mode:  TieredPricingGraduated,
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for negative usage, got none")
		}
	}()
	tp.CalculatePrice(-1)
}
