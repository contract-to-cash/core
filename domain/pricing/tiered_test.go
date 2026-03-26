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
