package projection

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/contract-to-cash/core/eventstore"
)

// --- Mock implementations ---

type mockEventStore struct {
	events []eventstore.Event
}

func (m *mockEventStore) Append(_ context.Context, _ string, _ []eventstore.Event, _ int) error {
	return nil
}
func (m *mockEventStore) Load(_ context.Context, _ string) ([]eventstore.Event, error) {
	return nil, nil
}
func (m *mockEventStore) LoadUntilVersion(_ context.Context, _ string, _ int) ([]eventstore.Event, error) {
	return nil, nil
}
func (m *mockEventStore) LoadUntil(_ context.Context, _ string, _ time.Time) ([]eventstore.Event, error) {
	return nil, nil
}
func (m *mockEventStore) LoadRange(_ context.Context, _ string, _, _ time.Time) ([]eventstore.Event, error) {
	return nil, nil
}
func (m *mockEventStore) LoadAll(_ context.Context, fromPosition int64, limit int) ([]eventstore.Event, error) {
	var result []eventstore.Event
	for _, e := range m.events {
		if e.GlobalPosition > fromPosition {
			result = append(result, e)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].GlobalPosition < result[j].GlobalPosition
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
func (m *mockEventStore) Subscribe(_ context.Context, _ int64) (<-chan eventstore.Event, error) {
	ch := make(chan eventstore.Event, len(m.events))
	for _, e := range m.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}
func (m *mockEventStore) SaveSnapshot(_ context.Context, _ eventstore.Snapshot) error { return nil }
func (m *mockEventStore) LoadSnapshot(_ context.Context, _ string) (*eventstore.Snapshot, error) {
	return nil, nil
}
func (m *mockEventStore) LoadSnapshotBefore(_ context.Context, _ string, _ time.Time) (*eventstore.Snapshot, error) {
	return nil, nil
}

// --- Mock projector ---

type mockProjector struct {
	err error
}

func (m *mockProjector) Project(_ context.Context, _ eventstore.Event) error {
	return m.err
}
func (m *mockProjector) Rebuild(_ context.Context, _ time.Time) error {
	return nil
}

func TestProjectionService_AsyncMode_LogsError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	projErr := fmt.Errorf("projector database write failed")
	es := &mockEventStore{
		events: []eventstore.Event{
			{StreamID: "test-stream", Type: "TestEvent", Version: 1},
		},
	}

	svc := NewProjectionService(es, ProjectionOptions{
		SyncMode:   false,
		MaxRetries: 1,
		Logger:     logger,
	})
	svc.RegisterProjector(&mockProjector{err: projErr})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start processes events from the channel; channel is closed so Start will return nil
	err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := buf.String()
	if logOutput == "" {
		t.Fatal("expected log output for async projection error, got nothing")
	}
	if !strings.Contains(logOutput, "projection failed") {
		t.Errorf("log output missing expected content, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "TestEvent") {
		t.Errorf("log output should contain event type, got: %s", logOutput)
	}
}

func TestProjectionService_SyncMode_StillReturnsError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	projErr := fmt.Errorf("projector database write failed")
	es := &mockEventStore{
		events: []eventstore.Event{
			{StreamID: "test-stream", Type: "TestEvent", Version: 1},
		},
	}

	svc := NewProjectionService(es, ProjectionOptions{
		SyncMode:   true,
		MaxRetries: 1,
		Logger:     logger,
	})
	svc.RegisterProjector(&mockProjector{err: projErr})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := svc.Start(ctx)
	if err == nil {
		t.Fatal("expected error in sync mode")
	}
}

// --- Recording projector for RebuildAll tests ---

type recordingProjector struct {
	projected []eventstore.Event
	err       error
}

func (r *recordingProjector) Project(_ context.Context, event eventstore.Event) error {
	if r.err != nil {
		return r.err
	}
	r.projected = append(r.projected, event)
	return nil
}

func (r *recordingProjector) Rebuild(_ context.Context, _ time.Time) error {
	return nil
}

func TestProjectionService_RebuildAll_ProjectsAllEvents(t *testing.T) {
	es := &mockEventStore{
		events: []eventstore.Event{
			{StreamID: "contract-1", Type: "contract.created", Version: 1, GlobalPosition: 1},
			{StreamID: "contract-1", Type: "contract.activated", Version: 2, GlobalPosition: 2},
			{StreamID: "contract-2", Type: "contract.created", Version: 1, GlobalPosition: 3},
		},
	}

	svc := NewProjectionService(es, ProjectionOptions{
		MaxRetries: 1,
		BatchSize:  100,
	})

	proj := &recordingProjector{}
	svc.RegisterProjector(proj)

	ctx := context.Background()
	if err := svc.RebuildAll(ctx); err != nil {
		t.Fatalf("RebuildAll failed: %v", err)
	}

	if len(proj.projected) != 3 {
		t.Fatalf("expected 3 projected events, got %d", len(proj.projected))
	}

	// Verify order matches global position
	if proj.projected[0].Type != "contract.created" || proj.projected[0].StreamID != "contract-1" {
		t.Errorf("unexpected first event: %v", proj.projected[0])
	}
	if proj.projected[2].Type != "contract.created" || proj.projected[2].StreamID != "contract-2" {
		t.Errorf("unexpected third event: %v", proj.projected[2])
	}
}

func TestProjectionService_RebuildAll_Pagination(t *testing.T) {
	// Create 5 events, use BatchSize=2 to verify pagination works
	events := make([]eventstore.Event, 5)
	for i := range events {
		events[i] = eventstore.Event{
			StreamID:       "s1",
			Type:           "test.event",
			Version:        i + 1,
			GlobalPosition: int64(i + 1),
		}
	}

	// mockEventStore that returns events respecting fromPosition and limit
	es := &paginatingMockEventStore{events: events}

	svc := NewProjectionService(es, ProjectionOptions{
		MaxRetries: 1,
		BatchSize:  2,
	})

	proj := &recordingProjector{}
	svc.RegisterProjector(proj)

	ctx := context.Background()
	if err := svc.RebuildAll(ctx); err != nil {
		t.Fatalf("RebuildAll failed: %v", err)
	}

	if len(proj.projected) != 5 {
		t.Fatalf("expected 5 projected events, got %d", len(proj.projected))
	}
}

func TestProjectionService_RebuildAll_EmptyStore(t *testing.T) {
	es := &mockEventStore{events: nil}

	svc := NewProjectionService(es, ProjectionOptions{
		MaxRetries: 1,
		BatchSize:  100,
	})

	proj := &recordingProjector{}
	svc.RegisterProjector(proj)

	ctx := context.Background()
	if err := svc.RebuildAll(ctx); err != nil {
		t.Fatalf("RebuildAll failed: %v", err)
	}

	if len(proj.projected) != 0 {
		t.Fatalf("expected 0 projected events, got %d", len(proj.projected))
	}
}

func TestProjectionService_RebuildAll_ProjectorError(t *testing.T) {
	es := &mockEventStore{
		events: []eventstore.Event{
			{StreamID: "s1", Type: "test.event", Version: 1, GlobalPosition: 1},
		},
	}

	svc := NewProjectionService(es, ProjectionOptions{
		MaxRetries: 1,
		BatchSize:  100,
	})

	projErr := fmt.Errorf("projector write failed")
	svc.RegisterProjector(&mockProjector{err: projErr})

	ctx := context.Background()
	err := svc.RebuildAll(ctx)
	if err == nil {
		t.Fatal("expected error from RebuildAll")
	}
}

// paginatingMockEventStore respects fromPosition and limit in LoadAll.
type paginatingMockEventStore struct {
	mockEventStore
	events []eventstore.Event
}

func (m *paginatingMockEventStore) LoadAll(_ context.Context, fromPosition int64, limit int) ([]eventstore.Event, error) {
	var result []eventstore.Event
	for _, e := range m.events {
		if e.GlobalPosition > fromPosition {
			result = append(result, e)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].GlobalPosition < result[j].GlobalPosition
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func TestProjectionService_NoLogger_NoPanic(t *testing.T) {
	projErr := fmt.Errorf("projector error")
	es := &mockEventStore{
		events: []eventstore.Event{
			{StreamID: "test-stream", Type: "TestEvent", Version: 1},
		},
	}

	svc := NewProjectionService(es, ProjectionOptions{
		SyncMode:   false,
		MaxRetries: 1,
	})
	svc.RegisterProjector(&mockProjector{err: projErr})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should not panic even without a logger
	err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
