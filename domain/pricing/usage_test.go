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

	result := up.CalculatePrice(-5)
	if !result.IsZero() {
		t.Errorf("expected zero for negative usage, got %s", result.Amount().RatString())
	}
}

func TestUsagePrice_ZeroUsage(t *testing.T) {
	unitPrice := shared.NewMoney(new(big.Rat).SetInt64(10), shared.CurrencyJPY)
	up := UsagePrice{UnitPrice: unitPrice}

	result := up.CalculatePrice(0)
	if !result.IsZero() {
		t.Errorf("expected zero for zero usage, got %s", result.Amount().RatString())
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
