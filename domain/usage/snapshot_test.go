package usage

import (
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestUsageRecord_Snapshot_RoundTrip(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	r, err := NewUsageRecord(
		shared.UsageRecordID("usg-1"),
		shared.ContractID("ctr-1"),
		shared.MetricName("api_calls"),
		1500,
		ts,
		"idem-1",
	)
	if err != nil {
		t.Fatalf("NewUsageRecord: %v", err)
	}

	snap := r.ToSnapshot()
	if snap.ID != shared.UsageRecordID("usg-1") {
		t.Errorf("Snapshot ID mismatch")
	}
	if snap.MetricName != shared.MetricName("api_calls") {
		t.Errorf("Snapshot MetricName mismatch")
	}

	restored, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}
	if restored.ID() != r.ID() {
		t.Errorf("ID mismatch")
	}
	if restored.ContractID() != r.ContractID() {
		t.Errorf("ContractID mismatch")
	}
	if restored.MetricName() != r.MetricName() {
		t.Errorf("MetricName mismatch")
	}
	if restored.Quantity() != 1500 {
		t.Errorf("Quantity mismatch")
	}
	if !restored.Timestamp().Equal(ts) {
		t.Errorf("Timestamp mismatch")
	}
	if restored.IdempotencyKey() != "idem-1" {
		t.Errorf("IdempotencyKey mismatch")
	}
}

// TestUsageRecord_FromSnapshot_DoesNotRerunQuantityValidation verifies that
// reconstitution accepts historical rows whose quantity would be rejected
// by NewUsageRecord today (e.g. negative quantities written by an earlier
// version of the system).
func TestUsageRecord_FromSnapshot_DoesNotRerunQuantityValidation(t *testing.T) {
	t.Parallel()

	snap := UsageRecordSnapshot{
		ID:         shared.UsageRecordID("usg-1"),
		ContractID: shared.ContractID("ctr-1"),
		MetricName: shared.MetricName("corrections"),
		Quantity:   -10, // negative: NewUsageRecord would reject this
		Timestamp:  time.Now(),
	}
	r, err := FromSnapshot(snap)
	if err != nil {
		t.Errorf("FromSnapshot should accept historical negative quantity: %v", err)
	}
	if r == nil || r.Quantity() != -10 {
		t.Errorf("quantity not preserved")
	}
}

func TestUsageRecord_FromSnapshot_ValidatesID(t *testing.T) {
	t.Parallel()

	if _, err := FromSnapshot(UsageRecordSnapshot{}); err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestUsageRecord_ToSnapshot_IsIndependentCopy(t *testing.T) {
	t.Parallel()

	r, _ := NewUsageRecord(
		shared.UsageRecordID("usg-1"),
		shared.ContractID("ctr-1"),
		shared.MetricName("m"),
		1,
		time.Now(),
		"k",
	)
	snap := r.ToSnapshot()
	snap.Metadata = map[string]string{"mutated": "yes"}

	if _, ok := r.Metadata()["mutated"]; ok {
		t.Error("ToSnapshot leaked metadata reference")
	}
}

// TestUsageRecord_PointerIndependence verifies Metadata map isolation by
// adding elements to the existing map (not reassigning it), in both
// ToSnapshot and FromSnapshot directions.
func TestUsageRecord_PointerIndependence(t *testing.T) {
	t.Parallel()

	srcMeta := map[string]string{"k": "v"}
	src := UsageRecordSnapshot{
		ID:         shared.UsageRecordID("usg-1"),
		ContractID: shared.ContractID("ctr-1"),
		MetricName: shared.MetricName("m"),
		Quantity:   1,
		Timestamp:  time.Now(),
		Metadata:   srcMeta,
	}
	r, err := FromSnapshot(src)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}

	// ToSnapshot: mutate snapshot's Metadata in place, entity must be unaffected.
	out := r.ToSnapshot()
	out.Metadata["leak"] = "yes"
	if _, leaked := r.Metadata()["leak"]; leaked {
		t.Error("ToSnapshot: Metadata map was shared")
	}

	// FromSnapshot: mutate the original source map, reconstructed entity
	// must be unaffected.
	srcMeta["leak-src"] = "yes"
	if _, leaked := r.Metadata()["leak-src"]; leaked {
		t.Error("FromSnapshot: Metadata map was shared")
	}
}
