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

func TestAppend_RejectsNonContiguousVersion(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	// Gap: expectedVersion 0 requires the first event to be version 1, but it is 2.
	gap := makeEvent("stream-1", 2, time.Now().UTC())
	err := store.Append(ctx, "stream-1", []eventstore.Event{gap}, 0)
	if err == nil {
		t.Fatal("expected error for non-contiguous first event version, got nil")
	}
	domErr, ok := err.(*shared.DomainError)
	if !ok {
		t.Fatalf("expected DomainError, got %T", err)
	}
	if domErr.Code != shared.ErrCodeValidation {
		t.Errorf("expected error code %s, got %s", shared.ErrCodeValidation, domErr.Code)
	}

	// Nothing should have been persisted.
	events, err := store.Load(ctx, "stream-1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events after rejected append, got %d", len(events))
	}
}

func TestAppend_RejectsNonContiguousBatch(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	// First event 1 is fine, but the second jumps from 2 to 4.
	e1 := makeEvent("s", 1, time.Now().UTC())
	e2 := makeEvent("s", 3, time.Now().UTC())
	err := store.Append(ctx, "s", []eventstore.Event{e1, e2}, 0)
	if err == nil {
		t.Fatal("expected error for non-contiguous batch, got nil")
	}
	if domErr, ok := err.(*shared.DomainError); !ok || domErr.Code != shared.ErrCodeValidation {
		t.Fatalf("expected validation DomainError, got %v", err)
	}
}

func TestAppend_ContiguousAfterExisting(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	if err := store.Append(ctx, "s", []eventstore.Event{makeEvent("s", 1, time.Now().UTC())}, 0); err != nil {
		t.Fatalf("first append failed: %v", err)
	}
	// Continuing from version 1 with event version 2 is contiguous and valid.
	if err := store.Append(ctx, "s", []eventstore.Event{makeEvent("s", 2, time.Now().UTC())}, 1); err != nil {
		t.Fatalf("contiguous append failed: %v", err)
	}
	// But an event version that does not follow expectedVersion is rejected.
	if err := store.Append(ctx, "s", []eventstore.Event{makeEvent("s", 5, time.Now().UTC())}, 2); err == nil {
		t.Fatal("expected rejection of non-contiguous continuation, got nil")
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

// TestLoadSnapshot_ReturnsHighestVersionOutOfOrder verifies that LoadSnapshot
// selects the highest-Version snapshot even when a stale snapshot is appended
// after a newer one (e.g. a lagging rebuild worker). Returning the last-appended
// snapshot would resume reconstruction from an older version and replay events
// that predate it — silent corruption (issue #157).
func TestLoadSnapshot_ReturnsHighestVersionOutOfOrder(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	newer := eventstore.Snapshot{
		StreamID:  "stream-1",
		Version:   200,
		State:     json.RawMessage(`{"v":200}`),
		AsOf:      time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	stale := eventstore.Snapshot{
		StreamID:  "stream-1",
		Version:   100,
		State:     json.RawMessage(`{"v":100}`),
		AsOf:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	// Save the newer snapshot first, then the stale one out of order.
	if err := store.SaveSnapshot(ctx, newer); err != nil {
		t.Fatalf("SaveSnapshot(newer) failed: %v", err)
	}
	if err := store.SaveSnapshot(ctx, stale); err != nil {
		t.Fatalf("SaveSnapshot(stale) failed: %v", err)
	}

	loaded, err := store.LoadSnapshot(ctx, "stream-1")
	if err != nil {
		t.Fatalf("LoadSnapshot failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if loaded.Version != 200 {
		t.Errorf("expected highest version 200, got %d", loaded.Version)
	}
}

// TestLoadSnapshotBefore_HighestOutOfOrder verifies that LoadSnapshotBefore
// returns the latest snapshot (by CreatedAt) among those before the cutoff even
// when saved out of order, rather than the last appended one.
func TestLoadSnapshotBefore_HighestOutOfOrder(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	newer := eventstore.Snapshot{
		StreamID:  "s",
		Version:   7,
		State:     json.RawMessage(`{}`),
		AsOf:      time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	older := eventstore.Snapshot{
		StreamID:  "s",
		Version:   3,
		State:     json.RawMessage(`{}`),
		AsOf:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	// Out-of-order save: newer first, then older.
	_ = store.SaveSnapshot(ctx, newer)
	_ = store.SaveSnapshot(ctx, older)

	// Cutoff after both: the latest (newer, v7) must win, not the last appended.
	loaded, err := store.LoadSnapshotBefore(ctx, "s", time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LoadSnapshotBefore failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if loaded.Version != 7 {
		t.Errorf("expected version 7 (latest before cutoff), got %d", loaded.Version)
	}

	// Cutoff between the two: only the older snapshot qualifies.
	loaded, err = store.LoadSnapshotBefore(ctx, "s", time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LoadSnapshotBefore failed: %v", err)
	}
	if loaded == nil || loaded.Version != 3 {
		t.Fatalf("expected version 3 before Feb, got %v", loaded)
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

	// Append events to multiple streams in interleaved order.
	e1 := makeEvent("stream-A", 1, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	e2 := makeEvent("stream-A", 2, time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC))
	if err := store.Append(ctx, "stream-A", []eventstore.Event{e1, e2}, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	e3 := makeEvent("stream-B", 1, time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC))
	if err := store.Append(ctx, "stream-B", []eventstore.Event{e3}, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	e4 := makeEvent("stream-A", 3, time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC))
	if err := store.Append(ctx, "stream-A", []eventstore.Event{e4}, 2); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// LoadAll from position 0 should return all 4 events in global order.
	events, err := store.LoadAll(ctx, 0, 100)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	// Verify global position ordering.
	for i := 0; i < len(events)-1; i++ {
		if events[i].GlobalPosition >= events[i+1].GlobalPosition {
			t.Errorf("events not in global position order at index %d: %d >= %d",
				i, events[i].GlobalPosition, events[i+1].GlobalPosition)
		}
	}

	// Events should come from different streams.
	if events[0].StreamID != "stream-A" || events[2].StreamID != "stream-B" {
		t.Errorf("unexpected stream order: %v, %v", events[0].StreamID, events[2].StreamID)
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

	// fromPosition is exclusive: LoadAll(ctx, 2, 100) should return events after position 2.
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

func TestLoadAll_Pagination(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	// Create 5 events across 2 streams.
	for i := 1; i <= 3; i++ {
		e := makeEvent("s1", i, time.Date(2025, 1, i, 0, 0, 0, 0, time.UTC))
		if err := store.Append(ctx, "s1", []eventstore.Event{e}, i-1); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}
	for i := 1; i <= 2; i++ {
		e := makeEvent("s2", i, time.Date(2025, 2, i, 0, 0, 0, 0, time.UTC))
		if err := store.Append(ctx, "s2", []eventstore.Event{e}, i-1); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// Page 1: first 2 events.
	page1, err := store.LoadAll(ctx, 0, 2)
	if err != nil {
		t.Fatalf("LoadAll page1 failed: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 events, got %d", len(page1))
	}
	if page1[0].GlobalPosition != 1 || page1[1].GlobalPosition != 2 {
		t.Errorf("page1 positions: got %d, %d", page1[0].GlobalPosition, page1[1].GlobalPosition)
	}

	// Page 2: next 2 events.
	page2, err := store.LoadAll(ctx, page1[1].GlobalPosition, 2)
	if err != nil {
		t.Fatalf("LoadAll page2 failed: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2 events, got %d", len(page2))
	}
	if page2[0].GlobalPosition != 3 || page2[1].GlobalPosition != 4 {
		t.Errorf("page2 positions: got %d, %d", page2[0].GlobalPosition, page2[1].GlobalPosition)
	}

	// Page 3: last event.
	page3, err := store.LoadAll(ctx, page2[1].GlobalPosition, 2)
	if err != nil {
		t.Fatalf("LoadAll page3 failed: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("expected 1 event, got %d", len(page3))
	}
	if page3[0].GlobalPosition != 5 {
		t.Errorf("page3 position: got %d", page3[0].GlobalPosition)
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

	// Limit to 3 events.
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

func TestLoadAll_NoLimit(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		e := makeEvent("s", i, time.Date(2025, 1, i, 0, 0, 0, 0, time.UTC))
		if err := store.Append(ctx, "s", []eventstore.Event{e}, i-1); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// limit=0 means no limit — should return all events.
	events, err := store.LoadAll(ctx, 0, 0)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events with limit=0 (no limit), got %d", len(events))
	}

	// limit=-1 also means no limit.
	events, err = store.LoadAll(ctx, 0, -1)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events with limit=-1 (no limit), got %d", len(events))
	}

	// With fromPosition, still returns remaining events.
	events, err = store.LoadAll(ctx, 3, 0)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events from position 3 with no limit, got %d", len(events))
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
