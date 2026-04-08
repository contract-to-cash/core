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
```

- **OccurredAt** is the business time (when the action happened)
- **RecordedAt** is the system time (when the event was persisted)
- **SchemaVersion** enables event schema evolution via the Upcaster pattern

## Temporal Queries

You can reconstruct contract state at any past point in time. This enables billing dispute investigation, regulatory audits, and debugging.

```go
queryService := query.NewTemporalQueryService(eventStore, clock)
historical, _ := queryService.GetContractAsOf(ctx, contractID, may15th)
historical.Status() // "active"
historical.Price()  // ¥3,000 (before the price change)
```

> For detailed usage and examples, see [Temporal Queries Guide](../guides/temporal-queries.md) and [Event Sourcing Demo](../examples/event-sourcing-demo.md).

## Snapshots

For aggregates with many events, snapshots avoid replaying the entire event history:

```go
snapshotService := service.NewSnapshotService(eventStore, clock, 100) // every 100 events

// Loading uses: snapshot state + only events after the snapshot
snap, _ := eventStore.LoadSnapshot(ctx, string(contractID))
agg.LoadFromSnapshot(*snap)
// Then replay only events since the snapshot
```

## Optimistic Locking

Concurrent write conflicts are detected via `expectedVersion` in the Event Store's `Append` method. If another process has written events since you loaded the aggregate, the append fails with a version conflict error.

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

Projections build read-optimized views by subscribing to the event stream. You can choose synchronous (in-process) or asynchronous (background) updates.

> For the complete Event Store and Projection API, see [Event Store Reference](../api/event-store.md).

## Next Steps

- [Event Store Reference](../api/event-store.md) — Store interface, BaseAggregate, EventRegistry, Upcaster
- [Temporal Queries Guide](../guides/temporal-queries.md) — Detailed usage with code examples
- [Event Sourcing Demo](../examples/event-sourcing-demo.md) — Runnable time-travel demonstration
