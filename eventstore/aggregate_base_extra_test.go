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
