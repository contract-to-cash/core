package eventstore

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestBaseAggregate_ClockAndIncrementVersion(t *testing.T) {
	fixedTime := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	clock := shared.FixedClock{FixedTime: fixedTime}
	agg := NewBaseAggregate("agg-clock", clock)

	if agg.Clock() == nil {
		t.Fatal("expected non-nil clock")
	}
	if got := agg.Clock().Now(); got != fixedTime {
		t.Errorf("Clock().Now() = %v, want %v", got, fixedTime)
	}

	if agg.Version() != 0 {
		t.Fatalf("expected initial version 0, got %d", agg.Version())
	}
	agg.IncrementVersion()
	agg.IncrementVersion()
	if agg.Version() != 2 {
		t.Errorf("expected version 2 after two increments, got %d", agg.Version())
	}
}

// TestBaseAggregate_UncommittedEventsReturnsCopy verifies that mutating the
// slice returned by UncommittedEvents does not corrupt the aggregate's
// internal pending-event state (issue #246 nit).
func TestBaseAggregate_UncommittedEventsReturnsCopy(t *testing.T) {
	fixedTime := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	clock := shared.FixedClock{FixedTime: fixedTime}
	agg := NewBaseAggregate("agg-copy", clock)

	if err := agg.RaiseEvent(&testEvent{Name: "first"}, EventMetadata{}); err != nil {
		t.Fatalf("RaiseEvent failed: %v", err)
	}
	if err := agg.RaiseEvent(&testEvent{Name: "second"}, EventMetadata{}); err != nil {
		t.Fatalf("RaiseEvent failed: %v", err)
	}

	got := agg.UncommittedEvents()
	if len(got) != 2 {
		t.Fatalf("expected 2 uncommitted events, got %d", len(got))
	}

	// Mutate the returned slice: overwrite an element and append a bogus event.
	got[0] = Event{StreamID: "tampered", Type: "tampered.event", Version: 999}
	_ = append(got, Event{StreamID: "extra", Type: "extra.event", Version: 1000})

	fresh := agg.UncommittedEvents()
	if len(fresh) != 2 {
		t.Fatalf("internal slice length changed after caller mutation: got %d, want 2", len(fresh))
	}
	if fresh[0].StreamID != "agg-copy" || fresh[0].Type != "test.event" || fresh[0].Version != 1 {
		t.Errorf("internal event corrupted by caller mutation: %+v", fresh[0])
	}
	if fresh[1].Version != 2 {
		t.Errorf("expected second event version 2, got %d", fresh[1].Version)
	}

	// Empty case: a fresh aggregate returns nil/empty without aliasing concerns.
	empty := NewBaseAggregate("agg-empty", clock)
	if evs := empty.UncommittedEvents(); len(evs) != 0 {
		t.Errorf("expected no uncommitted events, got %d", len(evs))
	}
}

// TestEventRegistry_DeserializeInvalidJSON exercises the unmarshal error path.
func TestEventRegistry_DeserializeInvalidJSON(t *testing.T) {
	registry := NewEventRegistry()
	if err := registry.Register(&testEvent{}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	_, err := registry.Deserialize("test.event", json.RawMessage(`{invalid json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}
