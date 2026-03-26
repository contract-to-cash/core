package service

import (
	"context"
	"encoding/json"
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
// The aggregate should implement eventstore.SnapshotMarshaler for correct
// serialization of unexported fields. Falls back to json.Marshal otherwise.
func (s *SnapshotService) CreateSnapshot(ctx context.Context, agg eventstore.AggregateRoot) error {
	var stateData []byte
	var err error
	if marshaler, ok := agg.(eventstore.SnapshotMarshaler); ok {
		stateData, err = marshaler.MarshalSnapshot()
	} else {
		stateData, err = json.Marshal(agg)
	}
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
