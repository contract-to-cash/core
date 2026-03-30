---
sidebar_position: 4
---

# Event Sourcing

Contract Billing Core uses event sourcing for the Contract aggregate, providing a complete audit trail and time-travel capabilities. For the full Store interface and event type definitions, see the [Event Store API Reference](../api/event-store).

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

Every event includes metadata for audit and traceability (UserID, CorrelationID, CausationID, etc.). Optimistic locking via `expectedVersion` prevents concurrent write conflicts.

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
    fmt.Printf("[%s] %s by %s\n", entry.OccurredAt.Format(time.RFC3339), entry.EventType, entry.UserID)
}
```

| Use Case | How |
|----------|-----|
| Billing dispute investigation | `GetContractAsOf` at the disputed billing date |
| Regulatory audit | `GetContractHistory` for full audit trail |
| Debugging state issues | Compare state at different points in time |
| Price change analysis | Query state before and after a price revision |
| Compliance reporting | Reconstruct who changed what and when |

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

### Performance Considerations

- **Snapshots** significantly improve performance for contracts with many events. Use `SnapshotService` to create periodic snapshots.
- **LoadUntil** stops replaying events at the target time, so queries for recent states are faster.
- For bulk historical queries, consider building projections instead of querying individual aggregates.

## Projections

Use the projection service to build read-optimized views from events. See [ProjectionService](../api/services#projectionservice) for the full API.

```go
projectionService := projection.NewProjectionService(eventStore, projection.ProjectionOptions{
    SyncMode:   true,
    BatchSize:  100,
    MaxRetries: 3,
})
projectionService.RegisterProjector(myProjector)
projectionService.Start(ctx)
```

## Example: Time Travel Demo

This example demonstrates time-travel across 5 months of contract changes:

| Time | Action | State |
|------|--------|-------|
| Apr 1 (T1) | Create & activate | active, ¥3,000/month |
| May 1 (T2) | Change price | active, ¥5,000/month |
| Jun 1 (T3) | Suspend (payment overdue) | suspended |
| Jul 1 (T4) | Resume | active, ¥5,000/month |
| Aug 1 (T5) | Cancel | cancelled |

```go
queryService := query.NewTemporalQueryService(eventStore, clock)

// Time travel to any past point
historical, _ := queryService.GetContractAsOf(ctx, contractID, may1)
// Status: active, Price: ¥5,000 (after the price change on May 1)
```

A snapshot created at T3 reduces replay cost:

```
Full replay:     6 events
Snapshot + replay: 3 events (snapshot at v3 + 3 remaining)
```

```bash
go run ./examples/event-sourcing-demo/
```
