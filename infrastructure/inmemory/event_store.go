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

// subscriberBufferSize is the capacity of a subscriber's delivery channel. It
// only affects delivery latency, not durability: a slow consumer causes events
// to accumulate in the subscription's internal queue (unbounded) rather than
// being dropped (issue #192).
const subscriberBufferSize = 100

// subscription is a live+backfill event feed for a single Subscribe caller.
//
// Append never blocks on and never drops for a slow consumer: it enqueues into
// an unbounded internal queue under the subscription's own lock. A dedicated
// pump goroutine drains the queue to the delivery channel with a blocking send
// (escaped by context cancellation), providing at-least-once, lossless delivery
// for the reference store.
type subscription struct {
	ch     chan eventstore.Event
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []eventstore.Event
	closed bool
	// lastPos is the highest GlobalPosition already enqueued. It is a monotonic
	// guard that makes the backfill→live handover gap-free AND overlap-free:
	// Subscribe seeds it from the backfill snapshot while holding the store lock,
	// so any later Append (which also takes the store lock) carries strictly
	// greater positions and is enqueued exactly once.
	lastPos int64
}

func newSubscription(fromPosition int64) *subscription {
	sub := &subscription{
		ch:      make(chan eventstore.Event, subscriberBufferSize),
		lastPos: fromPosition,
	}
	sub.cond = sync.NewCond(&sub.mu)
	return sub
}

// enqueue appends the events whose GlobalPosition advances past lastPos. It is
// non-blocking (a slice append) so it never stalls the writer.
func (sub *subscription) enqueue(events []eventstore.Event) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return
	}
	appended := false
	for _, e := range events {
		if e.GlobalPosition <= sub.lastPos {
			continue // already delivered / in backfill; skip (no duplicate, no gap)
		}
		sub.lastPos = e.GlobalPosition
		sub.queue = append(sub.queue, e)
		appended = true
	}
	if appended {
		sub.cond.Signal()
	}
}

// closeSub marks the subscription closed and wakes the pump so it can exit.
func (sub *subscription) closeSub() {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	sub.closed = true
	sub.cond.Signal()
}

// pump drains the queue to the delivery channel until the context is cancelled
// or the subscription is closed. It closes the delivery channel on exit so the
// consumer observes end-of-stream.
func (sub *subscription) pump(ctx context.Context) {
	defer close(sub.ch)
	for {
		sub.mu.Lock()
		for len(sub.queue) == 0 && !sub.closed {
			sub.cond.Wait()
		}
		if sub.closed {
			sub.mu.Unlock()
			return
		}
		batch := sub.queue
		sub.queue = nil
		sub.mu.Unlock()

		for _, e := range batch {
			select {
			case sub.ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}
}

// InMemoryEventStore is an in-memory implementation of eventstore.Store.
type InMemoryEventStore struct {
	mu          sync.RWMutex
	streams     map[string][]eventstore.Event    // streamID -> events
	allEvents   []eventstore.Event               // all events in global position order
	snapshots   map[string][]eventstore.Snapshot // streamID -> snapshots (multiple)
	position    int64                            // global position counter
	subscribers []*subscription
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

	// Validate Version contiguity: appended events must be numbered sequentially
	// starting at expectedVersion+1. A gap or out-of-order Version means the
	// events were built against a stale aggregate version and would corrupt the
	// append-only log (breaking optimistic locking and temporal replay), so
	// reject the batch rather than persist a discontinuous stream.
	for i := range events {
		wantVersion := expectedVersion + i + 1
		if events[i].Version != wantVersion {
			return shared.NewDomainError(shared.ErrCodeValidation,
				fmt.Sprintf("event %d for stream %q has version %d but expected contiguous version %d",
					i, streamID, events[i].Version, wantVersion))
		}
	}

	now := s.clock.Now()
	// Stamp RecordedAt/GlobalPosition on COPIES rather than on the caller's slice
	// elements. The caller (e.g. contract_repository.Save) may keep referencing
	// its UncommittedEvents slice; mutating those structs in place would leak
	// store-assigned fields back into the caller's aggregate (issue #162 I3).
	stored := make([]eventstore.Event, len(events))
	for i := range events {
		e := events[i]
		e.RecordedAt = now
		s.position++
		e.GlobalPosition = s.position
		stored[i] = e
		s.streams[streamID] = append(s.streams[streamID], e)
		s.allEvents = append(s.allEvents, e)
	}

	// Notify subscribers with the stored copies. enqueue is non-blocking and
	// lossless: it buffers into each subscription's internal queue rather than
	// dropping for a slow consumer (issue #192). The monotonic lastPos guard
	// makes this safe against the backfill→live boundary in Subscribe.
	for _, sub := range s.subscribers {
		sub.enqueue(stored)
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

// Subscribe returns a channel that delivers every event with a GlobalPosition
// greater than fromPosition: first the historical backfill, then the live tail,
// in global-position order, with no gap and no duplicate at the handover.
//
// Delivery is at-least-once and lossless (issue #192):
//   - fromPosition is HONOURED: events already stored with GlobalPosition >
//     fromPosition are replayed (backfill) before live events. Pass a position
//     obtained from a checkpoint to resume after a restart; pass 0 to receive
//     the full history followed by the live tail.
//   - A slow consumer never loses events: Append buffers into an unbounded
//     per-subscriber queue instead of dropping. The blocking hand-off applies
//     backpressure at the delivery channel only.
//
// Gap-free handover: the backfill snapshot and the subscriber registration are
// performed atomically under the store lock, and a monotonic position guard
// (subscription.lastPos) rejects any live event whose position was already in
// the backfill. Because positions are assigned under the same lock, any Append
// racing with Subscribe is serialized either fully before (captured by backfill)
// or fully after (delivered live) — never split.
//
// Lifecycle: when ctx is cancelled the subscriber is unregistered and the
// channel is closed, so there is no goroutine or channel leak.
//
// A production eventstore.Store should provide the same replay-then-tail,
// lossless semantics backed by durable storage.
func (s *InMemoryEventStore) Subscribe(ctx context.Context, fromPosition int64) (<-chan eventstore.Event, error) {
	sub := newSubscription(fromPosition)

	s.mu.Lock()
	// Backfill: snapshot events already stored with GlobalPosition > fromPosition.
	start := sort.Search(len(s.allEvents), func(i int) bool {
		return s.allEvents[i].GlobalPosition > fromPosition
	})
	if start < len(s.allEvents) {
		backfill := make([]eventstore.Event, len(s.allEvents)-start)
		copy(backfill, s.allEvents[start:])
		sub.enqueue(backfill) // advances lastPos to the highest backfilled position
	}
	// Register while still holding the lock so no Append can slip in unseen
	// between the backfill snapshot and live registration.
	s.subscribers = append(s.subscribers, sub)
	s.mu.Unlock()

	go sub.pump(ctx)
	go func() {
		<-ctx.Done()
		s.removeSubscriber(sub)
		sub.closeSub()
	}()

	return sub.ch, nil
}

// removeSubscriber unregisters a subscription so Append stops enqueuing to it.
func (s *InMemoryEventStore) removeSubscriber(target *subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sub := range s.subscribers {
		if sub == target {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			return
		}
	}
}

// SaveSnapshot saves an aggregate snapshot.
func (s *InMemoryEventStore) SaveSnapshot(_ context.Context, snapshot eventstore.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.snapshots[snapshot.StreamID] = append(s.snapshots[snapshot.StreamID], snapshot)
	return nil
}

// LoadSnapshot loads the highest-Version snapshot for a stream.
//
// It selects the maximum Version rather than the most recently appended
// snapshot: an out-of-order SaveSnapshot (e.g. a lagging rebuild worker saving
// a stale snapshot after a newer one) must not cause reads to resume from an
// older snapshot and replay events that predate it (issue #157).
func (s *InMemoryEventStore) LoadSnapshot(_ context.Context, streamID string) (*eventstore.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snaps := s.snapshots[streamID]
	if len(snaps) == 0 {
		return nil, nil
	}
	best := snaps[0]
	for i := 1; i < len(snaps); i++ {
		if snaps[i].Version > best.Version {
			best = snaps[i]
		}
	}
	return &best, nil
}

// LoadSnapshotBefore loads the latest snapshot created before a given time.
//
// The cut is on CreatedAt (wall-clock), matching the postgres reference
// (SELECT ... WHERE created_at < ? ORDER BY created_at DESC LIMIT 1) and the
// temporal-query consistency guard (review W7), which selects by CreatedAt and
// then rejects snapshots whose Version exceeds the asOf event horizon. Among the
// candidates it returns the one with the greatest CreatedAt (tie-broken by
// higher Version) rather than the last appended, so an out-of-order SaveSnapshot
// cannot make it return a non-latest snapshot (same class of bug as LoadSnapshot;
// issue #157).
func (s *InMemoryEventStore) LoadSnapshotBefore(_ context.Context, streamID string, before time.Time) (*eventstore.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snaps := s.snapshots[streamID]
	var best *eventstore.Snapshot
	for i := range snaps {
		if !snaps[i].CreatedAt.Before(before) {
			continue
		}
		if best == nil || snaps[i].CreatedAt.After(best.CreatedAt) ||
			(snaps[i].CreatedAt.Equal(best.CreatedAt) && snaps[i].Version > best.Version) {
			snap := snaps[i]
			best = &snap
		}
	}
	return best, nil
}
