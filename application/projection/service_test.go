package projection

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
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
