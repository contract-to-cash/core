package inmemory

import (
	"context"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/product"
	"github.com/contract-to-cash/core/domain/shared"
)

func TestInMemoryProductRepository_SaveAndFindByID(t *testing.T) {
	repo := NewInMemoryProductRepository()
	ctx := context.Background()

	p := product.NewProduct("Pro Plan", "Professional tier", time.Now())
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

// TestInMemoryProductRepository_PointerIsolation verifies that the repository
// does not share raw pointers (issue #197 / #152 discipline): mutating a product
// returned by FindByID, or the one passed to Save after saving, must not affect
// the stored state.
func TestInMemoryProductRepository_PointerIsolation(t *testing.T) {
	repo := NewInMemoryProductRepository()
	ctx := context.Background()

	p := product.NewProduct("Base", "desc", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p.AddUsageMetric(product.UsageMetric{Name: "calls", IncludedQuantity: 10})
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Mutating the passed-in pointer after Save must not leak into the store.
	p.AddUsageMetric(product.UsageMetric{Name: "leaked-via-save", IncludedQuantity: 99})

	got, err := repo.FindByID(ctx, p.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if len(got.UsageMetrics()) != 1 {
		t.Errorf("post-Save caller mutation leaked into store: got %d metrics, want 1", len(got.UsageMetrics()))
	}

	// Mutating a returned copy must not leak into the store either.
	got.AddUsageMetric(product.UsageMetric{Name: "leaked-via-find", IncludedQuantity: 42})
	again, err := repo.FindByID(ctx, p.ID())
	if err != nil {
		t.Fatalf("FindByID again: %v", err)
	}
	if len(again.UsageMetrics()) != 1 {
		t.Errorf("returned-copy mutation leaked into store: got %d metrics, want 1", len(again.UsageMetrics()))
	}
}
