package pricing

import (
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestUsagePrice_CalculatePrice(t *testing.T) {
	unitPrice := shared.NewMoney(new(big.Rat).SetInt64(10), shared.CurrencyJPY)
	up := UsagePrice{UnitPrice: unitPrice}

	// 5 * 10 = 50
	result := up.CalculatePrice(5)
	expected := new(big.Rat).SetInt64(50)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("expected 50, got %s", result.Amount().RatString())
	}
}

func TestUsagePrice_MinClamp(t *testing.T) {
	unitPrice := shared.NewMoney(new(big.Rat).SetInt64(10), shared.CurrencyJPY)
	min := shared.NewMoney(new(big.Rat).SetInt64(100), shared.CurrencyJPY)
	up := UsagePrice{UnitPrice: unitPrice, Minimum: &min}

	// 5 * 10 = 50, but min is 100
	result := up.CalculatePrice(5)
	expected := new(big.Rat).SetInt64(100)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("expected 100 (min clamp), got %s", result.Amount().RatString())
	}
}

func TestUsagePrice_MaxClamp(t *testing.T) {
	unitPrice := shared.NewMoney(new(big.Rat).SetInt64(10), shared.CurrencyJPY)
	max := shared.NewMoney(new(big.Rat).SetInt64(200), shared.CurrencyJPY)
	up := UsagePrice{UnitPrice: unitPrice, Maximum: &max}

	// 50 * 10 = 500, but max is 200
	result := up.CalculatePrice(50)
	expected := new(big.Rat).SetInt64(200)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("expected 200 (max clamp), got %s", result.Amount().RatString())
	}
}

func TestUsagePrice_NegativeUsage(t *testing.T) {
	unitPrice := shared.NewMoney(new(big.Rat).SetInt64(10), shared.CurrencyJPY)
	up := UsagePrice{UnitPrice: unitPrice}

	// Negative usage is a caller-side invariant violation; CalculatePrice must
	// panic rather than silently clamp to zero (issue #113).
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for negative usage, got none")
		}
	}()
	up.CalculatePrice(-5)
}

func TestUsagePrice_ZeroUsage(t *testing.T) {
	unitPrice := shared.NewMoney(new(big.Rat).SetInt64(10), shared.CurrencyJPY)
	up := UsagePrice{UnitPrice: unitPrice}

	result := up.CalculatePrice(0)
	if !result.IsZero() {
		t.Errorf("expected zero for zero usage, got %s", result.Amount().RatString())
	}
}

// TestUsagePrice_MinimumNotAppliedAtZeroUsage pins the documented semantics
// (issue #162 L-2): the Minimum floor applies only when usage > 0. A period with
// zero usage bills nothing, even when a Minimum is configured — the minimum is a
// floor on a used resource, not an unconditional periodic base charge.
func TestUsagePrice_MinimumNotAppliedAtZeroUsage(t *testing.T) {
	unitPrice := shared.NewMoney(new(big.Rat).SetInt64(10), shared.CurrencyJPY)
	min := shared.NewMoney(new(big.Rat).SetInt64(100), shared.CurrencyJPY)
	up := UsagePrice{UnitPrice: unitPrice, Minimum: &min}

	result := up.CalculatePrice(0)
	if !result.IsZero() {
		t.Errorf("expected zero (minimum must NOT apply at zero usage), got %s",
			result.Amount().RatString())
	}

	// Sanity: with usage > 0 below the floor, the minimum DOES apply.
	if got := up.CalculatePrice(5); got.Amount().Cmp(new(big.Rat).SetInt64(100)) != 0 {
		t.Errorf("expected minimum 100 to apply for usage=5, got %s", got.Amount().RatString())
	}
}

func TestUsagePrice_WithinBounds(t *testing.T) {
	unitPrice := shared.NewMoney(new(big.Rat).SetInt64(10), shared.CurrencyJPY)
	min := shared.NewMoney(new(big.Rat).SetInt64(50), shared.CurrencyJPY)
	max := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	up := UsagePrice{UnitPrice: unitPrice, Minimum: &min, Maximum: &max}

	// 20 * 10 = 200, within [50, 500]
	result := up.CalculatePrice(20)
	expected := new(big.Rat).SetInt64(200)
	if result.Amount().Cmp(expected) != 0 {
		t.Errorf("expected 200, got %s", result.Amount().RatString())
	}
}
