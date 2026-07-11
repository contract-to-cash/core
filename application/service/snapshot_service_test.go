package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// snapshotCapturingStore embeds the stateless mockEventStore and overrides
// SaveSnapshot to record the saved snapshot (or return a forced error).
type snapshotCapturingStore struct {
	mockEventStore
	saved   *eventstore.Snapshot
	saveErr error
}

func (m *snapshotCapturingStore) SaveSnapshot(_ context.Context, snap eventstore.Snapshot) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	s := snap
	m.saved = &s
	return nil
}

// nonMarshalerAggregate implements eventstore.AggregateRoot but deliberately
// does NOT implement eventstore.SnapshotMarshaler, exercising the guard in
// CreateSnapshot (issue #157).
type nonMarshalerAggregate struct {
	eventstore.BaseAggregate
}

func (a *nonMarshalerAggregate) LoadFromHistory(_ []eventstore.Event) error   { return nil }
func (a *nonMarshalerAggregate) LoadFromSnapshot(_ eventstore.Snapshot) error { return nil }

func TestCreateSnapshot_HappyPath_RoundTrips(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(10000))
	// Simulate a persisted aggregate: events have been appended and cleared.
	// CreateSnapshot rejects aggregates with uncommitted events (issue #197).
	agg.ClearUncommittedEvents()

	store := &snapshotCapturingStore{}
	svc := NewSnapshotService(store, clock, 100)

	if err := svc.CreateSnapshot(context.Background(), agg); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}
	if store.saved == nil {
		t.Fatal("expected a snapshot to be saved")
	}
	if store.saved.StreamID != agg.ID() {
		t.Errorf("StreamID = %q, want %q", store.saved.StreamID, agg.ID())
	}
	if store.saved.Version != agg.Version() {
		t.Errorf("Version = %d, want %d", store.saved.Version, agg.Version())
	}
	if !store.saved.CreatedAt.Equal(clock.Now()) {
		t.Errorf("CreatedAt = %v, want %v", store.saved.CreatedAt, clock.Now())
	}
	// The payload must round-trip: restoring it must reproduce the aggregate
	// state (not an empty {} that the old json.Marshal fallback produced).
	if string(store.saved.State) == "{}" {
		t.Fatal("snapshot state is empty {}; MarshalSnapshot was not used")
	}
	restored := contract.NewContractAggregate("", clock)
	if err := restored.LoadFromSnapshot(*store.saved); err != nil {
		t.Fatalf("LoadFromSnapshot failed: %v", err)
	}
	if restored.ContractID() != agg.ContractID() {
		t.Errorf("restored ContractID = %q, want %q", restored.ContractID(), agg.ContractID())
	}
	if restored.Status() != agg.Status() {
		t.Errorf("restored Status = %q, want %q", restored.Status(), agg.Status())
	}
	if restored.Price().Amount().Cmp(agg.Price().Amount()) != 0 ||
		restored.Price().Currency() != agg.Price().Currency() {
		t.Errorf("restored Price = %v, want %v", restored.Price(), agg.Price())
	}
	if restored.Version() != agg.Version() {
		t.Errorf("restored Version = %d, want %d", restored.Version(), agg.Version())
	}
}

func TestCreateSnapshot_NonMarshaler_ReturnsError(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	agg := &nonMarshalerAggregate{BaseAggregate: eventstore.NewBaseAggregate("agg-1", clock)}
	agg.SetVersion(150) // non-zero version: the corruption scenario the guard prevents

	store := &snapshotCapturingStore{}
	svc := NewSnapshotService(store, clock, 100)

	err := svc.CreateSnapshot(context.Background(), agg)
	if err == nil {
		t.Fatal("expected an error for an aggregate without SnapshotMarshaler, got nil")
	}
	var de *shared.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected a shared.DomainError, got %T: %v", err, err)
	}
	if de.Code != shared.ErrCodeValidation {
		t.Errorf("error code = %q, want %q", de.Code, shared.ErrCodeValidation)
	}
	if store.saved != nil {
		t.Error("no snapshot should have been saved when marshaling is unsupported")
	}
}

func TestCreateSnapshot_UncommittedEvents_ReturnsError(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	// Freshly-mutated aggregate: Create + Activate left uncommitted events that
	// have NOT been persisted. Snapshotting it now would pair post-mutation
	// state with a version that disagrees with the event stream (issue #197).
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(10000))
	if len(agg.UncommittedEvents()) == 0 {
		t.Fatal("test precondition: aggregate should have uncommitted events")
	}

	store := &snapshotCapturingStore{}
	svc := NewSnapshotService(store, clock, 100)

	err := svc.CreateSnapshot(context.Background(), agg)
	if err == nil {
		t.Fatal("expected an error snapshotting an aggregate with uncommitted events, got nil")
	}
	var de *shared.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected a shared.DomainError, got %T: %v", err, err)
	}
	if de.Code != shared.ErrCodeBusinessRule {
		t.Errorf("error code = %q, want %q", de.Code, shared.ErrCodeBusinessRule)
	}
	if store.saved != nil {
		t.Error("no snapshot should have been saved for an aggregate with uncommitted events")
	}
}

func TestCreateSnapshot_SaveError_Propagates(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(500))
	agg.ClearUncommittedEvents() // persisted aggregate — see CreateSnapshot guard (issue #197)

	sentinel := errors.New("store unavailable")
	store := &snapshotCapturingStore{saveErr: sentinel}
	svc := NewSnapshotService(store, clock, 100)

	err := svc.CreateSnapshot(context.Background(), agg)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected SaveSnapshot error to propagate, got %v", err)
	}
}

func TestShouldCreateSnapshot_Interval(t *testing.T) {
	svc := NewSnapshotService(nil, nil, 100)

	tests := []struct {
		version  int
		expected bool
	}{
		{0, false},
		{1, false},
		{50, false},
		{99, false},
		{100, true},
		{101, false},
		{200, true},
		{300, true},
		{150, false},
	}

	for _, tt := range tests {
		got := svc.ShouldCreateSnapshot(tt.version)
		if got != tt.expected {
			t.Errorf("ShouldCreateSnapshot(%d) = %v, want %v", tt.version, got, tt.expected)
		}
	}
}

func TestShouldCreateSnapshot_DefaultInterval(t *testing.T) {
	svc := NewSnapshotService(nil, nil, 0)
	if svc.interval != DefaultSnapshotInterval {
		t.Errorf("expected default interval %d, got %d", DefaultSnapshotInterval, svc.interval)
	}
}

func TestShouldCreateSnapshot_CustomInterval(t *testing.T) {
	svc := NewSnapshotService(nil, nil, 50)

	if !svc.ShouldCreateSnapshot(50) {
		t.Error("expected true for version 50 with interval 50")
	}
	if !svc.ShouldCreateSnapshot(100) {
		t.Error("expected true for version 100 with interval 50")
	}
	if svc.ShouldCreateSnapshot(75) {
		t.Error("expected false for version 75 with interval 50")
	}
}
