package invoice

import (
	"errors"
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

func jpy(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func TestNewLineItem_NegativeQuantity(t *testing.T) {
	_, err := NewLineItem("li-1", "Test", -1, jpy(1000), jpy(1000), nil)
	if err == nil {
		t.Fatal("expected error for negative quantity, got nil")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("expected DomainError, got %T", err)
	}
	if domErr.Code != shared.ErrCodeValidation {
		t.Errorf("expected ErrCodeValidation, got %s", domErr.Code)
	}
}

func TestNewLineItem_ZeroQuantity(t *testing.T) {
	li, err := NewLineItem("li-1", "Free item", 0, jpy(0), jpy(0), nil)
	if err != nil {
		t.Fatalf("unexpected error for zero quantity: %v", err)
	}
	if li.Quantity() != 0 {
		t.Errorf("expected quantity 0, got %d", li.Quantity())
	}
}

func TestLineItem_WithPriceID(t *testing.T) {
	priceID := shared.PriceID("price-001")
	li, err := NewLineItem("li-1", "Monthly subscription", 1, jpy(1000), jpy(1000), nil,
		WithPriceID(priceID),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if li.PriceID() != priceID {
		t.Errorf("expected priceID %s, got %s", priceID, li.PriceID())
	}
}

func TestLineItem_PriceID_DefaultEmpty(t *testing.T) {
	li, err := NewLineItem("li-1", "Monthly subscription", 1, jpy(1000), jpy(1000), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if li.PriceID() != "" {
		t.Errorf("expected empty priceID by default, got %s", li.PriceID())
	}
}
