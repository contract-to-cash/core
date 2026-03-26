// Package projection provides event projection infrastructure
// for building read models from event streams.
package projection

import (
	"context"
	"fmt"
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
}

// ProjectionService manages projectors and processes events from the event store.
type ProjectionService struct {
	eventStore eventstore.Store
	projectors []Projector
	options    ProjectionOptions
}

// NewProjectionService creates a new ProjectionService.
func NewProjectionService(eventStore eventstore.Store, options ProjectionOptions) *ProjectionService {
	return &ProjectionService{
		eventStore: eventStore,
		options:    options,
	}
}

// RegisterProjector adds a projector to the service.
func (s *ProjectionService) RegisterProjector(p Projector) {
	s.projectors = append(s.projectors, p)
}

// Start begins processing events from the event store subscription.
// It blocks until the context is cancelled.
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
				// In sync mode, return the error; otherwise continue
				if s.options.SyncMode {
					return err
				}
				// In async mode, we log and continue (caller can wrap with logging)
			}
		}
	}
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
