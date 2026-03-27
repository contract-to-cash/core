---
sidebar_position: 3
---

# Temporal Queries

Temporal queries let you reconstruct contract state at any past point in time, enabling auditing, debugging, and historical analysis.

## GetContractAsOf

Reconstruct contract state at a specific timestamp:

```go
queryService := query.NewTemporalQueryService(eventStore, clock)

// What was the contract on May 15, 2026?
may15 := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
historical, err := queryService.GetContractAsOf(ctx, contractID, may15)
if err != nil {
    log.Fatal(err)
}

fmt.Println(historical.Status())  // "active"
fmt.Println(historical.Price())   // ¥3,000 (before June 1 price change)
```

Under the hood, this:
1. Tries to load the nearest snapshot before the target time
2. Replays events from the snapshot (or beginning) up to the target time
3. Returns the reconstructed aggregate

## GetContractHistory

Get the full chronological history of a contract:

```go
history, _ := queryService.GetContractHistory(ctx, contractID)
for _, entry := range history {
    fmt.Printf("[%s] %s by %s\n",
        entry.OccurredAt.Format(time.RFC3339),
        entry.EventType,
        entry.UserID,
    )
    // entry.Data contains the raw event JSON
}
```

Output:
```
[2026-04-01T00:00:00Z] contract.created by admin
[2026-04-01T00:00:00Z] contract.activated by admin
[2026-05-01T00:00:00Z] contract.price_changed by admin
[2026-06-01T00:00:00Z] contract.suspended by system
[2026-07-01T00:00:00Z] contract.resumed by admin
```

## Use Cases

| Use Case | How |
|----------|-----|
| Billing dispute investigation | `GetContractAsOf` at the disputed billing date |
| Regulatory audit | `GetContractHistory` for full audit trail |
| Debugging state issues | Compare state at different points in time |
| Price change analysis | Query state before and after a price revision |
| Compliance reporting | Reconstruct who changed what and when |

## Performance Considerations

- **Snapshots** significantly improve performance for contracts with many events. Use `SnapshotService` to create periodic snapshots.
- **LoadUntil** stops replaying events at the target time, so queries for recent states are faster.
- For bulk historical queries, consider building projections instead of querying individual aggregates.
