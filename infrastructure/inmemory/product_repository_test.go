package inmemory

import (
	"context"
	"testing"

	"github.com/contract-to-cash/core/domain/product"
	"github.com/contract-to-cash/core/domain/shared"
)

func TestInMemoryProductRepository_SaveAndFindByID(t *testing.T) {
	repo := NewInMemoryProductRepository()
	ctx := context.Background()

	p := product.NewProduct("Pro Plan", "Professional tier")
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
	if found.Name() != "Pro Plan" {
		t.Errorf("expected name 'Pro Plan', got %q", found.Name())
	}
}

func TestInMemoryProductRepository_FindByID_NotFound(t *testing.T) {
	repo := NewInMemoryProductRepository()
	ctx := context.Background()

	_, err := repo.FindByID(ctx, shared.ProductID("nonexistent"))
	if err == nil {
		t.Error("expected error for non-existent product")
	}
}
