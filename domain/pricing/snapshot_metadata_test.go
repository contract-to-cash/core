package pricing

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Snapshot round-trip tests for Price metadata (issue #219). These live in a
// snapshot*_test.go file because ToSnapshot / FromSnapshot are restricted to
// domain/*/snapshot*.go by the forbidigo rule (issue #100).

func TestPriceSnapshot_RoundTrip_PreservesMetadata(t *testing.T) {
	t.Parallel()

	p := newMetadataTestPrice(t, WithMetadata(map[string]string{"creator_id": "user-42"}))

	snap := p.ToSnapshot()
	restored, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot failed: %v", err)
	}
	if got := restored.Metadata()["creator_id"]; got != "user-42" {
		t.Errorf("snapshot round-trip lost metadata: got %v", restored.Metadata())
	}

	// The snapshot map must be independent of both the source and the restored
	// entity (aliasing across the snapshot boundary would let an adapter mutate
	// the immutable Price).
	snap.Metadata["creator_id"] = "tampered"
	if got := p.Metadata()["creator_id"]; got != "user-42" {
		t.Errorf("snapshot mutation leaked into source Price: %q", got)
	}
	if got := restored.Metadata()["creator_id"]; got != "user-42" {
		t.Errorf("snapshot mutation leaked into restored Price: %q", got)
	}
}

func TestPriceSnapshot_LegacyWithoutMetadata_Loads(t *testing.T) {
	t.Parallel()

	// A snapshot persisted before issue #219 has no metadata (nil map).
	snap := PriceSnapshot{
		ID:           shared.PriceID("price-legacy-1"),
		ProductID:    shared.ProductID("prod-1"),
		Amount:       shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		Currency:     shared.CurrencyJPY,
		Interval:     Monthly(),
		PricingModel: &FlatPrice{},
		Status:       PriceStatusActive,
		CreatedAt:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	restored, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot failed for legacy snapshot without metadata: %v", err)
	}
	got := restored.Metadata()
	if got == nil || len(got) != 0 {
		t.Errorf("legacy snapshot must restore with empty metadata, got %v", got)
	}
}
