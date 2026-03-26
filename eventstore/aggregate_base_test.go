package eventstore

import (
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

type sampleEvent struct {
	Message string `json:"message"`
}

func (e *sampleEvent) EventType() EventType { return "sample.created" }

func TestBaseAggregate_RaiseEvent(t *testing.T) {
	fixedTime := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	clock := shared.FixedClock{FixedTime: fixedTime}

	agg := NewBaseAggregate("agg-1", clock)
	if agg.ID() != "agg-1" {
		t.Errorf("expected ID agg-1, got %s", agg.ID())
	}
	if agg.Version() != 0 {
		t.Errorf("expected version 0, got %d", agg.Version())
	}

	metadata := EventMetadata{UserID: "user-1"}
	err := agg.RaiseEvent(&sampleEvent{Message: "hello"}, metadata)
	if err != nil {
		t.Fatal(err)
	}

	// Version() should remain 0 (uncommitted version) — committed version is updated only on persist
	if agg.Version() != 0 {
		t.Errorf("expected version 0 after RaiseEvent (uncommitted), got %d", agg.Version())
	}

	events := agg.UncommittedEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 uncommitted event, got %d", len(events))
	}

	evt := events[0]
	if evt.StreamID != "agg-1" {
		t.Errorf("expected StreamID agg-1, got %s", evt.StreamID)
	}
	if evt.Type != "sample.created" {
		t.Errorf("expected type sample.created, got %s", evt.Type)
	}
	if evt.Version != 1 {
		t.Errorf("expected event version 1, got %d", evt.Version)
	}
	if evt.SchemaVersion != 1 {
		t.Errorf("expected schema version 1, got %d", evt.SchemaVersion)
	}
	if evt.OccurredAt != fixedTime {
		t.Errorf("expected OccurredAt %v, got %v", fixedTime, evt.OccurredAt)
	}
	if evt.Metadata.UserID != "user-1" {
		t.Errorf("expected UserID user-1, got %s", evt.Metadata.UserID)
	}
	if evt.ID == "" {
		t.Error("expected non-empty event ID")
	}
}

func TestBaseAggregate_MultipleEvents(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Now().UTC()}
	agg := NewBaseAggregate("agg-2", clock)

	metadata := EventMetadata{UserID: "user-1"}
	_ = agg.RaiseEvent(&sampleEvent{Message: "first"}, metadata)
	_ = agg.RaiseEvent(&sampleEvent{Message: "second"}, metadata)

	// Version() stays 0 — uncommitted events don't bump the committed version
	if agg.Version() != 0 {
		t.Errorf("expected version 0 after RaiseEvent (uncommitted), got %d", agg.Version())
	}
	if len(agg.UncommittedEvents()) != 2 {
		t.Errorf("expected 2 events, got %d", len(agg.UncommittedEvents()))
	}
}

func TestBaseAggregate_ClearUncommittedEvents(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Now().UTC()}
	agg := NewBaseAggregate("agg-3", clock)
	metadata := EventMetadata{UserID: "user-1"}
	_ = agg.RaiseEvent(&sampleEvent{Message: "test"}, metadata)

	agg.ClearUncommittedEvents()
	if len(agg.UncommittedEvents()) != 0 {
		t.Error("expected 0 uncommitted events after clear")
	}
	// version should remain 0 — RaiseEvent does not increment committed version
	if agg.Version() != 0 {
		t.Errorf("expected version 0 after clear, got %d", agg.Version())
	}
}

func TestBaseAggregate_SetVersion(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Now().UTC()}
	agg := NewBaseAggregate("agg-4", clock)
	agg.SetVersion(50)
	if agg.Version() != 50 {
		t.Errorf("expected version 50, got %d", agg.Version())
	}
}
