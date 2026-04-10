package eventstore

import (
	"context"
	"time"
)

// Store is the interface for the event store.
type Store interface {
	// Append persists events to a stream with optimistic locking.
	// expectedVersion is the version the caller expects the stream to be at.
	Append(ctx context.Context, streamID string, events []Event, expectedVersion int) error

	// Load returns all events for a stream.
	Load(ctx context.Context, streamID string) ([]Event, error)

	// LoadUntilVersion returns events up to a specific version.
	LoadUntilVersion(ctx context.Context, streamID string, version int) ([]Event, error)

	// LoadUntil returns events until a specific time (OccurredAt-based).
	LoadUntil(ctx context.Context, streamID string, until time.Time) ([]Event, error)

	// LoadRange returns events within a date range (OccurredAt-based).
	LoadRange(ctx context.Context, streamID string, from, to time.Time) ([]Event, error)

	// LoadAll loads events across all streams ordered by global position.
	// fromPosition is exclusive (events after this position are returned).
	// limit controls the maximum number of events returned (for pagination).
	// A limit <= 0 means no limit.
	LoadAll(ctx context.Context, fromPosition int64, limit int) ([]Event, error)

	// Subscribe returns a channel that receives events from the given global position.
	Subscribe(ctx context.Context, fromPosition int64) (<-chan Event, error)

	// SaveSnapshot saves an aggregate snapshot.
	SaveSnapshot(ctx context.Context, snapshot Snapshot) error

	// LoadSnapshot loads the latest snapshot for a stream.
	LoadSnapshot(ctx context.Context, streamID string) (*Snapshot, error)

	// LoadSnapshotBefore loads the latest snapshot before a given time.
	LoadSnapshotBefore(ctx context.Context, streamID string, before time.Time) (*Snapshot, error)
}
