package pricing

import (
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

func BenchmarkFlatPrice_CalculatePrice(b *testing.B) {
	b.ReportAllocs()
	price := FlatPrice{
		Price: shared.NewMoney(big.NewRat(9800, 1), shared.CurrencyJPY),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = price.CalculatePrice(100)
	}
}

func BenchmarkTieredPrice_Graduated_10Tiers(b *testing.B) {
	b.ReportAllocs()
	tiers := make([]PriceTier, 10)
	for i := range tiers {
		tiers[i] = PriceTier{
			UpTo:      int64((i + 1) * 100),
			UnitPrice: shared.NewMoney(big.NewRat(int64(100-i*8), 1), shared.CurrencyJPY),
			FlatFee:   shared.Zero(shared.CurrencyJPY),
		}
	}
	// Last tier is unlimited
	tiers[9].UpTo = 0

	price, err := NewTieredPrice(tiers, TieredPricingGraduated)
	if err != nil {
		b.Fatalf("NewTieredPrice: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = price.CalculatePrice(750)
	}
}

func BenchmarkTieredPrice_Volume_10Tiers(b *testing.B) {
	b.ReportAllocs()
	tiers := make([]PriceTier, 10)
	for i := range tiers {
		tiers[i] = PriceTier{
			UpTo:      int64((i + 1) * 100),
			UnitPrice: shared.NewMoney(big.NewRat(int64(100-i*8), 1), shared.CurrencyJPY),
			FlatFee:   shared.Zero(shared.CurrencyJPY),
		}
	}
	tiers[9].UpTo = 0

	price, err := NewTieredPrice(tiers, TieredPricingVolume)
	if err != nil {
		b.Fatalf("NewTieredPrice: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = price.CalculatePrice(750)
	}
}

func BenchmarkUsagePrice_Simple(b *testing.B) {
	b.ReportAllocs()
	price := UsagePrice{
		UnitPrice: shared.NewMoney(big.NewRat(10, 1), shared.CurrencyJPY),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = price.CalculatePrice(5000)
	}
}

func BenchmarkUsagePrice_WithMinMax(b *testing.B) {
	b.ReportAllocs()
	min := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	max := shared.NewMoney(big.NewRat(100000, 1), shared.CurrencyJPY)
	price := UsagePrice{
		UnitPrice: shared.NewMoney(big.NewRat(10, 1), shared.CurrencyJPY),
		Minimum:   &min,
		Maximum:   &max,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = price.CalculatePrice(5000)
	}
}
