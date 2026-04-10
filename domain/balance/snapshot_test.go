package balance

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestBalanceEntry_Snapshot_RoundTrip(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	orig := shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY)

	// Build snapshot directly to exercise fields with no setter (sourceID, description).
	snap := BalanceEntrySnapshot{
		ID:              shared.BalanceEntryID("bal-1"),
		AccountID:       shared.AccountID("acc-1"),
		OriginalAmount:  orig,
		RemainingAmount: shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY),
		Reason:          BalanceReasonProration,
		SourceType:      BalanceSourceTypeProration,
		SourceID:        "proration-event-42",
		Description:     "Pro→Basic downgrade proration",
		ExpiresAt:       &expiresAt,
		CreatedAt:       createdAt,
		Version:         3,
	}

	e, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}

	if e.ID() != shared.BalanceEntryID("bal-1") {
		t.Errorf("ID mismatch")
	}
	if e.OriginalAmount().Amount().Cmp(big.NewRat(5000, 1)) != 0 {
		t.Errorf("OriginalAmount mismatch")
	}
	if e.RemainingAmount().Amount().Cmp(big.NewRat(3000, 1)) != 0 {
		t.Errorf("RemainingAmount mismatch")
	}
	if e.Reason() != BalanceReasonProration {
		t.Errorf("Reason mismatch")
	}
	if e.SourceType() != BalanceSourceTypeProration {
		t.Errorf("SourceType mismatch")
	}
	if e.SourceID() != "proration-event-42" {
		t.Errorf("SourceID not restored: %s", e.SourceID())
	}
	if e.Description() != "Pro→Basic downgrade proration" {
		t.Errorf("Description not restored: %s", e.Description())
	}
	if e.ExpiresAt() == nil || !e.ExpiresAt().Equal(expiresAt) {
		t.Errorf("ExpiresAt mismatch")
	}
	if !e.CreatedAt().Equal(createdAt) {
		t.Errorf("CreatedAt mismatch")
	}
	if e.Version() != 3 {
		t.Errorf("Version: got %d want 3", e.Version())
	}
	if e.LoadedVersion() != 3 {
		t.Errorf("LoadedVersion: got %d want 3", e.LoadedVersion())
	}

	// Round-trip: ToSnapshot should return the same state.
	out := e.ToSnapshot()
	if out.SourceID != "proration-event-42" {
		t.Errorf("ToSnapshot lost SourceID")
	}
	if out.Version != 3 {
		t.Errorf("ToSnapshot lost Version")
	}
}

// TestBalanceEntry_FromSnapshot_DoesNotIncrementVersion verifies that
// reconstitution preserves the version exactly (unlike Consume which
// increments it).
func TestBalanceEntry_FromSnapshot_DoesNotIncrementVersion(t *testing.T) {
	t.Parallel()

	snap := BalanceEntrySnapshot{
		ID:              shared.BalanceEntryID("bal-1"),
		AccountID:       shared.AccountID("acc-1"),
		OriginalAmount:  shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		RemainingAmount: shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		Reason:          BalanceReasonGoodwill,
		CreatedAt:       time.Now(),
		Version:         7,
	}
	e, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}
	if e.Version() != 7 || e.LoadedVersion() != 7 {
		t.Errorf("version/loadedVersion should be 7, got %d/%d", e.Version(), e.LoadedVersion())
	}
}

func TestBalanceEntry_FromSnapshot_ValidatesID(t *testing.T) {
	t.Parallel()

	if _, err := FromSnapshot(BalanceEntrySnapshot{}); err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestBalanceEntry_ToSnapshot_IsIndependentCopy(t *testing.T) {
	t.Parallel()

	e := NewBalanceEntry(
		shared.AccountID("acc-1"),
		shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		BalanceReasonGoodwill,
		time.Now(),
	)
	snap := e.ToSnapshot()
	snap.SourceID = "mutated"

	if e.SourceID() == "mutated" {
		t.Error("ToSnapshot leaked sourceID reference")
	}
}

// TestBalanceEntry_PointerIndependence verifies that ExpiresAt (*time.Time)
// is isolated at the Snapshot boundary, in both directions.
func TestBalanceEntry_PointerIndependence(t *testing.T) {
	t.Parallel()

	expires := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	snap := BalanceEntrySnapshot{
		ID:              shared.BalanceEntryID("bal-1"),
		AccountID:       shared.AccountID("acc-1"),
		OriginalAmount:  shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		RemainingAmount: shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		Reason:          BalanceReasonGoodwill,
		ExpiresAt:       &expires,
		CreatedAt:       time.Now(),
	}
	e, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}

	// ToSnapshot: mutate snapshot, entity must be unaffected.
	out := e.ToSnapshot()
	*out.ExpiresAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	if e.ExpiresAt() != nil && e.ExpiresAt().Year() == 2099 {
		t.Error("ToSnapshot: ExpiresAt pointer was shared")
	}

	// FromSnapshot: mutate original snapshot, reconstructed entity must be unaffected.
	*snap.ExpiresAt = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	if e.ExpiresAt() != nil && e.ExpiresAt().Year() == 2100 {
		t.Error("FromSnapshot: ExpiresAt pointer was shared")
	}
}
