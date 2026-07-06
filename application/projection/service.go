// Package projection provides event projection infrastructure
// for building read models from event streams.
package projection

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/contract-to-cash/core/eventstore"
)

// Projector processes events to build read models.
type Projector interface {
	// Project processes a single event to update a read model.
	Project(ctx context.Context, event eventstore.Event) error

	// Rebuild rebuilds the read model by replaying all events up to the given time.
	Rebuild(ctx context.Context, until time.Time) error
}

// ProjectionOptions configures the ProjectionService.
type ProjectionOptions struct {
	SyncMode   bool
	BatchSize  int
	MaxRetries int
	RetryDelay time.Duration
	Logger     *slog.Logger
}

// ProjectionService manages projectors and processes events from the event store.
type ProjectionService struct {
	eventStore eventstore.Store
	projectors []Projector
	options    ProjectionOptions
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
// It blocks until the context is cancelled.
//
// Failure semantics (IMPORTANT):
//   - SyncMode=true: a projector failure (after MaxRetries) aborts Start and
//     returns the error, so the caller can stop and retry — at-least-once within
//     the caller's control.
//   - SyncMode=false (async): a projector failure is logged and SKIPPED; the
//     loop advances to the next event. There is no built-in dead-letter queue or
//     checkpoint, so a permanently-failing event leaves the read model
//     divergent until a full RebuildAll. Choose SyncMode for at-least-once
//     delivery, or run RebuildAll to recover from divergence. The reliability of
//     the underlying subscription is a property of the consumer's eventstore.Store
//     implementation (the in-memory reference store is best-effort — see its
//     Subscribe doc).
func (s *ProjectionService) Start(ctx context.Context) error {
	eventCh, err := s.eventStore.Subscribe(ctx, 0)
	if err != nil {
		return fmt.Errorf("failed to subscribe to events: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-eventCh:
			if !ok {
				return nil
			}
			if err := s.ProcessEvent(ctx, event); err != nil {
				if s.options.SyncMode {
					return err
				}
				s.options.Logger.Error("projection failed in async mode",
					"eventType", event.Type,
					"streamID", event.StreamID,
					"version", event.Version,
					"error", err,
				)
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
func (s *ProjectionService) ProcessEvent(ctx context.Context, event eventstore.Event) error {
	for _, p := range s.projectors {
		var lastErr error
		maxRetries := s.options.MaxRetries
		if maxRetries <= 0 {
			maxRetries = 1
		}

		for attempt := 0; attempt < maxRetries; attempt++ {
			if err := p.Project(ctx, event); err != nil {
				lastErr = err
				if attempt < maxRetries-1 && s.options.RetryDelay > 0 {
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
			return fmt.Errorf("projector failed after %d retries: %w", maxRetries, lastErr)
		}
	}
	return nil
}
