package product

import (
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestProduct_Snapshot_RoundTrip(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	limit := int64(1000)

	snap := ProductSnapshot{
		ID:          shared.ProductID("prod-42"),
		Name:        "Pro Plan",
		Description: "Pro tier",
		Features: []Feature{
			{Name: "storage", Included: true, Limit: &limit},
		},
		UsageMetrics: []UsageMetric{
			{Name: shared.MetricName("api_calls"), IncludedQuantity: 10000},
		},
		Status:    ProductStatusArchived,
		Metadata:  map[string]string{"tier": "pro"},
		CreatedAt: createdAt,
	}

	p, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}

	// Critical: ID must be preserved exactly. NewProduct generates a fresh
	// ULID, so reconstitution requires explicitly preserving the stored ID.
	if p.ID() != shared.ProductID("prod-42") {
		t.Errorf("ID not preserved: got %s want prod-42", p.ID())
	}
	if p.Name() != "Pro Plan" {
		t.Errorf("Name mismatch")
	}
	if p.Status() != ProductStatusArchived {
		t.Errorf("Status mismatch: %s", p.Status())
	}
	if !p.CreatedAt().Equal(createdAt) {
		t.Errorf("CreatedAt mismatch")
	}
	if len(p.Features()) != 1 || p.Features()[0].Name != "storage" {
		t.Errorf("Features not restored")
	}
	if feat := p.Features()[0]; feat.Limit == nil || *feat.Limit != 1000 {
		t.Errorf("Feature.Limit not restored")
	}
	if len(p.UsageMetrics()) != 1 || p.UsageMetrics()[0].Name != shared.MetricName("api_calls") {
		t.Errorf("UsageMetrics not restored")
	}
	if p.Metadata()["tier"] != "pro" {
		t.Errorf("Metadata not restored")
	}

	// Round-trip
	out := p.ToSnapshot()
	if out.ID != shared.ProductID("prod-42") {
		t.Errorf("round-trip lost ID")
	}
	if out.Status != ProductStatusArchived {
		t.Errorf("round-trip lost status")
	}
}

// TestProduct_FromSnapshot_PreservesStableIDForRestoration verifies the
// primary motivation: NewProduct generates a fresh ULID, but FromSnapshot
// must honor the caller-supplied ID.
func TestProduct_FromSnapshot_PreservesStableIDForRestoration(t *testing.T) {
	t.Parallel()

	existingID := shared.ProductID("prod-existing-123")
	snap := ProductSnapshot{
		ID:        existingID,
		Name:      "x",
		Status:    ProductStatusActive,
		CreatedAt: time.Now(),
	}
	p, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}
	if p.ID() != existingID {
		t.Errorf("ID not preserved: got %s want %s", p.ID(), existingID)
	}
}

func TestProduct_FromSnapshot_ValidatesID(t *testing.T) {
	t.Parallel()

	if _, err := FromSnapshot(ProductSnapshot{}); err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestProduct_ToSnapshot_IsIndependentCopy(t *testing.T) {
	t.Parallel()

	p := NewProduct("x", "y", time.Now())
	p.AddFeature(Feature{Name: "f1", Included: true})
	p.SetMetadata("k", "v")

	snap := p.ToSnapshot()
	snap.Features[0].Name = "mutated"
	snap.Metadata["k"] = "changed"

	if p.Features()[0].Name != "f1" {
		t.Error("ToSnapshot leaked features reference")
	}
	if p.Metadata()["k"] != "v" {
		t.Error("ToSnapshot leaked metadata reference")
	}
}
