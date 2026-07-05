package pricing

import (
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestFlatPrice_CalculatePrice(t *testing.T) {
	price := shared.NewMoney(new(big.Rat).SetInt64(5000), shared.CurrencyJPY)
	flat := FlatPrice{Price: price}

	result := flat.CalculatePrice(0)
	if result.Amount().Cmp(price.Amount()) != 0 {
		t.Errorf("expected %s, got %s", price.Amount().RatString(), result.Amount().RatString())
	}

	result = flat.CalculatePrice(100)
	if result.Amount().Cmp(price.Amount()) != 0 {
		t.Errorf("expected %s for usage=100, got %s", price.Amount().RatString(), result.Amount().RatString())
	}
}

func TestFlatPrice_NegativeUsagePanics(t *testing.T) {
	flat := FlatPrice{Price: shared.NewMoney(new(big.Rat).SetInt64(5000), shared.CurrencyJPY)}

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for negative usage, got none")
		}
	}()
	flat.CalculatePrice(-1)
}
