package projection

import (
	"bytes"
	"context"
	"errors"
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

	// Start processes events from the channel; the mock closes the channel with
	// the context still active, so Start reports ErrSubscriptionClosed (#246).
	err := svc.Start(ctx)
	if !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("expected ErrSubscriptionClosed, got: %v", err)
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
	es := &paginatingMockEventStore{mockEventStore: mockEventStore{events: events}}

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

// paginatingMockEventStore embeds mockEventStore and overrides LoadAll
// to respect fromPosition and limit for pagination testing.
type paginatingMockEventStore struct {
	mockEventStore
}

// --- Checkpoint + retry tests (issue #192) ---

// fromPositionMockStore honors fromPosition on Subscribe: it delivers the events
// with GlobalPosition > fromPosition (ordered) and then closes the channel,
// modelling a replay-then-terminate feed so Start returns after draining.
type fromPositionMockStore struct {
	mockEventStore
}

func (m *fromPositionMockStore) Subscribe(_ context.Context, fromPosition int64) (<-chan eventstore.Event, error) {
	var selected []eventstore.Event
	for _, e := range m.events {
		if e.GlobalPosition > fromPosition {
			selected = append(selected, e)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].GlobalPosition < selected[j].GlobalPosition })
	ch := make(chan eventstore.Event, len(selected))
	for _, e := range selected {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// mockCheckpointStore is an in-package CheckpointStore for tests.
type mockCheckpointStore struct {
	positions map[string]int64
	saveErr   error
}

func newMockCheckpointStore() *mockCheckpointStore {
	return &mockCheckpointStore{positions: make(map[string]int64)}
}
func (m *mockCheckpointStore) Load(_ context.Context, name string) (int64, error) {
	return m.positions[name], nil
}
func (m *mockCheckpointStore) Save(_ context.Context, name string, pos int64) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.positions[name] = pos
	return nil
}

// positionRecordingProjector records the GlobalPositions it observed and can be
// configured to fail on a specific position.
type positionRecordingProjector struct {
	seen   []int64
	failAt int64 // 0 = never fail
}

func (p *positionRecordingProjector) Project(_ context.Context, e eventstore.Event) error {
	if p.failAt != 0 && e.GlobalPosition == p.failAt {
		return fmt.Errorf("forced failure at position %d", e.GlobalPosition)
	}
	p.seen = append(p.seen, e.GlobalPosition)
	return nil
}
func (p *positionRecordingProjector) Rebuild(_ context.Context, _ time.Time) error { return nil }

func TestProjectionService_CheckpointResumeAfterRestart(t *testing.T) {
	es := &fromPositionMockStore{mockEventStore{events: []eventstore.Event{
		{StreamID: "s", Type: "e", Version: 1, GlobalPosition: 1},
		{StreamID: "s", Type: "e", Version: 2, GlobalPosition: 2},
		{StreamID: "s", Type: "e", Version: 3, GlobalPosition: 3},
	}}}
	cp := newMockCheckpointStore()

	// First run: process everything, checkpoint advances to 3.
	proj1 := &positionRecordingProjector{}
	svc1 := NewProjectionService(es, ProjectionOptions{SyncMode: true, MaxRetries: 0, CheckpointStore: cp, ProjectionName: "p"})
	svc1.RegisterProjector(proj1)
	// The mock feed closes the channel after draining with the context still
	// active, so Start reports ErrSubscriptionClosed (#246).
	if err := svc1.Start(context.Background()); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("first Start: expected ErrSubscriptionClosed, got: %v", err)
	}
	if got := cp.positions["p"]; got != 3 {
		t.Fatalf("expected checkpoint 3 after first run, got %d", got)
	}
	if len(proj1.seen) != 3 {
		t.Fatalf("expected 3 events processed in first run, got %v", proj1.seen)
	}

	// Simulated restart: two more events arrive.
	es.events = append(es.events,
		eventstore.Event{StreamID: "s", Type: "e", Version: 4, GlobalPosition: 4},
		eventstore.Event{StreamID: "s", Type: "e", Version: 5, GlobalPosition: 5},
	)

	// Second run with a FRESH projector: must deliver exactly the missed events.
	proj2 := &positionRecordingProjector{}
	svc2 := NewProjectionService(es, ProjectionOptions{SyncMode: true, MaxRetries: 0, CheckpointStore: cp, ProjectionName: "p"})
	svc2.RegisterProjector(proj2)
	if err := svc2.Start(context.Background()); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("second Start: expected ErrSubscriptionClosed, got: %v", err)
	}
	if len(proj2.seen) != 2 || proj2.seen[0] != 4 || proj2.seen[1] != 5 {
		t.Fatalf("expected exactly missed events [4 5], got %v", proj2.seen)
	}
	if got := cp.positions["p"]; got != 5 {
		t.Fatalf("expected checkpoint 5 after second run, got %d", got)
	}
}

func TestProjectionService_CheckpointNotAdvancedPastAsyncFailure(t *testing.T) {
	es := &fromPositionMockStore{mockEventStore{events: []eventstore.Event{
		{StreamID: "s", Type: "e", Version: 1, GlobalPosition: 1},
		{StreamID: "s", Type: "e", Version: 2, GlobalPosition: 2},
		{StreamID: "s", Type: "e", Version: 3, GlobalPosition: 3},
	}}}
	cp := newMockCheckpointStore()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	proj := &positionRecordingProjector{failAt: 2}
	svc := NewProjectionService(es, ProjectionOptions{
		SyncMode: false, MaxRetries: 0, CheckpointStore: cp, ProjectionName: "p", Logger: logger,
	})
	svc.RegisterProjector(proj)

	if err := svc.Start(context.Background()); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("expected ErrSubscriptionClosed, got: %v", err)
	}

	// Event 1 succeeded (checkpoint 1). Event 2 failed → checkpoint frozen.
	// Event 3 is still applied live, but the checkpoint must NOT advance past 1.
	if got := cp.positions["p"]; got != 1 {
		t.Fatalf("checkpoint must not advance past failed event: want 1, got %d", got)
	}
	if !strings.Contains(buf.String(), "projection failed") {
		t.Errorf("expected async failure to be logged, got: %s", buf.String())
	}
}

func TestProjectionService_MaxRetriesSemantics(t *testing.T) {
	// A projector that fails failTimes then succeeds.
	newProj := func(failTimes int) *flakyProjector { return &flakyProjector{failTimes: failTimes} }

	// MaxRetries=0 -> 1 attempt: a single failure is not retried.
	svc := NewProjectionService(&mockEventStore{}, ProjectionOptions{MaxRetries: 0})
	p := newProj(1)
	svc.RegisterProjector(p)
	err := svc.ProcessEvent(context.Background(), eventstore.Event{GlobalPosition: 1})
	if err == nil {
		t.Fatal("MaxRetries=0 should give a single attempt and surface the failure")
	}
	if p.attempts != 1 {
		t.Fatalf("MaxRetries=0: expected 1 attempt, got %d", p.attempts)
	}
	if !strings.Contains(err.Error(), "1 attempts") {
		t.Errorf("error should report attempt count, got: %v", err)
	}

	// MaxRetries=2 -> 3 attempts: succeeds after 2 failures.
	svc2 := NewProjectionService(&mockEventStore{}, ProjectionOptions{MaxRetries: 2})
	p2 := newProj(2)
	svc2.RegisterProjector(p2)
	if err := svc2.ProcessEvent(context.Background(), eventstore.Event{GlobalPosition: 1}); err != nil {
		t.Fatalf("MaxRetries=2 should retry twice and succeed, got: %v", err)
	}
	if p2.attempts != 3 {
		t.Fatalf("MaxRetries=2: expected 3 attempts, got %d", p2.attempts)
	}
}

type flakyProjector struct {
	failTimes int
	attempts  int
}

func (p *flakyProjector) Project(_ context.Context, _ eventstore.Event) error {
	p.attempts++
	if p.attempts <= p.failTimes {
		return fmt.Errorf("attempt %d fails", p.attempts)
	}
	return nil
}
func (p *flakyProjector) Rebuild(_ context.Context, _ time.Time) error { return nil }

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

	// Should not panic even without a logger. The mock closes the channel with
	// the context still active, so the sentinel (not a panic) is expected.
	err := svc.Start(ctx)
	if !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("expected ErrSubscriptionClosed, got: %v", err)
	}
}

// --- Start return-value classification (issue #246) ---

// cancelClosingMockStore mirrors the in-memory reference store's lifecycle:
// the subscription channel stays open until the context is cancelled, at which
// point it is closed. This lets tests exercise the race where Start observes
// the closed channel (!ok) instead of <-ctx.Done().
type cancelClosingMockStore struct {
	mockEventStore
}

func (m *cancelClosingMockStore) Subscribe(ctx context.Context, _ int64) (<-chan eventstore.Event, error) {
	ch := make(chan eventstore.Event)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func TestProjectionService_Start_SubscriptionClosedWithLiveContext(t *testing.T) {
	// Channel closes while the context is still active → abnormal termination.
	es := &mockEventStore{events: []eventstore.Event{
		{StreamID: "s", Type: "e", Version: 1, GlobalPosition: 1},
	}}
	proj := &recordingProjector{}
	svc := NewProjectionService(es, ProjectionOptions{SyncMode: true})
	svc.RegisterProjector(proj)

	err := svc.Start(context.Background())
	if !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("expected ErrSubscriptionClosed for a close with a live context, got: %v", err)
	}
	// The events delivered before the close were still processed.
	if len(proj.projected) != 1 {
		t.Errorf("expected 1 event processed before the close, got %d", len(proj.projected))
	}
}

func TestProjectionService_Start_GracefulShutdownOnContextCancel(t *testing.T) {
	// The store closes the channel in response to ctx cancellation (like the
	// in-memory reference store). Whichever select branch Start observes first
	// (ctx.Done or the closed channel), cancellation must be classified as a
	// graceful shutdown: ctx.Err(), never ErrSubscriptionClosed.
	es := &cancelClosingMockStore{}
	svc := NewProjectionService(es, ProjectionOptions{SyncMode: true})
	svc.RegisterProjector(&recordingProjector{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Start(ctx) }()
	cancel()

	select {
	case err := <-done:
		if errors.Is(err, ErrSubscriptionClosed) {
			t.Fatalf("context cancellation must not be classified as ErrSubscriptionClosed, got: %v", err)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled on graceful shutdown, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}

func TestProjectionService_Start_ClosedChannelWithCancelledContext(t *testing.T) {
	// Deterministically exercise the !ok branch with an already-done context:
	// the mock's channel is already closed AND the context is already cancelled,
	// so whichever branch the select picks must classify this as cancellation.
	es := &mockEventStore{} // Subscribe returns an immediately-closed channel
	svc := NewProjectionService(es, ProjectionOptions{SyncMode: true})
	svc.RegisterProjector(&recordingProjector{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.Start(ctx)
	if errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("cancelled context must win over the closed channel, got: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}
