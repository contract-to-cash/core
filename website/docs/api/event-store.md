---
sidebar_position: 4
---

# Event Store Reference

## Store Interface

```go
import "github.com/contract-to-cash/core/eventstore"

type Store interface {
    // Append events with optimistic locking
    Append(ctx context.Context, streamID string, events []Event, expectedVersion int) error

    // Load all events for a stream
    Load(ctx context.Context, streamID string) ([]Event, error)

    // Load events up to a specific version
    LoadUntilVersion(ctx context.Context, streamID string, version int) ([]Event, error)

    // Load events up to a specific time (for temporal queries)
    LoadUntil(ctx context.Context, streamID string, until time.Time) ([]Event, error)

    // Load events within a time range
    LoadRange(ctx context.Context, streamID string, from, to time.Time) ([]Event, error)

    // Subscribe to all events (for projections)
    Subscribe(ctx context.Context, fromPosition int64) (<-chan Event, error)

    // Snapshot management
    SaveSnapshot(ctx context.Context, snapshot Snapshot) error
    LoadSnapshot(ctx context.Context, streamID string) (*Snapshot, error)
    LoadSnapshotBefore(ctx context.Context, streamID string, before time.Time) (*Snapshot, error)
}
```

## Event

```go
type Event struct {
    ID            string           // Unique event ID
    StreamID      string           // Aggregate ID
    Type          EventType        // Event type (e.g., "contract.created")
    Version       int              // Version within the stream
    SchemaVersion int              // For upcasting
    Data          json.RawMessage  // Event payload (JSON)
    Metadata      EventMetadata    // Audit information
    OccurredAt    time.Time        // Business timestamp
    RecordedAt    time.Time        // System timestamp
}
```

## EventMetadata

```go
type EventMetadata struct {
    UserID        string  // Who performed the action (required)
    IPAddress     *string // Client IP (optional)
    UserAgent     *string // Client user agent (optional)
    CorrelationID string  // Request correlation ID
    CausationID   string  // Causal event ID
}
```

## Snapshot

```go
type Snapshot struct {
    StreamID  string          // Aggregate ID
    Version   int             // Version at snapshot time
    State     json.RawMessage // Serialized aggregate state
    AsOf      time.Time       // Snapshot point in time
    CreatedAt time.Time       // When snapshot was created
}
```

## DomainEvent Interface

Events implement this interface to be serializable/deserializable:

```go
type DomainEvent interface {
    EventType() EventType
}
```

## BaseAggregate

Base implementation for event-sourced aggregates:

```go
type BaseAggregate struct { ... }

agg := eventstore.NewBaseAggregate(id, clock)

agg.ID() string
agg.Version() int
agg.SetVersion(v int)
agg.IncrementVersion()
agg.UncommittedEvents() []Event
agg.ClearUncommittedEvents()
agg.RaiseEvent(event DomainEvent, metadata EventMetadata) error
agg.Clock() Clock
```

## EventRegistry

Maps event types to Go structs for deserialization:

```go
registry := eventstore.NewEventRegistry()

registry.Register(event DomainEvent) error           // Register event type
registry.Serialize(event DomainEvent) (json.RawMessage, error)
registry.Deserialize(eventType EventType, data json.RawMessage) (DomainEvent, error)
```

## Upcaster

Handle event schema evolution:

```go
type Upcaster interface {
    SourceVersion() int
    TargetVersion() int
    Upcast(data json.RawMessage) (json.RawMessage, error)
}
```

## Contract Event Types

| EventType | Constant |
|-----------|----------|
| `contract.created` | `EventTypeContractCreated` |
| `contract.activated` | `EventTypeContractActivated` |
| `contract.suspended` | `EventTypeContractSuspended` |
| `contract.resumed` | `EventTypeContractResumed` |
| `contract.cancelled` | `EventTypeContractCancelled` |
| `contract.renewed` | `EventTypeContractRenewed` |
| `contract.expired` | `EventTypeContractExpired` |
| `contract.price_changed` | `EventTypePriceChanged` |
| `contract.price_change_scheduled` | `EventTypePriceChangeScheduled` |
| `contract.price_change_unscheduled` | `EventTypePriceChangeUnscheduled` |
| `contract.trial_started` | `EventTypeTrialStarted` |
| `contract.trial_ended` | `EventTypeTrialEnded` |

## In-Memory Implementation

For testing and demos:

```go
import "github.com/contract-to-cash/core/infrastructure/inmemory"

store := inmemory.NewInMemoryEventStore(clock)
```

Thread-safe, supports all Store interface methods including subscriptions and snapshots.
