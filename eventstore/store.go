package eventstore

import (
	"context"
	"time"
)

// Store is the persistence interface for the event store. The core ships an
// in-memory reference implementation (infrastructure/inmemory.InMemoryEventStore);
// production backends are brought by the consumer (BYO DB). The per-method
// contracts below are what core services (contract repository, SnapshotService,
// TemporalQueryService, ProjectionService) and tx.RetryOnConflict depend on —
// an implementation that deviates breaks retries, temporal replay, or
// projection checkpoints in ways the compiler cannot catch.
type Store interface {
	// Append persists events to a stream with optimistic locking.
	// expectedVersion is the version the caller expects the stream to be at
	// (the number of events already stored).
	//
	// Version-conflict contract (REQUIRED): when the stream is not at
	// expectedVersion, Append MUST return an error that
	// tx.IsVersionConflict recognises — either a *shared.DomainError with
	// code shared.ErrCodeVersionConflict (the encoding used by the
	// in-memory reference store, which keeps this layer free of an
	// application/tx import) or an error wrapping the tx.ErrVersionConflict
	// sentinel (errors.Is must hold). tx.RetryOnConflict retries ONLY
	// errors matching one of these two encodings; a custom conflict error
	// silently disables every conflict retry in the system (contract saves
	// wrapped in RetryOnConflict fail permanently on the first concurrent
	// write instead of retrying).
	//
	// Event.Version stamping: core aggregates always stamp the batch
	// contiguously — eventstore.BaseAggregate.RaiseEvent numbers events
	// version+1, version+2, ... so a batch appended at expectedVersion
	// carries exactly expectedVersion+1 .. expectedVersion+len(events).
	// Implementations MUST either honour-and-validate those caller-stamped
	// versions (the reference behaviour: the in-memory store validates
	// contiguity from expectedVersion+1 and rejects a gapped or
	// out-of-order batch with a validation error, since such a batch was
	// built against a stale aggregate and would corrupt the append-only
	// log) or renumber identically (assign expectedVersion+1 ..
	// expectedVersion+len(events) themselves). Either way the stored
	// versions are the same; what is forbidden is persisting a
	// discontinuous stream.
	//
	// Atomicity (REQUIRED): the batch is all-or-nothing. Either every event
	// in the slice is persisted (in slice order, with contiguous versions)
	// or none is; a partially-appended batch leaves the aggregate
	// unreconstructable. Implementations should also stamp RecordedAt and
	// GlobalPosition on their own stored copies rather than mutating the
	// caller's slice elements (the caller may still hold the aggregate's
	// UncommittedEvents; see the in-memory store, issue #162 I3).
	Append(ctx context.Context, streamID string, events []Event, expectedVersion int) error

	// Load returns all events for a stream.
	//
	// Ordering (REQUIRED): events MUST be returned in ascending Version
	// order — aggregate replay (LoadFromHistory) applies them in slice
	// order and does not sort.
	//
	// Empty-stream convention: a stream with no events (including a
	// streamID that has never been written) returns (empty slice, nil),
	// NOT a not-found error. Callers such as TemporalQueryService rely on
	// this to reconstruct "aggregate did not exist yet" as a zero-value
	// aggregate.
	Load(ctx context.Context, streamID string) ([]Event, error)

	// LoadUntilVersion returns events with Version <= version.
	//
	// Same ordering (ascending Version) and empty-stream ((empty, nil))
	// contracts as Load.
	//
	// NOTE: LoadUntilVersion is not currently called by any core service —
	// it is kept on the interface for BYO tooling (debugging, audit,
	// version-pinned replay). Do not remove it from implementations.
	LoadUntilVersion(ctx context.Context, streamID string, version int) ([]Event, error)

	// LoadUntil returns events with OccurredAt <= until.
	//
	// Same ordering (ascending Version) and empty-stream ((empty, nil))
	// contracts as Load. The cut is on OccurredAt (the domain timestamp),
	// not RecordedAt; TemporalQueryService uses this for point-in-time
	// reconstruction.
	LoadUntil(ctx context.Context, streamID string, until time.Time) ([]Event, error)

	// LoadRange returns events with from <= OccurredAt < to.
	//
	// Same ordering (ascending Version) and empty-stream ((empty, nil))
	// contracts as Load.
	LoadRange(ctx context.Context, streamID string, from, to time.Time) ([]Event, error)

	// LoadAll loads events across all streams ordered by ascending
	// GlobalPosition. fromPosition is exclusive (events after this position
	// are returned). limit controls the maximum number of events returned
	// (for pagination). A limit <= 0 means no limit.
	//
	// GlobalPosition visibility contract (REQUIRED): positions MUST become
	// visible to readers gap-free and monotonically — a reader that has
	// seen position N must never later discover a previously-unseen event
	// with a position < N. Projection checkpoints depend on this:
	// ProjectionService saves the last processed GlobalPosition and resumes
	// strictly after it, so an event that becomes visible "behind" the
	// checkpoint is skipped forever and the read model silently diverges.
	// Naive sequence/auto-increment assignment violates this under
	// concurrent commits (a transaction that reserved position N-1 can
	// commit AFTER a reader already saw N). Implementations must close
	// that window — e.g. serialize appends through a single writer, assign
	// positions in commit order, or make readers wait out in-flight gaps.
	LoadAll(ctx context.Context, fromPosition int64, limit int) ([]Event, error)

	// Subscribe returns a channel that delivers every event with a
	// GlobalPosition greater than fromPosition (exclusive): first the
	// historical backfill (replay), then the live tail, in global-position
	// order, with no gap and no duplicate at the handover. Pass a position
	// obtained from a checkpoint to resume after a restart; pass 0 for the
	// full history followed by the live tail.
	//
	// Delivery is at-least-once: consumers (Projector implementations)
	// must tolerate redelivery. The GlobalPosition visibility contract
	// documented on LoadAll applies equally here — a gap at the
	// backfill-to-live handover loses events for every projection built on
	// this subscription.
	//
	// Lifecycle: when ctx is cancelled the subscription is torn down and
	// the channel is CLOSED, so consumers observe end-of-stream rather
	// than blocking forever. The in-memory reference store documents (and
	// tests) these exact semantics; production implementations should
	// mirror them backed by durable storage.
	Subscribe(ctx context.Context, fromPosition int64) (<-chan Event, error)

	// SaveSnapshot saves an aggregate snapshot. Implementations may retain
	// multiple snapshots per stream; they only need to satisfy the
	// selection rules documented on LoadSnapshot / LoadSnapshotBefore.
	SaveSnapshot(ctx context.Context, snapshot Snapshot) error

	// LoadSnapshot loads the latest snapshot for a stream, where "latest"
	// means the snapshot with the MAXIMUM Version — not the most recently
	// written one. An out-of-order SaveSnapshot (e.g. a lagging rebuild
	// worker saving a stale snapshot after a newer one) must not cause
	// reads to resume from an older snapshot (issue #157). Returns
	// (nil, nil) when the stream has no snapshot.
	//
	// Informational: Snapshot.AsOf is currently unused by core — core
	// selection is by Version here and by CreatedAt in LoadSnapshotBefore.
	// Implementations may persist AsOf but must not key selection on it.
	LoadSnapshot(ctx context.Context, streamID string) (*Snapshot, error)

	// LoadSnapshotBefore loads the latest snapshot created strictly before
	// the given time. The cut is on CreatedAt (wall-clock); among the
	// candidates the one with the greatest CreatedAt wins, tie-broken by
	// higher Version, so an out-of-order SaveSnapshot cannot surface a
	// non-latest snapshot (same class of bug as LoadSnapshot; issue #157).
	// Returns (nil, nil) when no snapshot predates the cut.
	// TemporalQueryService additionally guards the result against the asOf
	// event horizon before using it (see GetContractAsOf).
	LoadSnapshotBefore(ctx context.Context, streamID string, before time.Time) (*Snapshot, error)
}
