package pricing

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestPrice_Snapshot_RoundTrip(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	model := FlatPrice{Price: shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY)}

	snap := PriceSnapshot{
		ID:           shared.PriceID("price-42"),
		ProductID:    shared.ProductID("prod-1"),
		Amount:       shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY),
		Currency:     shared.CurrencyJPY,
		Interval:     Monthly(),
		PricingModel: model,
		Status:       PriceStatusArchived,
		CreatedAt:    createdAt,
	}

	p, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}

	// Critical: ID must be preserved exactly. NewPrice generates a fresh ULID.
	if p.ID() != shared.PriceID("price-42") {
		t.Errorf("ID not preserved: got %s want price-42", p.ID())
	}
	if p.ProductID() != shared.ProductID("prod-1") {
		t.Errorf("ProductID mismatch")
	}
	if p.Amount().Amount().Cmp(big.NewRat(5000, 1)) != 0 {
		t.Errorf("Amount mismatch")
	}
	if p.Currency() != shared.CurrencyJPY {
		t.Errorf("Currency mismatch")
	}
	if p.Status() != PriceStatusArchived {
		t.Errorf("Status not preserved: %s", p.Status())
	}
	if !p.CreatedAt().Equal(createdAt) {
		t.Errorf("CreatedAt mismatch")
	}

	// Round-trip
	out := p.ToSnapshot()
	if out.ID != shared.PriceID("price-42") {
		t.Errorf("round-trip lost ID")
	}
	if out.Status != PriceStatusArchived {
		t.Errorf("round-trip lost status")
	}
}

// TestPrice_FromSnapshot_RestoresArchivedDirectly verifies that we can
// reconstruct an archived Price without going through NewPrice → Archive().
func TestPrice_FromSnapshot_RestoresArchivedDirectly(t *testing.T) {
	t.Parallel()

	snap := PriceSnapshot{
		ID:        shared.PriceID("price-1"),
		ProductID: shared.ProductID("prod-1"),
		Amount:    shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		Currency:  shared.CurrencyJPY,
		Interval:  Monthly(),
		Status:    PriceStatusArchived,
		CreatedAt: time.Now(),
	}
	p, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}
	if p.Status() != PriceStatusArchived {
		t.Errorf("expected archived, got %s", p.Status())
	}
}

func TestPrice_FromSnapshot_ValidatesID(t *testing.T) {
	t.Parallel()

	if _, err := FromSnapshot(PriceSnapshot{}); err == nil {
		t.Error("expected error for empty ID")
	}
}
