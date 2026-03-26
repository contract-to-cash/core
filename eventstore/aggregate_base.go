package eventstore

import (
	"encoding/json"
	"fmt"

	"github.com/contract-to-cash/core/domain/shared"
)

// BaseAggregate provides common aggregate root functionality.
// Domain aggregates should embed this struct.
type BaseAggregate struct {
	id                string
	version           int
	uncommittedEvents []Event
	clock             shared.Clock
}

// NewBaseAggregate creates a new BaseAggregate.
func NewBaseAggregate(id string, clock shared.Clock) BaseAggregate {
	return BaseAggregate{
		id:    id,
		clock: clock,
	}
}

// ID returns the aggregate ID.
func (a *BaseAggregate) ID() string {
	return a.id
}

// Version returns the current version.
func (a *BaseAggregate) Version() int {
	return a.version
}

// UncommittedEvents returns events not yet persisted.
func (a *BaseAggregate) UncommittedEvents() []Event {
	return a.uncommittedEvents
}

// ClearUncommittedEvents clears uncommitted events after persistence.
func (a *BaseAggregate) ClearUncommittedEvents() {
	a.uncommittedEvents = nil
}

// Clock returns the clock used by this aggregate.
func (a *BaseAggregate) Clock() shared.Clock {
	return a.clock
}

// IncrementVersion increments the aggregate version.
func (a *BaseAggregate) IncrementVersion() {
	a.version++
}

// SetVersion sets the aggregate version directly (for snapshot restore).
func (a *BaseAggregate) SetVersion(v int) {
	a.version = v
}

// RaiseEvent emits a typed domain event.
// It serializes the DomainEvent to JSON, wraps it in an Event envelope,
// and appends to uncommittedEvents.
func (a *BaseAggregate) RaiseEvent(domainEvent DomainEvent, metadata EventMetadata) error {
	data, err := json.Marshal(domainEvent)
	if err != nil {
		return fmt.Errorf("failed to marshal domain event: %w", err)
	}

	event := Event{
		ID:            shared.GenerateID(),
		StreamID:      a.id,
		Type:          domainEvent.EventType(),
		Version:       a.version + len(a.uncommittedEvents) + 1,
		SchemaVersion: 1,
		Data:          data,
		Metadata:      metadata,
		OccurredAt:    a.clock.Now(),
		// RecordedAt is set by the Event Store when persisting
	}

	a.uncommittedEvents = append(a.uncommittedEvents, event)
	return nil
}
