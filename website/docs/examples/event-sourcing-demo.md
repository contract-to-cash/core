---
sidebar_position: 2
---

# Event Sourcing Demo

This example demonstrates time travel and audit capabilities of event sourcing.

## Running the Example

```bash
go run ./examples/event-sourcing-demo/
```

## What It Does

Creates a contract and applies multiple state changes over 5 months:

| Time | Action | State |
|------|--------|-------|
| Apr 1 (T1) | Create & activate | active, ¥3,000/month |
| May 1 (T2) | Change price | active, ¥5,000/month |
| Jun 1 (T3) | Suspend (payment overdue) | suspended |
| Jul 1 (T4) | Resume | active, ¥5,000/month |
| Aug 1 (T5) | Cancel | cancelled |

Then uses `TemporalQueryService` to query the contract state at each point:

```go
queryService := query.NewTemporalQueryService(eventStore, clock)

// Time travel to any past point
historical, _ := queryService.GetContractAsOf(ctx, contractID, may1)
// Status: active, Price: ¥5,000 (after the price change on May 1)
```

### Snapshot Recovery

A snapshot is created at T3 (after suspension). Later recovery only needs to replay events after the snapshot:

```
Full replay:     6 events
Snapshot + replay: 3 events (snapshot at v3 + 3 remaining)
```

## Key Takeaways

- `GetContractAsOf` reconstructs state at any past time
- Snapshots dramatically reduce replay cost for long-lived aggregates

> For concepts and API details, see [Event Sourcing](../concepts/event-sourcing.md) and [Temporal Queries Guide](../guides/temporal-queries.md).
