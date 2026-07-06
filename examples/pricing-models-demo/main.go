// pricing-models-demo compares four pricing models side by side:
// Flat, Graduated Tiered, Volume Tiered, and Usage-Based (with min/max).
//
// Run: go run ./examples/pricing-models-demo/
package main

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
)

func main() {
	fmt.Println("=== Pricing Models Demo ===")
	fmt.Println()

	// ── 1. Flat Pricing ──
	fmt.Println("--- 1. Flat Pricing ---")
	fmt.Println("  Model: ¥5,000/month (fixed, regardless of usage)")
	fmt.Println()

	flatModel := pricing.FlatPrice{Price: moneyJPY(5000)}

	usages := []int64{0, 100, 1000, 10000}
	fmt.Printf("  %-10s %s\n", "Usage", "Price")
	fmt.Printf("  %s\n", strings.Repeat("-", 22))
	for _, u := range usages {
		price := flatModel.CalculatePrice(u)
		fmt.Printf("  %-10d ¥%s\n", u, price.Amount().RatString())
	}
	fmt.Println()

	// ── 2. Graduated Tiered Pricing ──
	fmt.Println("--- 2. Graduated Tiered Pricing ---")
	fmt.Println("  Model: API calls pricing")
	fmt.Println("    Tier 1:    0 - 1,000 calls  @ ¥10/call")
	fmt.Println("    Tier 2: 1,001 - 5,000 calls  @ ¥8/call")
	fmt.Println("    Tier 3: 5,001+         calls  @ ¥5/call")
	fmt.Println()

	graduatedModel := mustTiered(pricing.NewTieredPrice(
		[]pricing.PriceTier{
			{UpTo: 1000, UnitPrice: moneyJPY(10), FlatFee: shared.Zero(shared.CurrencyJPY)},
			{UpTo: 5000, UnitPrice: moneyJPY(8), FlatFee: shared.Zero(shared.CurrencyJPY)},
			{UpTo: 0, UnitPrice: moneyJPY(5), FlatFee: shared.Zero(shared.CurrencyJPY)}, // 0 = unlimited
		},
		pricing.TieredPricingGraduated,
	))

	// ── 3. Volume Tiered Pricing ──
	fmt.Println("--- 3. Volume Tiered Pricing ---")
	fmt.Println("  Model: Same tiers, but the TOTAL usage determines the single rate")
	fmt.Println("    0 - 1,000 calls  -> all @ ¥10/call")
	fmt.Println("    1,001 - 5,000    -> all @ ¥8/call")
	fmt.Println("    5,001+           -> all @ ¥5/call")
	fmt.Println()

	volumeModel := mustTiered(pricing.NewTieredPrice(
		[]pricing.PriceTier{
			{UpTo: 1000, UnitPrice: moneyJPY(10), FlatFee: shared.Zero(shared.CurrencyJPY)},
			{UpTo: 5000, UnitPrice: moneyJPY(8), FlatFee: shared.Zero(shared.CurrencyJPY)},
			{UpTo: 0, UnitPrice: moneyJPY(5), FlatFee: shared.Zero(shared.CurrencyJPY)},
		},
		pricing.TieredPricingVolume,
	))

	// ── 4. Usage-Based Pricing (with min/max) ──
	fmt.Println("--- 4. Usage-Based Pricing ---")
	fmt.Println("  Model: Storage pricing")
	fmt.Println("    ¥50/GB, minimum ¥500/month, maximum ¥50,000/month")
	fmt.Println()

	minPrice := moneyJPY(500)
	maxPrice := moneyJPY(50000)
	usageModel := pricing.UsagePrice{
		UnitPrice: moneyJPY(50),
		Minimum:   &minPrice,
		Maximum:   &maxPrice,
	}

	// ── Comparison Table ──
	fmt.Println("=== Side-by-Side Comparison ===")
	fmt.Println()

	testUsages := []int64{0, 3, 100, 500, 1000, 2000, 3500, 5000, 8000, 10000}

	// Header
	fmt.Printf("  %-8s  %-12s  %-12s  %-12s  %-12s\n",
		"Usage", "Flat", "Graduated", "Volume", "Usage-Based")
	fmt.Printf("  %s\n", strings.Repeat("-", 62))

	for _, u := range testUsages {
		flat := flatModel.CalculatePrice(u)
		grad := graduatedModel.CalculatePrice(u)
		vol := volumeModel.CalculatePrice(u)
		usage := usageModel.CalculatePrice(u)

		fmt.Printf("  %-8d  ¥%-11s  ¥%-11s  ¥%-11s  ¥%-11s\n",
			u,
			flat.Amount().RatString(),
			grad.Amount().RatString(),
			vol.Amount().RatString(),
			usage.Amount().RatString())
	}

	// ── Detailed Graduated vs Volume Breakdown ──
	fmt.Println()
	fmt.Println("=== Graduated vs Volume: Detailed Breakdown for 3,500 calls ===")
	fmt.Println()
	fmt.Println("  Graduated Tiered:")
	fmt.Println("    Tier 1: 1,000 calls x ¥10 = ¥10,000")
	fmt.Println("    Tier 2: 2,500 calls x ¥8  = ¥20,000")
	fmt.Printf("    Total:                       ¥%s\n", graduatedModel.CalculatePrice(3500).Amount().RatString())
	fmt.Println()
	fmt.Println("  Volume Tiered:")
	fmt.Println("    3,500 falls in tier 2 (1,001-5,000)")
	fmt.Println("    All 3,500 calls x ¥8      = ¥28,000")
	fmt.Printf("    Total:                       ¥%s\n", volumeModel.CalculatePrice(3500).Amount().RatString())

	// ── Usage-Based Min/Max Demonstration ──
	fmt.Println()
	fmt.Println("=== Usage-Based: Min/Max Clamping ===")
	fmt.Println()
	clampCases := []struct {
		gb   int64
		note string
	}{
		{3, "3 GB x ¥50 = ¥150 -> clamped to minimum ¥500"},
		{100, "100 GB x ¥50 = ¥5,000 (within range)"},
		{2000, "2,000 GB x ¥50 = ¥100,000 -> clamped to maximum ¥50,000"},
	}
	for _, c := range clampCases {
		price := usageModel.CalculatePrice(c.gb)
		fmt.Printf("  %5d GB -> ¥%-8s  %s\n", c.gb, price.Amount().RatString(), c.note)
	}

	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}

func moneyJPY(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

// mustTiered unwraps a NewTieredPrice result, panicking on a configuration error.
// Demo code only — real callers should handle the error.
func mustTiered(tp pricing.TieredPrice, err error) pricing.TieredPrice {
	if err != nil {
		panic(err)
	}
	return tp
}
