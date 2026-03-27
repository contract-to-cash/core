---
sidebar_position: 2
---

# Event Sourcing

Contract Billing Core uses event sourcing for the Contract aggregate, providing a complete audit trail and time-travel capabilities.

## How It Works

Instead of storing only the current state, every state change is recorded as an immutable event:

```
ContractCreatedEvent     → status: draft, price: ¥3,000
ContractActivatedEvent   → status: active
PriceChangedEvent        → price: ¥5,000 (immediate)
ContractSuspendedEvent   → status: suspended
ContractResumedEvent     → status: active
ContractRenewedEvent     → new period, price promoted from pending
```

The current state is reconstructed by replaying all events from the beginning:

```go
agg := contract.NewContractAggregate(contractID, clock)
events, _ := eventStore.Load(ctx, string(contractID))
agg.LoadFromHistory(events)
// agg now reflects the current state
```

## Event Structure

Every event includes metadata for audit and traceability:

```go
type Event struct {
    ID            string           // Unique event ID
    StreamID      string           // Aggregate ID (contract ID)
    Type          EventType        // e.g., "contract.created"
    Version       int              // Sequential version within the stream
    SchemaVersion int              // For future upcasting
    Data          json.RawMessage  // Event-specific payload
    Metadata      EventMetadata    // Audit information
    OccurredAt    time.Time        // Business time
    RecordedAt    time.Time        // System time
}

type EventMetadata struct {
    UserID        string   // Who performed the action
    CorrelationID string   // Request tracing
    CausationID   string   // Causal event chain
    IPAddress     *string  // Optional
    UserAgent     *string  // Optional
}
```

## Temporal Queries

Query contract state at any past point in time:

```go
queryService := query.NewTemporalQueryService(eventStore, clock)

// What was the contract state on May 15th?
historical, _ := queryService.GetContractAsOf(ctx, contractID, may15th)
historical.Status() // "active"
historical.Price()  // ¥3,000 (before the price change)

// Full change history
history, _ := queryService.GetContractHistory(ctx, contractID)
for _, entry := range history {
    fmt.Printf("%s: %s (by %s)\n", entry.OccurredAt, entry.EventType, entry.UserID)
}
```

## Snapshots

For aggregates with many events, snapshots speed up loading:

```go
snapshotService := service.NewSnapshotService(eventStore, clock, snapshotInterval)

// Save a snapshot of the current state
snapshotService.CreateSnapshot(ctx, agg)

// Load uses snapshot + recent events (instead of all events)
snap, _ := eventStore.LoadSnapshot(ctx, string(contractID))
agg.LoadFromSnapshot(*snap)
// Then replay only events after the snapshot version
```

## Event Store Interface

Implement this interface for your database:

```go
type Store interface {
    Append(ctx context.Context, streamID string, events []Event, expectedVersion int) error
    Load(ctx context.Context, streamID string) ([]Event, error)
    LoadUntilVersion(ctx context.Context, streamID string, version int) ([]Event, error)
    LoadUntil(ctx context.Context, streamID string, until time.Time) ([]Event, error)
    LoadRange(ctx context.Context, streamID string, from, to time.Time) ([]Event, error)
    Subscribe(ctx context.Context, fromPosition int64) (<-chan Event, error)
    SaveSnapshot(ctx context.Context, snapshot Snapshot) error
    LoadSnapshot(ctx context.Context, streamID string) (*Snapshot, error)
    LoadSnapshotBefore(ctx context.Context, streamID string, before time.Time) (*Snapshot, error)
}
```

Optimistic locking via `expectedVersion` prevents concurrent write conflicts.

## Contract Events

| Event | Trigger |
|-------|---------|
| `ContractCreatedEvent` | `agg.Create()` |
| `ContractActivatedEvent` | `agg.Activate()` |
| `ContractSuspendedEvent` | `agg.Suspend()` |
| `ContractResumedEvent` | `agg.Resume()` |
| `ContractCancelledEvent` | `agg.Cancel()` |
| `ContractRenewedEvent` | `agg.Renew()` |
| `ContractExpiredEvent` | `agg.Renew()` when autoRenew=false |
| `PriceChangedEvent` | `agg.ChangePrice()` with IMMEDIATE policy |
| `PriceChangeScheduledEvent` | `agg.ChangePrice()` with END_OF_TERM policy |
| `PriceChangeUnscheduledEvent` | `agg.UnscheduleChange()` |
| `TrialStartedEvent` | `agg.StartTrial()` |
| `TrialEndedEvent` | `agg.EndTrial()` |

## Projections

Use the projection service to build read-optimized views from events:

```go
type Projector interface {
    Project(ctx context.Context, event eventstore.Event) error
    Rebuild(ctx context.Context, until time.Time) error
}

projectionService := projection.NewProjectionService(eventStore, projection.ProjectionOptions{
    SyncMode:   true,
    BatchSize:  100,
    MaxRetries: 3,
})
projectionService.RegisterProjector(myProjector)
projectionService.Start(ctx)
```
