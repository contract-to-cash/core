package service

import (
	"context"
	"fmt"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// DefaultSnapshotInterval is the default number of events between snapshots.
const DefaultSnapshotInterval = 100

// SnapshotService manages aggregate snapshot creation based on event count.
type SnapshotService struct {
	eventStore eventstore.Store
	clock      shared.Clock
	interval   int
}

// NewSnapshotService creates a new SnapshotService.
// If interval <= 0, DefaultSnapshotInterval is used.
func NewSnapshotService(eventStore eventstore.Store, clock shared.Clock, interval int) *SnapshotService {
	if interval <= 0 {
		interval = DefaultSnapshotInterval
	}
	return &SnapshotService{
		eventStore: eventStore,
		clock:      clock,
		interval:   interval,
	}
}

// ShouldCreateSnapshot returns true if a snapshot should be created
// at the given version (i.e., every `interval` events).
func (s *SnapshotService) ShouldCreateSnapshot(currentVersion int) bool {
	return currentVersion > 0 && currentVersion%s.interval == 0
}

// CreateSnapshot serializes the aggregate state and saves it as a snapshot.
//
// The aggregate MUST implement eventstore.SnapshotMarshaler. Event-sourced
// aggregates hold their state in unexported fields, so a generic json.Marshal
// would silently serialize an empty object ("{}") and a later LoadFromSnapshot
// would restore an empty aggregate at a non-zero version — silent state
// corruption. Rather than fall back to json.Marshal, CreateSnapshot returns an
// error naming the required interface when it is not implemented (issue #157).
func (s *SnapshotService) CreateSnapshot(ctx context.Context, agg eventstore.AggregateRoot) error {
	marshaler, ok := agg.(eventstore.SnapshotMarshaler)
	if !ok {
		return shared.NewDomainError(
			shared.ErrCodeValidation,
			fmt.Sprintf("aggregate %T does not implement eventstore.SnapshotMarshaler; "+
				"snapshotting event-sourced aggregates requires a MarshalSnapshot() method "+
				"(a generic json.Marshal would serialize an empty {} and corrupt restores)", agg),
		)
	}

	// Guard against snapshotting an aggregate with uncommitted events (issue
	// #197). A snapshot records the aggregate's state paired with its Version().
	// If the aggregate still holds uncommitted events, its in-memory state has
	// already advanced past the persisted version: MarshalSnapshot captures the
	// post-mutation state while Version() may not correspond to what has been
	// appended to the event stream. Persisting that pairing yields a snapshot
	// whose state and version disagree, so a later LoadFromSnapshot (optionally
	// followed by replay of events after Version()) reconstructs a corrupt
	// aggregate — double-applying or skipping the uncommitted events. Callers
	// must persist (append + ClearUncommittedEvents) BEFORE snapshotting.
	if len(agg.UncommittedEvents()) > 0 {
		return shared.NewDomainError(
			shared.ErrCodeBusinessRule,
			fmt.Sprintf("cannot snapshot aggregate %s: it has %d uncommitted event(s); "+
				"persist the aggregate (append events + ClearUncommittedEvents) before creating a snapshot",
				agg.ID(), len(agg.UncommittedEvents())),
		)
	}

	stateData, err := marshaler.MarshalSnapshot()
	if err != nil {
		return fmt.Errorf("failed to marshal aggregate state: %w", err)
	}

	snapshot := eventstore.Snapshot{
		StreamID:  agg.ID(),
		Version:   agg.Version(),
		State:     stateData,
		AsOf:      s.clock.Now(),
		CreatedAt: s.clock.Now(),
	}

	return s.eventStore.SaveSnapshot(ctx, snapshot)
}
