package inmemory

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
)

func jpy(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func TestInMemoryPriceRepository_SaveAndFindByID(t *testing.T) {
	repo := NewInMemoryPriceRepository()
	ctx := context.Background()

	productID := shared.NewProductID()
	p := pricing.NewPrice(productID, jpy(1000), shared.CurrencyJPY, pricing.BillingCycleMonthly, nil, time.Now())
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found, err := repo.FindByID(ctx, p.ID())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found.ID() != p.ID() {
		t.Errorf("expected ID %s, got %s", p.ID(), found.ID())
	}
}

func TestInMemoryPriceRepository_FindByID_NotFound(t *testing.T) {
	repo := NewInMemoryPriceRepository()
	ctx := context.Background()

	_, err := repo.FindByID(ctx, shared.PriceID("nonexistent"))
	if err == nil {
		t.Error("expected error for non-existent price")
	}
}

func TestInMemoryPriceRepository_FindByProductID(t *testing.T) {
	repo := NewInMemoryPriceRepository()
	ctx := context.Background()

	productID := shared.NewProductID()
	p1 := pricing.NewPrice(productID, jpy(1000), shared.CurrencyJPY, pricing.BillingCycleMonthly, nil, time.Now())
	p2 := pricing.NewPrice(productID, jpy(10000), shared.CurrencyJPY, pricing.BillingCycleYearly, nil, time.Now())
	otherProduct := shared.NewProductID()
	p3 := pricing.NewPrice(otherProduct, jpy(500), shared.CurrencyJPY, pricing.BillingCycleMonthly, nil, time.Now())

	_ = repo.Save(ctx, p1)
	_ = repo.Save(ctx, p2)
	_ = repo.Save(ctx, p3)

	prices, err := repo.FindByProductID(ctx, productID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prices) != 2 {
		t.Errorf("expected 2 prices, got %d", len(prices))
	}
}

func TestInMemoryPriceRepository_FindActiveByProductID(t *testing.T) {
	repo := NewInMemoryPriceRepository()
	ctx := context.Background()

	productID := shared.NewProductID()
	active := pricing.NewPrice(productID, jpy(1000), shared.CurrencyJPY, pricing.BillingCycleMonthly, nil, time.Now())
	archived := pricing.NewPrice(productID, jpy(2000), shared.CurrencyJPY, pricing.BillingCycleMonthly, nil, time.Now())
	_ = archived.Archive()

	_ = repo.Save(ctx, active)
	_ = repo.Save(ctx, archived)

	prices, err := repo.FindActiveByProductID(ctx, productID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prices) != 1 {
		t.Errorf("expected 1 active price, got %d", len(prices))
	}
	if prices[0].ID() != active.ID() {
		t.Errorf("expected active price ID %s, got %s", active.ID(), prices[0].ID())
	}
}
