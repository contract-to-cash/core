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
