package plugin

import (
	"context"
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestCalculationContext_SetSubtotal(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := NewCalculationContext(context.Background(), nil, subtotal)

	if ctx.Subtotal() != subtotal {
		t.Errorf("expected subtotal %v, got %v", subtotal, ctx.Subtotal())
	}

	newSubtotal := shared.NewMoney(big.NewRat(20000, 1), shared.CurrencyJPY)
	ctx.SetSubtotal(newSubtotal)

	if ctx.Subtotal() != newSubtotal {
		t.Errorf("expected subtotal %v after SetSubtotal, got %v", newSubtotal, ctx.Subtotal())
	}
}

func TestCalculationContext_RecordDiscount(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := NewCalculationContext(context.Background(), nil, subtotal)

	if discounts := ctx.AppliedDiscounts(); discounts != nil {
		t.Errorf("expected nil applied discounts initially, got %v", discounts)
	}

	discount := AppliedDiscount{
		PluginName: "test-plugin",
		Code:       "PROMO10",
		Amount:     shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
	}
	ctx.RecordDiscount(discount)

	discounts := ctx.AppliedDiscounts()
	if len(discounts) != 1 {
		t.Fatalf("expected 1 discount, got %d", len(discounts))
	}
	if discounts[0].PluginName != "test-plugin" {
		t.Errorf("expected plugin name %q, got %q", "test-plugin", discounts[0].PluginName)
	}
	if discounts[0].Code != "PROMO10" {
		t.Errorf("expected code %q, got %q", "PROMO10", discounts[0].Code)
	}
}

func TestCalculationContext_SetSubtotalAfterDiscount(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := NewCalculationContext(context.Background(), nil, subtotal)

	// Initially, subtotalAfterDiscount equals subtotal
	if ctx.SubtotalAfterDiscount() != subtotal {
		t.Errorf("expected initial subtotalAfterDiscount to equal subtotal")
	}

	afterDiscount := shared.NewMoney(big.NewRat(9000, 1), shared.CurrencyJPY)
	ctx.SetSubtotalAfterDiscount(afterDiscount)

	if ctx.SubtotalAfterDiscount() != afterDiscount {
		t.Errorf("expected subtotalAfterDiscount %v, got %v", afterDiscount, ctx.SubtotalAfterDiscount())
	}
}

func TestCalculationContext_Invoice(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := NewCalculationContext(context.Background(), nil, subtotal)

	if ctx.Invoice() != nil {
		t.Error("expected nil invoice initially")
	}
}

func TestCalculationContext_Context(t *testing.T) {
	bg := context.Background()
	ctx := NewCalculationContext(bg, nil, shared.Zero(shared.CurrencyJPY))

	if ctx.Context() != bg {
		t.Error("expected Context() to return the provided context")
	}
}

func TestContext_Metadata(t *testing.T) {
	ctx := NewContext(context.Background())

	if v, ok := ctx.GetMetadata("missing"); ok || v != nil {
		t.Errorf("expected nil/false for missing key, got %v/%v", v, ok)
	}

	ctx.SetMetadata("key", "value")
	v, ok := ctx.GetMetadata("key")
	if !ok {
		t.Error("expected ok=true for existing key")
	}
	if v != "value" {
		t.Errorf("expected %q, got %v", "value", v)
	}
}
