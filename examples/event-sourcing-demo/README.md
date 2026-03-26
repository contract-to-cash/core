# Event Sourcing Demo

Demonstrates the "time travel" capability of event sourcing. A contract goes through 5 state changes over 5 months, and `TemporalQueryService` reconstructs the exact state at any past point.

## What You'll See

```
=== Time Travel: Contract State at Each Point ===

  Date           Status         Price
  ------------------------------------------
  T1 (Apr 1)     active         ¥3000
  T2 (May 1)     active         ¥5000      <- price changed
  T3 (Jun 1)     suspended      ¥5000      <- payment overdue
  T4 (Jul 1)     active         ¥5000      <- resumed
  T5 (Aug 1)     cancelled      ¥5000      <- customer left

=== Snapshot Recovery ===
  Snapshot at v4 -> only replay 2 events instead of 6
```

## Key Concepts

| Concept | Description |
|---------|-------------|
| **TemporalQueryService.GetContractAsOf()** | Reconstruct aggregate state at any point in time |
| **GetContractHistory()** | Full event history with timestamps and user IDs |
| **LoadFromHistory()** | Replay raw events to rebuild state from scratch |
| **Snapshot** | Save aggregate state periodically; skip replaying old events |
| **SnapshotService.CreateSnapshot()** | Serialize and persist aggregate state |

## Why This Matters

- **Audit compliance**: Answer "what was the contract state on June 15?" with certainty
- **Debugging**: Reproduce any past state without guessing
- **Performance**: Snapshots reduce replay cost from O(n) events to O(1) + recent events

## Run

```bash
go run ./examples/event-sourcing-demo/
```
