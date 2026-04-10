package inmemory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

func makeEvent(streamID string, version int, occurredAt time.Time) eventstore.Event {
	return eventstore.Event{
		ID:         shared.GenerateID(),
		StreamID:   streamID,
		Type:       "test_event",
		Version:    version,
		Data:       json.RawMessage(`{"key":"value"}`),
		OccurredAt: occurredAt,
	}
}

func TestAppendAndLoad(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	e1 := makeEvent("stream-1", 1, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	e2 := makeEvent("stream-1", 2, time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC))

	if err := store.Append(ctx, "stream-1", []eventstore.Event{e1, e2}, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	events, err := store.Load(ctx, "stream-1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].RecordedAt.IsZero() {
		t.Error("RecordedAt should be set by Append")
	}
}

func TestOptimisticLocking(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	e1 := makeEvent("stream-1", 1, time.Now().UTC())
	if err := store.Append(ctx, "stream-1", []eventstore.Event{e1}, 0); err != nil {
		t.Fatalf("first Append failed: %v", err)
	}

	// Second append with same expectedVersion should fail.
	e2 := makeEvent("stream-1", 2, time.Now().UTC())
	err := store.Append(ctx, "stream-1", []eventstore.Event{e2}, 0)
	if err == nil {
		t.Fatal("expected version conflict error, got nil")
	}

	domErr, ok := err.(*shared.DomainError)
	if !ok {
		t.Fatalf("expected DomainError, got %T", err)
	}
	if domErr.Code != shared.ErrCodeVersionConflict {
		t.Errorf("expected error code %s, got %s", shared.ErrCodeVersionConflict, domErr.Code)
	}
}

func TestLoadUntil(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

	events := []eventstore.Event{
		makeEvent("s", 1, t1),
		makeEvent("s", 2, t2),
		makeEvent("s", 3, t3),
	}
	if err := store.Append(ctx, "s", events, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// LoadUntil t2 should return events at t1 and t2.
	result, err := store.LoadUntil(ctx, "s", t2)
	if err != nil {
		t.Fatalf("LoadUntil failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 events, got %d", len(result))
	}
}

func TestLoadRange(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

	events := []eventstore.Event{
		makeEvent("s", 1, t1),
		makeEvent("s", 2, t2),
		makeEvent("s", 3, t3),
	}
	if err := store.Append(ctx, "s", events, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// LoadRange [t1, t3) should return events at t1 and t2.
	result, err := store.LoadRange(ctx, "s", t1, t3)
	if err != nil {
		t.Fatalf("LoadRange failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 events, got %d", len(result))
	}

	// LoadRange [t2, t3) should return only event at t2.
	result, err = store.LoadRange(ctx, "s", t2, t3)
	if err != nil {
		t.Fatalf("LoadRange failed: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
}

func TestSaveAndLoadSnapshot(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	snap := eventstore.Snapshot{
		StreamID:  "stream-1",
		Version:   5,
		State:     json.RawMessage(`{"status":"active"}`),
		AsOf:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	if err := store.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveSnapshot failed: %v", err)
	}

	loaded, err := store.LoadSnapshot(ctx, "stream-1")
	if err != nil {
		t.Fatalf("LoadSnapshot failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if loaded.Version != 5 {
		t.Errorf("expected version 5, got %d", loaded.Version)
	}
}

func TestAppend_SetsGlobalPosition(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	// Append to stream-1
	e1 := makeEvent("stream-1", 1, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	e2 := makeEvent("stream-1", 2, time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC))
	if err := store.Append(ctx, "stream-1", []eventstore.Event{e1, e2}, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Append to stream-2
	e3 := makeEvent("stream-2", 1, time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC))
	if err := store.Append(ctx, "stream-2", []eventstore.Event{e3}, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Load stream-1 and verify GlobalPosition is set incrementally
	events, err := store.Load(ctx, "stream-1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if events[0].GlobalPosition != 1 {
		t.Errorf("expected GlobalPosition 1, got %d", events[0].GlobalPosition)
	}
	if events[1].GlobalPosition != 2 {
		t.Errorf("expected GlobalPosition 2, got %d", events[1].GlobalPosition)
	}

	// Load stream-2 and verify GlobalPosition continues from stream-1
	events2, err := store.Load(ctx, "stream-2")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if events2[0].GlobalPosition != 3 {
		t.Errorf("expected GlobalPosition 3, got %d", events2[0].GlobalPosition)
	}
}

func TestLoadAll_AcrossStreams(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	// Append events to multiple streams
	e1 := makeEvent("stream-1", 1, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	e2 := makeEvent("stream-1", 2, time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC))
	if err := store.Append(ctx, "stream-1", []eventstore.Event{e1, e2}, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	e3 := makeEvent("stream-2", 1, time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC))
	e4 := makeEvent("stream-2", 2, time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC))
	if err := store.Append(ctx, "stream-2", []eventstore.Event{e3, e4}, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// LoadAll from position 0 should return all 4 events in global order
	events, err := store.LoadAll(ctx, 0, 100)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	// Verify global position ordering
	for i := 0; i < len(events)-1; i++ {
		if events[i].GlobalPosition >= events[i+1].GlobalPosition {
			t.Errorf("events not in global position order at index %d: %d >= %d",
				i, events[i].GlobalPosition, events[i+1].GlobalPosition)
		}
	}
}

func TestLoadAll_FromPosition(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	e1 := makeEvent("s1", 1, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	e2 := makeEvent("s1", 2, time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC))
	e3 := makeEvent("s2", 1, time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC))
	if err := store.Append(ctx, "s1", []eventstore.Event{e1, e2}, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := store.Append(ctx, "s2", []eventstore.Event{e3}, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// fromPosition is exclusive: LoadAll(ctx, 2, 100) should return events after position 2
	events, err := store.LoadAll(ctx, 2, 100)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].GlobalPosition != 3 {
		t.Errorf("expected GlobalPosition 3, got %d", events[0].GlobalPosition)
	}
}

func TestLoadAll_WithLimit(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		e := makeEvent("s", i, time.Date(2025, 1, i, 0, 0, 0, 0, time.UTC))
		if err := store.Append(ctx, "s", []eventstore.Event{e}, i-1); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// Limit to 3 events
	events, err := store.LoadAll(ctx, 0, 3)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[2].GlobalPosition != 3 {
		t.Errorf("expected last event GlobalPosition 3, got %d", events[2].GlobalPosition)
	}
}

func TestLoadAll_EmptyStore(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	events, err := store.LoadAll(ctx, 0, 100)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestLoadSnapshotBefore(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	snap1 := eventstore.Snapshot{
		StreamID:  "s",
		Version:   3,
		State:     json.RawMessage(`{}`),
		AsOf:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	snap2 := eventstore.Snapshot{
		StreamID:  "s",
		Version:   7,
		State:     json.RawMessage(`{}`),
		AsOf:      time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
	}

	_ = store.SaveSnapshot(ctx, snap1)
	_ = store.SaveSnapshot(ctx, snap2)

	// Before Feb should return snap1.
	loaded, err := store.LoadSnapshotBefore(ctx, "s", time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LoadSnapshotBefore failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if loaded.Version != 3 {
		t.Errorf("expected version 3, got %d", loaded.Version)
	}

	// Before April should return snap2.
	loaded, err = store.LoadSnapshotBefore(ctx, "s", time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LoadSnapshotBefore failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if loaded.Version != 7 {
		t.Errorf("expected version 7, got %d", loaded.Version)
	}

	// Before Jan should return nil.
	loaded, err = store.LoadSnapshotBefore(ctx, "s", time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LoadSnapshotBefore failed: %v", err)
	}
	if loaded != nil {
		t.Errorf("expected nil, got version %d", loaded.Version)
	}
}
