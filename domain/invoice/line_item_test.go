package invoice

import (
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

func jpy(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func TestNewLineItem_NegativeQuantity(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for negative quantity, got nil")
		}
	}()
	NewLineItem("li-1", "Test", -1, jpy(1000), jpy(1000), nil)
}

func TestLineItem_WithPriceID(t *testing.T) {
	priceID := shared.PriceID("price-001")
	li := NewLineItem("li-1", "Monthly subscription", 1, jpy(1000), jpy(1000), nil,
		WithPriceID(priceID),
	)

	if li.PriceID() != priceID {
		t.Errorf("expected priceID %s, got %s", priceID, li.PriceID())
	}
}

func TestLineItem_PriceID_DefaultEmpty(t *testing.T) {
	li := NewLineItem("li-1", "Monthly subscription", 1, jpy(1000), jpy(1000), nil)

	if li.PriceID() != "" {
		t.Errorf("expected empty priceID by default, got %s", li.PriceID())
	}
}
