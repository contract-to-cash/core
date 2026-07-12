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
//
// It returns a (shallow) COPY of the internal slice: appending to or
// reordering the returned slice cannot corrupt the aggregate's pending-event
// state (stores already copy on their side before stamping RecordedAt /
// GlobalPosition; this closes the caller-mutation direction). The Event
// structs themselves are value copies, but reference fields (Data,
// Metadata contents) are shared — treat them as immutable.
func (a *BaseAggregate) UncommittedEvents() []Event {
	if len(a.uncommittedEvents) == 0 {
		return nil
	}
	cp := make([]Event, len(a.uncommittedEvents))
	copy(cp, a.uncommittedEvents)
	return cp
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

	// Determine the schema version to stamp. Events that self-declare via
	// SchemaVersioned are stamped at their true current version so they skip the
	// Upcaster on replay; all others default to 1.
	schemaVersion := 1
	if sv, ok := domainEvent.(SchemaVersioned); ok {
		schemaVersion = sv.CurrentSchemaVersion()
	}

	event := Event{
		ID:            shared.GenerateID(),
		StreamID:      a.id,
		Type:          domainEvent.EventType(),
		Version:       a.version + len(a.uncommittedEvents) + 1,
		SchemaVersion: schemaVersion,
		Data:          data,
		Metadata:      metadata,
		OccurredAt:    a.clock.Now(),
		// RecordedAt is set by the Event Store when persisting
	}

	a.uncommittedEvents = append(a.uncommittedEvents, event)
	return nil
}
