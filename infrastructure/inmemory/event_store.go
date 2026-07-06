package inmemory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// Compile-time interface check.
var _ eventstore.Store = (*InMemoryEventStore)(nil)

// InMemoryEventStore is an in-memory implementation of eventstore.Store.
type InMemoryEventStore struct {
	mu          sync.RWMutex
	streams     map[string][]eventstore.Event    // streamID -> events
	allEvents   []eventstore.Event               // all events in global position order
	snapshots   map[string][]eventstore.Snapshot // streamID -> snapshots (multiple)
	position    int64                            // global position counter
	subscribers []chan eventstore.Event
	clock       shared.Clock
}

// NewInMemoryEventStore creates a new InMemoryEventStore.
func NewInMemoryEventStore(clock shared.Clock) *InMemoryEventStore {
	return &InMemoryEventStore{
		streams:   make(map[string][]eventstore.Event),
		snapshots: make(map[string][]eventstore.Snapshot),
		clock:     clock,
	}
}

// Append persists events to a stream with optimistic locking.
func (s *InMemoryEventStore) Append(_ context.Context, streamID string, events []eventstore.Event, expectedVersion int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentVersion := len(s.streams[streamID])
	if currentVersion != expectedVersion {
		// Optimistic-lock conflict. This is encoded as a version_conflict
		// DomainError (not the tx.ErrVersionConflict sentinel) to keep the
		// eventstore/infrastructure layer free of an application/tx import.
		// tx.RetryOnConflict recognises this code via tx.IsVersionConflict, so a
		// contract Save wrapped in RetryOnConflict retries just like an
		// invoice/balance Save that returns the sentinel.
		return shared.NewDomainError(shared.ErrCodeVersionConflict,
			fmt.Sprintf("expected version %d but stream %q is at version %d", expectedVersion, streamID, currentVersion))
	}

	now := s.clock.Now()
	for i := range events {
		events[i].RecordedAt = now
		s.position++
		events[i].GlobalPosition = s.position
		s.streams[streamID] = append(s.streams[streamID], events[i])
		s.allEvents = append(s.allEvents, events[i])
	}

	// Notify subscribers.
	for _, ch := range s.subscribers {
		for _, e := range events {
			select {
			case ch <- e:
			default:
				// Drop if subscriber is slow.
			}
		}
	}

	return nil
}

// Load returns all events for a stream.
func (s *InMemoryEventStore) Load(_ context.Context, streamID string) ([]eventstore.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.streams[streamID]
	result := make([]eventstore.Event, len(events))
	copy(result, events)
	return result, nil
}

// LoadUntilVersion returns events up to a specific version.
func (s *InMemoryEventStore) LoadUntilVersion(_ context.Context, streamID string, version int) ([]eventstore.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []eventstore.Event
	for _, e := range s.streams[streamID] {
		if e.Version <= version {
			result = append(result, e)
		}
	}
	return result, nil
}

// LoadUntil returns events until a specific time (OccurredAt <= until).
func (s *InMemoryEventStore) LoadUntil(_ context.Context, streamID string, until time.Time) ([]eventstore.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []eventstore.Event
	for _, e := range s.streams[streamID] {
		if !e.OccurredAt.After(until) {
			result = append(result, e)
		}
	}
	return result, nil
}

// LoadRange returns events within a date range (from <= OccurredAt < to).
func (s *InMemoryEventStore) LoadRange(_ context.Context, streamID string, from, to time.Time) ([]eventstore.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []eventstore.Event
	for _, e := range s.streams[streamID] {
		if !e.OccurredAt.Before(from) && e.OccurredAt.Before(to) {
			result = append(result, e)
		}
	}
	return result, nil
}

// LoadAll loads events across all streams ordered by global position.
// fromPosition is exclusive (events after this position are returned).
// limit controls the maximum number of events returned. A limit <= 0 means no limit.
func (s *InMemoryEventStore) LoadAll(_ context.Context, fromPosition int64, limit int) ([]eventstore.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Binary search for the first event with GlobalPosition > fromPosition.
	start := sort.Search(len(s.allEvents), func(i int) bool {
		return s.allEvents[i].GlobalPosition > fromPosition
	})

	remaining := s.allEvents[start:]
	if limit <= 0 || len(remaining) <= limit {
		result := make([]eventstore.Event, len(remaining))
		copy(result, remaining)
		return result, nil
	}
	result := make([]eventstore.Event, limit)
	copy(result, remaining[:limit])
	return result, nil
}

// Subscribe returns a channel that receives events appended after subscription.
//
// Reference-implementation limitations (NOT suitable for production durability):
//   - fromPosition is IGNORED: this is a live-only feed; historical events with a
//     GlobalPosition at or after fromPosition are NOT replayed. Use LoadAll (or
//     ProjectionService.RebuildAll) to catch up from a position, then Subscribe
//     for the live tail.
//   - Delivery is best-effort: if a subscriber's buffered channel is full (slow
//     consumer), Append DROPS the event for that subscriber rather than blocking
//     the writer (see Append). Combined with the ignored fromPosition, missed
//     events are only recoverable via a full RebuildAll.
//
// A production eventstore.Store should honour fromPosition (replay-then-tail) and
// provide durable, lossless delivery.
func (s *InMemoryEventStore) Subscribe(_ context.Context, _ int64) (<-chan eventstore.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan eventstore.Event, 100)
	s.subscribers = append(s.subscribers, ch)
	return ch, nil
}

// SaveSnapshot saves an aggregate snapshot.
func (s *InMemoryEventStore) SaveSnapshot(_ context.Context, snapshot eventstore.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.snapshots[snapshot.StreamID] = append(s.snapshots[snapshot.StreamID], snapshot)
	return nil
}

// LoadSnapshot loads the latest snapshot for a stream.
func (s *InMemoryEventStore) LoadSnapshot(_ context.Context, streamID string) (*eventstore.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snaps := s.snapshots[streamID]
	if len(snaps) == 0 {
		return nil, nil
	}
	latest := snaps[len(snaps)-1]
	return &latest, nil
}

// LoadSnapshotBefore loads the latest snapshot before a given time.
func (s *InMemoryEventStore) LoadSnapshotBefore(_ context.Context, streamID string, before time.Time) (*eventstore.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snaps := s.snapshots[streamID]
	var best *eventstore.Snapshot
	for i := range snaps {
		if snaps[i].CreatedAt.Before(before) {
			snap := snaps[i]
			best = &snap
		}
	}
	return best, nil
}
