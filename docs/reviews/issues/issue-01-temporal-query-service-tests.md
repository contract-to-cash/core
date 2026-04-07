# Add tests for TemporalQueryService

**Labels**: `testing`, `high-priority`
**Source**: codebase-review-20260327 item #6

## Problem

`application/query/temporal_query_service.go` (101 lines) has **zero test coverage**. This service handles point-in-time contract reconstruction — a core feature of the event sourcing architecture.

Untested logic includes:
- Snapshot-based restoration with post-snapshot event filtering
- `GetContractAsOf()` — reconstructs aggregate state at arbitrary past timestamps
- `GetContractHistory()` — retrieves full event history
- Edge case: what happens when no snapshot exists vs. when snapshot exists but no subsequent events

## Acceptance Criteria

- [ ] `TestGetContractAsOf_WithoutSnapshot` — full event replay from empty state
- [ ] `TestGetContractAsOf_WithSnapshot` — snapshot restoration + incremental events
- [ ] `TestGetContractAsOf_SnapshotOnly` — snapshot at exact point, no additional events
- [ ] `TestGetContractAsOf_FutureTimestamp` — requesting state beyond last event
- [ ] `TestGetContractAsOf_BeforeCreation` — requesting state before contract existed
- [ ] `TestGetContractHistory_ReturnsAllEvents` — verify complete history
- [ ] `TestGetContractHistory_EmptyStream` — no events for given ID
- [ ] All tests use `shared.FixedClock` and `inmemory.EventStore`
- [ ] Tests run with `-race` flag

## Files

- **Target**: `application/query/temporal_query_service.go`
- **New file**: `application/query/temporal_query_service_test.go`
