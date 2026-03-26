package eventstore

// AggregateRoot is the interface for event-sourced aggregates.
type AggregateRoot interface {
	// ID returns the aggregate ID.
	ID() string
	// Version returns the current version.
	Version() int
	// UncommittedEvents returns events that have not yet been persisted.
	UncommittedEvents() []Event
	// ClearUncommittedEvents clears uncommitted events after persistence.
	ClearUncommittedEvents()
	// LoadFromHistory restores aggregate state by replaying events.
	LoadFromHistory(events []Event) error
	// LoadFromSnapshot restores aggregate state from a snapshot.
	LoadFromSnapshot(snapshot Snapshot) error
}

// Evolver applies a typed domain event to update aggregate state.
// Apply must be a pure function — no IO, no deserialization.
type Evolver interface {
	Apply(event DomainEvent) error
}

// SnapshotMarshaler is an optional interface that aggregates can implement
// to provide custom snapshot serialization. This is necessary because
// aggregates typically have unexported fields that json.Marshal cannot access.
type SnapshotMarshaler interface {
	MarshalSnapshot() ([]byte, error)
}
