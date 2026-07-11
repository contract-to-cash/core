package projection

import "context"

// CheckpointStore persists the last successfully-processed global position for a
// projection, so a ProjectionService can resume from where it left off after a
// process restart instead of replaying the entire event log (or, worse, missing
// every event appended while it was down).
//
// Cadence: the reference ProjectionService saves the checkpoint after every
// successfully-processed event (see ProjectionService.Start). Production
// implementations MAY batch saves (e.g. every N events or every T seconds) to
// reduce write amplification, provided they never persist a position ahead of
// an event that has not yet been durably applied by all projectors — doing so
// would silently drop that event on resume.
//
// At-least-once / idempotency: because Subscribe delivery is at-least-once (see
// eventstore.Store.Subscribe) and resume replays from the checkpointed position,
// a Projector may observe the same event more than once. Projector
// implementations MUST be idempotent (see Projector).
type CheckpointStore interface {
	// Load returns the last successfully-processed global position for
	// projectionName. It returns 0 (meaning "start from the beginning") when no
	// checkpoint has been saved for that name yet.
	Load(ctx context.Context, projectionName string) (int64, error)

	// Save persists position as the last successfully-processed global position
	// for projectionName. Callers pass the GlobalPosition of an event only after
	// all projectors have successfully processed it.
	Save(ctx context.Context, projectionName string, position int64) error
}
