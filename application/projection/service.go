// Package projection provides event projection infrastructure
// for building read models from event streams.
package projection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/contract-to-cash/core/eventstore"
)

// ErrSubscriptionClosed is returned by Start when the event-store subscription
// channel closes while the context is still active — i.e. the feed died
// underneath the projection (store shutdown, broken connection, misbehaving
// Subscribe implementation) rather than the caller requesting shutdown via
// context cancellation. Supervisors should treat it as a failure and
// resubscribe/restart (a CheckpointStore makes the restart resume where it
// left off); a context-cancelled shutdown returns ctx.Err() instead and is
// never wrapped in this sentinel (issue #246).
var ErrSubscriptionClosed = errors.New("projection: event subscription closed unexpectedly")

// Projector processes events to build read models.
//
// Idempotency requirement: Project MUST be idempotent — applying the same event
// (identified by its GlobalPosition, or a natural key in the event) more than
// once must not corrupt the read model. Delivery through Start is at-least-once:
//   - the underlying subscription may redeliver (see eventstore.Store.Subscribe);
//   - checkpoint-based resume replays from the last saved position, so the
//     event at that position (and any after it that a sibling projector had
//     already applied) is re-dispatched to every projector;
//   - within a single ProcessEvent call, if projector k fails, projectors
//     1..k-1 have already applied the event, so a caller-level retry re-applies
//     it to them.
//
// Typical idempotent strategies: UPSERT keyed by entity id, or skip events whose
// GlobalPosition is not greater than the last one recorded for that read model.
type Projector interface {
	// Project processes a single event to update a read model. It must be
	// idempotent (see the interface doc).
	Project(ctx context.Context, event eventstore.Event) error

	// Rebuild rebuilds the read model by replaying all events up to the given time.
	Rebuild(ctx context.Context, until time.Time) error
}

// ProjectionOptions configures the ProjectionService.
type ProjectionOptions struct {
	SyncMode  bool
	BatchSize int
	// MaxRetries is the number of RETRIES per projector per event (in addition
	// to the initial attempt). MaxRetries=0 means a single attempt with no
	// retry; MaxRetries=2 means up to 3 attempts. Negative values are treated
	// as 0.
	MaxRetries int
	RetryDelay time.Duration
	Logger     *slog.Logger

	// ProjectionName identifies this projection for checkpointing. Defaults to
	// "default" when empty. Give each independently-checkpointed ProjectionService
	// a distinct name.
	ProjectionName string

	// CheckpointStore, when non-nil, is loaded on Start to resume from the last
	// processed global position and is saved after each successfully-processed
	// event. When nil, Start always subscribes from position 0 (full history +
	// live tail) and keeps no durable progress.
	CheckpointStore CheckpointStore
}

// ProjectionService manages projectors and processes events from the event store.
type ProjectionService struct {
	eventStore eventstore.Store
	projectors []Projector
	options    ProjectionOptions
}

// projectionName returns the checkpoint key for this service.
func (s *ProjectionService) projectionName() string {
	if s.options.ProjectionName != "" {
		return s.options.ProjectionName
	}
	return "default"
}

// NewProjectionService creates a new ProjectionService.
func NewProjectionService(eventStore eventstore.Store, options ProjectionOptions) *ProjectionService {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &ProjectionService{
		eventStore: eventStore,
		options:    options,
	}
}

// RegisterProjector adds a projector to the service.
//
// NOT concurrency-safe with Start/RebuildAll: register all projectors during
// single-threaded setup BEFORE calling Start (or RebuildAll). The projectors
// slice is read without synchronization by ProcessEvent, so appending to it
// while Start's event loop is running is a data race and may cause a projector
// to miss events. If dynamic registration after Start is required, the caller
// must provide external synchronization (issue #162 L3).
func (s *ProjectionService) RegisterProjector(p Projector) {
	s.projectors = append(s.projectors, p)
}

// Start begins processing events from the event store subscription.
// It blocks until the context is cancelled or the subscription ends.
//
// Return value: on graceful shutdown (the context is cancelled, including the
// case where the store closes the subscription channel in response to that
// cancellation) Start returns ctx.Err(). If the subscription channel closes
// while the context is still active, Start returns ErrSubscriptionClosed so a
// supervisor can distinguish a dead event feed from a requested shutdown and
// restart the projection (issue #246). In SyncMode a projector failure is
// returned as-is (see below).
//
// Checkpointing: when ProjectionOptions.CheckpointStore is set, Start loads the
// last processed global position on entry and subscribes from there (so a
// restart resumes exactly where it left off, closing the "events appended while
// down are never delivered" gap). After every successfully-processed event it
// saves the event's GlobalPosition. The checkpoint is NEVER advanced past an
// event that failed, so a resume redelivers the failed event and everything
// after it (Projector must be idempotent — see Projector).
//
// Failure semantics (IMPORTANT):
//   - SyncMode=true: a projector failure (after MaxRetries retries) aborts Start
//     and returns the error, so the caller can stop and retry. The checkpoint is
//     not advanced past the failing event.
//   - SyncMode=false (async): a projector failure is logged and the loop
//     advances to the next event to keep the read model live. The checkpoint is
//     FROZEN at the last fully-successful position for the rest of this run, so
//     no progress is persisted past the failed event; on the next restart the
//     failed event (and everything after) is redelivered from the checkpoint.
//     Without a CheckpointStore there is no durable progress, so a
//     permanently-failing event leaves the read model divergent until a full
//     RebuildAll. The reliability of the underlying subscription is a property
//     of the consumer's eventstore.Store implementation (the in-memory reference
//     store is lossless — see its Subscribe doc).
func (s *ProjectionService) Start(ctx context.Context) error {
	var fromPosition int64
	if s.options.CheckpointStore != nil {
		pos, err := s.options.CheckpointStore.Load(ctx, s.projectionName())
		if err != nil {
			return fmt.Errorf("failed to load checkpoint for projection %q: %w", s.projectionName(), err)
		}
		fromPosition = pos
	}

	eventCh, err := s.eventStore.Subscribe(ctx, fromPosition)
	if err != nil {
		return fmt.Errorf("failed to subscribe to events: %w", err)
	}

	// checkpointFrozen is set once an async-mode failure occurs, so the
	// checkpoint is never advanced past an event that has not been fully applied.
	checkpointFrozen := false

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-eventCh:
			if !ok {
				// Distinguish "the caller shut us down" from "the feed died".
				// The reference in-memory store closes the subscription channel
				// IN RESPONSE to ctx cancellation, so this select can observe
				// the closed channel before (or instead of) <-ctx.Done();
				// classifying that race as a failure would be flaky. When the
				// context is done the close is part of a graceful shutdown and
				// Start returns ctx.Err(), exactly as the <-ctx.Done() branch
				// does. Only a close with a live context is abnormal (#246).
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return ErrSubscriptionClosed
			}
			if err := s.ProcessEvent(ctx, event); err != nil {
				if s.options.SyncMode {
					return err
				}
				s.options.Logger.Error("projection failed in async mode",
					"eventType", event.Type,
					"streamID", event.StreamID,
					"version", event.Version,
					"globalPosition", event.GlobalPosition,
					"error", err,
				)
				// Freeze the checkpoint: do not persist progress past a failed
				// event, so a restart redelivers it (issue #192).
				checkpointFrozen = true
				continue
			}

			if s.options.CheckpointStore != nil && !checkpointFrozen {
				if serr := s.options.CheckpointStore.Save(ctx, s.projectionName(), event.GlobalPosition); serr != nil {
					// A failed checkpoint save must not advance progress either;
					// freeze and keep the read model live.
					s.options.Logger.Error("failed to save projection checkpoint",
						"projection", s.projectionName(),
						"globalPosition", event.GlobalPosition,
						"error", serr,
					)
					checkpointFrozen = true
				}
			}
		}
	}
}

// defaultBatchSize is used when ProjectionOptions.BatchSize is not set.
const defaultBatchSize = 1000

// RebuildAll rebuilds all projections by loading events across all streams
// using LoadAll and dispatching them to all registered projectors in global
// position order. Events are fetched in batches controlled by BatchSize.
//
// Unlike Projector.Rebuild (which delegates rebuilding to each projector),
// RebuildAll streams events in global position order across all streams,
// ensuring consistent cross-stream ordering for all registered projectors.
//
// Callers are responsible for clearing existing projection data before
// calling RebuildAll (e.g., TRUNCATE projection tables).
func (s *ProjectionService) RebuildAll(ctx context.Context) error {
	batchSize := s.options.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}

	var fromPosition int64
	for {
		events, err := s.eventStore.LoadAll(ctx, fromPosition, batchSize)
		if err != nil {
			return fmt.Errorf("failed to load events from position %d: %w", fromPosition, err)
		}
		if len(events) == 0 {
			break
		}

		for _, event := range events {
			if err := s.ProcessEvent(ctx, event); err != nil {
				return fmt.Errorf("rebuild failed at global position %d: %w", event.GlobalPosition, err)
			}
		}

		newPosition := events[len(events)-1].GlobalPosition
		if newPosition == fromPosition {
			return fmt.Errorf("LoadAll returned events but global position did not advance (stuck at %d)", fromPosition)
		}
		fromPosition = newPosition
	}

	return nil
}

// ProcessEvent dispatches an event to all registered projectors.
//
// Note: if projector k fails, projectors 1..k-1 have already applied the event.
// A caller that retries ProcessEvent (or resumes from a checkpoint) re-applies
// the event to those projectors, which is why Projector.Project must be
// idempotent (see Projector).
func (s *ProjectionService) ProcessEvent(ctx context.Context, event eventstore.Event) error {
	// MaxRetries counts retries in ADDITION to the initial attempt, so the total
	// number of attempts is MaxRetries+1 (issue #192: the old code treated it as
	// a total-attempt count, so MaxRetries=1 gave zero retries).
	retries := s.options.MaxRetries
	if retries < 0 {
		retries = 0
	}
	maxAttempts := retries + 1

	for _, p := range s.projectors {
		var lastErr error
		for attempt := 0; attempt < maxAttempts; attempt++ {
			if err := p.Project(ctx, event); err != nil {
				lastErr = err
				if attempt < maxAttempts-1 && s.options.RetryDelay > 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(s.options.RetryDelay):
					}
				}
				continue
			}
			lastErr = nil
			break
		}
		if lastErr != nil {
			return fmt.Errorf("projector failed after %d attempts (%d retries): %w", maxAttempts, retries, lastErr)
		}
	}
	return nil
}
