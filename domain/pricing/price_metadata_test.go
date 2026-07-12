package pricing

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Tests for integrator-defined Price metadata (issue #219): accepted only at
// construction time (Price is immutable), exposed via a defensively-copied
// getter, and carried through the snapshot round-trip.

func newMetadataTestPrice(t *testing.T, opts ...PriceOption) *Price {
	t.Helper()
	p, err := NewPriceWithInterval(
		shared.ProductID("prod-meta-1"),
		shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		shared.CurrencyJPY,
		Monthly(),
		&FlatPrice{},
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		opts...,
	)
	if err != nil {
		t.Fatalf("NewPriceWithInterval failed: %v", err)
	}
	return p
}

func TestNewPrice_WithMetadata_RoundTrip(t *testing.T) {
	t.Parallel()

	p := newMetadataTestPrice(t, WithMetadata(map[string]string{
		"creator_id": "user-42",
		"channel":    "self-serve",
	}))

	got := p.Metadata()
	if len(got) != 2 || got["creator_id"] != "user-42" || got["channel"] != "self-serve" {
		t.Errorf("Metadata() = %v, want creator_id=user-42 channel=self-serve", got)
	}
}

func TestNewPrice_LegacyConstructor_AcceptsMetadata(t *testing.T) {
	t.Parallel()

	p, err := NewPrice(
		shared.ProductID("prod-meta-2"),
		shared.NewMoney(big.NewRat(500, 1), shared.CurrencyJPY),
		shared.CurrencyJPY,
		BillingCycleMonthly,
		&FlatPrice{},
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		WithMetadata(map[string]string{"creator_id": "user-1"}),
	)
	if err != nil {
		t.Fatalf("NewPrice failed: %v", err)
	}
	if got := p.Metadata()["creator_id"]; got != "user-1" {
		t.Errorf("Metadata()[creator_id] = %q, want user-1", got)
	}
}

func TestPrice_Metadata_DefensiveCopies(t *testing.T) {
	t.Parallel()

	src := map[string]string{"creator_id": "user-42"}
	p := newMetadataTestPrice(t, WithMetadata(src))

	// Mutating the caller-owned input map after construction must not affect
	// the (immutable) Price.
	src["creator_id"] = "tampered"
	src["extra"] = "tampered"
	if got := p.Metadata(); got["creator_id"] != "user-42" || len(got) != 1 {
		t.Errorf("input-map mutation leaked into Price: %v", got)
	}

	// Mutating the getter result must not affect the Price either.
	out := p.Metadata()
	out["creator_id"] = "tampered"
	out["extra"] = "tampered"
	if got := p.Metadata(); got["creator_id"] != "user-42" || len(got) != 1 {
		t.Errorf("getter-result mutation leaked into Price: %v", got)
	}
}

func TestPrice_Metadata_EmptyByDefault(t *testing.T) {
	t.Parallel()

	p := newMetadataTestPrice(t)
	got := p.Metadata()
	if got == nil {
		t.Fatal("Metadata() must never return nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty metadata by default, got %v", got)
	}
}
