package query

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
)

// Test time constants for deterministic time control.
var (
	t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 = time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	t2 = time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	t3 = time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	t5 = time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC)
)

func testMoney() shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
}

func testMetadata() eventstore.EventMetadata {
	return eventstore.EventMetadata{UserID: "test-user"}
}

// createContractEvents creates a ContractAggregate, runs the given commands,
// and returns the uncommitted events. The clock is set to the provided time
// so that OccurredAt is deterministic.
func createContractEvents(t *testing.T, contractID shared.ContractID, clockTime time.Time, commands func(agg *contract.ContractAggregate, meta eventstore.EventMetadata) error) []eventstore.Event {
	t.Helper()
	clock := shared.FixedClock{FixedTime: clockTime}
	agg := contract.NewContractAggregate(contractID, clock)
	meta := testMetadata()
	if err := commands(agg, meta); err != nil {
		t.Fatalf("command execution failed: %v", err)
	}
	events := agg.UncommittedEvents()
	result := make([]eventstore.Event, len(events))
	copy(result, events)
	return result
}

// appendEvents appends events to the event store at the given expected version.
func appendEvents(t *testing.T, store eventstore.Store, streamID string, events []eventstore.Event, expectedVersion int) {
	t.Helper()
	ctx := context.Background()
	if err := store.Append(ctx, streamID, events, expectedVersion); err != nil {
		t.Fatalf("failed to append events: %v", err)
	}
}

// createSnapshotFromAggregate creates a snapshot by building an aggregate from events,
// then marshalling its state.
func createSnapshotFromAggregate(t *testing.T, contractID shared.ContractID, store eventstore.Store, snapshotTime time.Time) eventstore.Snapshot {
	t.Helper()
	ctx := context.Background()
	clock := shared.FixedClock{FixedTime: snapshotTime}
	agg := contract.NewContractAggregate(contractID, clock)

	events, err := store.Load(ctx, string(contractID))
	if err != nil {
		t.Fatalf("failed to load events for snapshot: %v", err)
	}
	if err := agg.LoadFromHistory(events); err != nil {
		t.Fatalf("failed to load history for snapshot: %v", err)
	}

	state, err := agg.MarshalSnapshot()
	if err != nil {
		t.Fatalf("failed to marshal snapshot: %v", err)
	}

	return eventstore.Snapshot{
		StreamID:  string(contractID),
		Version:   agg.Version(),
		State:     state,
		AsOf:      snapshotTime,
		CreatedAt: snapshotTime,
	}
}

// setupContractWithCreateAndActivate creates a contract with Create at clockCreate
// and Activate at clockActivate, appending both event batches to the store.
func setupContractWithCreateAndActivate(t *testing.T, store eventstore.Store, contractID shared.ContractID, clockCreate, clockActivate time.Time) {
	t.Helper()
	streamID := string(contractID)

	// Create event
	createEvents := createContractEvents(t, contractID, clockCreate, func(agg *contract.ContractAggregate, meta eventstore.EventMetadata) error {
		return agg.Create(contract.CreateContractCommand{
			AccountID:    shared.AccountID("acc-001"),
			PriceID:      shared.PriceID("price-001"),
			ContractType: contract.ContractTypeSubscription,
			Interval:     pricing.Monthly(),
			Price:        testMoney(),
			BasePrice:    testMoney(),
			AutoRenew:    true,
		}, meta)
	})
	appendEvents(t, store, streamID, createEvents, 0)

	// Activate event: build aggregate from stored events, then activate
	clock2 := shared.FixedClock{FixedTime: clockActivate}
	agg2 := contract.NewContractAggregate(contractID, clock2)
	ctx := context.Background()
	storedEvents, err := store.Load(ctx, streamID)
	if err != nil {
		t.Fatalf("failed to load events: %v", err)
	}
	if err := agg2.LoadFromHistory(storedEvents); err != nil {
		t.Fatalf("failed to load history: %v", err)
	}
	if err := agg2.Activate(testMetadata()); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	activateEvents := agg2.UncommittedEvents()
	appendEvents(t, store, streamID, activateEvents, 1)
}

func TestGetContractAsOf_WithoutSnapshot(t *testing.T) {
	clock := shared.FixedClock{FixedTime: t0}
	store := inmemory.NewInMemoryEventStore(clock)
	svc := NewTemporalQueryService(store, clock)
	ctx := context.Background()
	contractID := shared.ContractID("contract-no-snap")

	setupContractWithCreateAndActivate(t, store, contractID, t1, t2)

	// Query at t2 should show active status (both events applied)
	agg, err := svc.GetContractAsOf(ctx, contractID, t2)
	if err != nil {
		t.Fatalf("GetContractAsOf failed: %v", err)
	}
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("expected status active, got %s", agg.Status())
	}
	if agg.AccountID() != shared.AccountID("acc-001") {
		t.Errorf("expected account acc-001, got %s", agg.AccountID())
	}

	// Query at t1 should show draft status (only create event applied)
	aggDraft, err := svc.GetContractAsOf(ctx, contractID, t1)
	if err != nil {
		t.Fatalf("GetContractAsOf at t1 failed: %v", err)
	}
	if aggDraft.Status() != contract.ContractStatusDraft {
		t.Errorf("expected status draft at t1, got %s", aggDraft.Status())
	}
}

// TestGetContractAsOf_IgnoresSnapshotCoveringPostAsOfEvents guards review W7:
// snapshot selection is by CreatedAt while events are bounded by OccurredAt. If a
// (backdated) snapshot was physically created before asOf but covers an event that
// OCCURRED after asOf, applying it would leak post-asOf state. The service must
// detect this (snapshot.Version beyond the asOf event horizon) and replay from
// scratch instead.
func TestGetContractAsOf_IgnoresSnapshotCoveringPostAsOfEvents(t *testing.T) {
	clock := shared.FixedClock{FixedTime: t0}
	store := inmemory.NewInMemoryEventStore(clock)
	svc := NewTemporalQueryService(store, clock)
	ctx := context.Background()
	contractID := shared.ContractID("contract-backdated-snap")

	// Create occurred at t1, Activate occurred at t3.
	setupContractWithCreateAndActivate(t, store, contractID, t1, t3)

	// A snapshot at version 2 (Active) but with CreatedAt backdated to t0 (before
	// the asOf below). Under a monotonic clock this is impossible, but a
	// misbehaving/consumer event store could produce it — the query must be robust.
	snap := createSnapshotFromAggregate(t, contractID, store, t0)
	if err := store.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveSnapshot failed: %v", err)
	}

	// asOf = t2 is between Create (t1) and Activate (t3). The contract was Draft.
	agg, err := svc.GetContractAsOf(ctx, contractID, t2)
	if err != nil {
		t.Fatalf("GetContractAsOf failed: %v", err)
	}
	if agg.Status() != contract.ContractStatusDraft {
		t.Errorf("expected status draft at t2 (snapshot covering the t3 activate must be ignored), got %s", agg.Status())
	}
}

func TestGetContractAsOf_WithSnapshot(t *testing.T) {
	clock := shared.FixedClock{FixedTime: t0}
	store := inmemory.NewInMemoryEventStore(clock)
	svc := NewTemporalQueryService(store, clock)
	ctx := context.Background()
	contractID := shared.ContractID("contract-with-snap")

	setupContractWithCreateAndActivate(t, store, contractID, t1, t2)

	// Create a snapshot after Create (version 1, at t1+30min)
	// We need to build a partial aggregate from just the first event.
	snapshotTime := t1.Add(30 * time.Minute)
	partialClock := shared.FixedClock{FixedTime: snapshotTime}
	partialAgg := contract.NewContractAggregate(contractID, partialClock)
	firstEvents, err := store.LoadUntilVersion(ctx, string(contractID), 1)
	if err != nil {
		t.Fatalf("LoadUntilVersion failed: %v", err)
	}
	if err := partialAgg.LoadFromHistory(firstEvents); err != nil {
		t.Fatalf("LoadFromHistory for snapshot failed: %v", err)
	}
	snapState, err := partialAgg.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot failed: %v", err)
	}
	snap := eventstore.Snapshot{
		StreamID:  string(contractID),
		Version:   partialAgg.Version(),
		State:     snapState,
		AsOf:      snapshotTime,
		CreatedAt: snapshotTime,
	}
	if err := store.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveSnapshot failed: %v", err)
	}

	// Query at t2: should restore from snapshot (draft at v1) + apply activate event
	agg, err := svc.GetContractAsOf(ctx, contractID, t2)
	if err != nil {
		t.Fatalf("GetContractAsOf failed: %v", err)
	}
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("expected status active after snapshot+events, got %s", agg.Status())
	}
	if agg.AccountID() != shared.AccountID("acc-001") {
		t.Errorf("expected account acc-001, got %s", agg.AccountID())
	}
	// Verify version is correct (snapshot v1 + 1 replayed event = v2)
	if agg.Version() != 2 {
		t.Errorf("expected version 2, got %d", agg.Version())
	}
}

func TestGetContractAsOf_SnapshotOnly(t *testing.T) {
	clock := shared.FixedClock{FixedTime: t0}
	store := inmemory.NewInMemoryEventStore(clock)
	svc := NewTemporalQueryService(store, clock)
	ctx := context.Background()
	contractID := shared.ContractID("contract-snap-only")

	setupContractWithCreateAndActivate(t, store, contractID, t1, t2)

	// Create snapshot at t2 with full state (version 2, active)
	snap := createSnapshotFromAggregate(t, contractID, store, t2)
	if err := store.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveSnapshot failed: %v", err)
	}

	// Query at t3: snapshot (CreatedAt=t2) is before t3 so LoadSnapshotBefore returns it.
	// The snapshot covers all events (version 2), so no additional replay is needed.
	agg, err := svc.GetContractAsOf(ctx, contractID, t3)
	if err != nil {
		t.Fatalf("GetContractAsOf failed: %v", err)
	}
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("expected status active from snapshot, got %s", agg.Status())
	}
	if agg.Version() != 2 {
		t.Errorf("expected version 2 from snapshot, got %d", agg.Version())
	}
}

func TestGetContractAsOf_FutureTimestamp(t *testing.T) {
	clock := shared.FixedClock{FixedTime: t0}
	store := inmemory.NewInMemoryEventStore(clock)
	svc := NewTemporalQueryService(store, clock)
	ctx := context.Background()
	contractID := shared.ContractID("contract-future")

	setupContractWithCreateAndActivate(t, store, contractID, t1, t2)

	// Query at t5 (far future): should still return latest state
	agg, err := svc.GetContractAsOf(ctx, contractID, t5)
	if err != nil {
		t.Fatalf("GetContractAsOf at future time failed: %v", err)
	}
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("expected status active at future time, got %s", agg.Status())
	}
	if agg.Version() != 2 {
		t.Errorf("expected version 2, got %d", agg.Version())
	}
}

func TestGetContractAsOf_BeforeCreation(t *testing.T) {
	clock := shared.FixedClock{FixedTime: t0}
	store := inmemory.NewInMemoryEventStore(clock)
	svc := NewTemporalQueryService(store, clock)
	ctx := context.Background()
	contractID := shared.ContractID("contract-before-creation")

	setupContractWithCreateAndActivate(t, store, contractID, t2, t3)

	// Query at t1 (before any events): should return empty aggregate
	agg, err := svc.GetContractAsOf(ctx, contractID, t1)
	if err != nil {
		t.Fatalf("GetContractAsOf before creation failed: %v", err)
	}
	// Empty aggregate has zero-value status
	if agg.Status() != "" {
		t.Errorf("expected empty status before creation, got %q", agg.Status())
	}
	if agg.Version() != 0 {
		t.Errorf("expected version 0 before creation, got %d", agg.Version())
	}
}

func TestGetContractHistory_ReturnsAllEvents(t *testing.T) {
	clock := shared.FixedClock{FixedTime: t0}
	store := inmemory.NewInMemoryEventStore(clock)
	svc := NewTemporalQueryService(store, clock)
	ctx := context.Background()
	contractID := shared.ContractID("contract-history")

	setupContractWithCreateAndActivate(t, store, contractID, t1, t2)

	history, err := svc.GetContractHistory(ctx, contractID)
	if err != nil {
		t.Fatalf("GetContractHistory failed: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}

	// Verify event types
	if history[0].EventType != string(contract.EventTypeContractCreated) {
		t.Errorf("expected first event to be contract.created, got %s", history[0].EventType)
	}
	if history[1].EventType != string(contract.EventTypeContractActivated) {
		t.Errorf("expected second event to be contract.activated, got %s", history[1].EventType)
	}

	// Verify UserID is populated from metadata
	if history[0].UserID != "test-user" {
		t.Errorf("expected UserID 'test-user', got %q", history[0].UserID)
	}

	// Verify Data is non-empty
	if len(history[0].Data) == 0 {
		t.Error("expected non-empty Data for first event")
	}
	if len(history[1].Data) == 0 {
		t.Error("expected non-empty Data for second event")
	}
}

func TestGetContractHistory_EmptyStream(t *testing.T) {
	clock := shared.FixedClock{FixedTime: t0}
	store := inmemory.NewInMemoryEventStore(clock)
	svc := NewTemporalQueryService(store, clock)
	ctx := context.Background()

	history, err := svc.GetContractHistory(ctx, shared.ContractID("nonexistent"))
	if err != nil {
		t.Fatalf("GetContractHistory for empty stream failed: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("expected 0 entries for empty stream, got %d", len(history))
	}
}

func TestGetContractAsOf_WithSnapshot_FiltersEventsCorrectly(t *testing.T) {
	// This test verifies the version-based filtering logic:
	// Events with Version <= snapshot.Version should be skipped.
	clock := shared.FixedClock{FixedTime: t0}
	store := inmemory.NewInMemoryEventStore(clock)
	svc := NewTemporalQueryService(store, clock)
	ctx := context.Background()
	contractID := shared.ContractID("contract-filter-test")
	streamID := string(contractID)

	// Create contract at t1
	createEvents := createContractEvents(t, contractID, t1, func(agg *contract.ContractAggregate, meta eventstore.EventMetadata) error {
		return agg.Create(contract.CreateContractCommand{
			AccountID:    shared.AccountID("acc-002"),
			PriceID:      shared.PriceID("price-002"),
			ContractType: contract.ContractTypeSubscription,
			Interval:     pricing.Monthly(),
			Price:        testMoney(),
			BasePrice:    testMoney(),
		}, meta)
	})
	appendEvents(t, store, streamID, createEvents, 0)

	// Activate at t2
	clock2 := shared.FixedClock{FixedTime: t2}
	agg2 := contract.NewContractAggregate(contractID, clock2)
	storedEvents, err := store.Load(ctx, streamID)
	if err != nil {
		t.Fatalf("failed to load events: %v", err)
	}
	if err := agg2.LoadFromHistory(storedEvents); err != nil {
		t.Fatalf("failed to load history: %v", err)
	}
	if err := agg2.Activate(testMetadata()); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	appendEvents(t, store, streamID, agg2.UncommittedEvents(), 1)

	// Suspend at t3
	clock3 := shared.FixedClock{FixedTime: t3}
	agg3 := contract.NewContractAggregate(contractID, clock3)
	storedEvents, err = store.Load(ctx, streamID)
	if err != nil {
		t.Fatalf("failed to load events: %v", err)
	}
	if err := agg3.LoadFromHistory(storedEvents); err != nil {
		t.Fatalf("failed to load history: %v", err)
	}
	if err := agg3.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingSkip,
		Reason:          "test",
	}, testMetadata()); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}
	appendEvents(t, store, streamID, agg3.UncommittedEvents(), 2)

	// Create snapshot at t2 covering Create+Activate (version 2)
	snapClock := shared.FixedClock{FixedTime: t2}
	snapAgg := contract.NewContractAggregate(contractID, snapClock)
	eventsForSnap, err := store.LoadUntilVersion(ctx, streamID, 2)
	if err != nil {
		t.Fatalf("failed to load events for snapshot: %v", err)
	}
	if err := snapAgg.LoadFromHistory(eventsForSnap); err != nil {
		t.Fatalf("failed to load history for snapshot: %v", err)
	}
	snapState, err := snapAgg.MarshalSnapshot()
	if err != nil {
		t.Fatalf("failed to marshal snapshot: %v", err)
	}
	snap := eventstore.Snapshot{
		StreamID:  streamID,
		Version:   2,
		State:     snapState,
		AsOf:      t2,
		CreatedAt: t2,
	}
	if err := store.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("failed to save snapshot: %v", err)
	}

	// Query at t3: should restore from snapshot (active at v2) + apply suspend event
	result, err := svc.GetContractAsOf(ctx, contractID, t3)
	if err != nil {
		t.Fatalf("GetContractAsOf failed: %v", err)
	}
	if result.Status() != contract.ContractStatusSuspended {
		t.Errorf("expected status suspended, got %s", result.Status())
	}
	if result.Version() != 3 {
		t.Errorf("expected version 3, got %d", result.Version())
	}
}

func TestGetContractAsOf_NonexistentContract(t *testing.T) {
	clock := shared.FixedClock{FixedTime: t0}
	store := inmemory.NewInMemoryEventStore(clock)
	svc := NewTemporalQueryService(store, clock)
	ctx := context.Background()

	// Querying a nonexistent contract should return an empty aggregate without error
	agg, err := svc.GetContractAsOf(ctx, shared.ContractID("does-not-exist"), t5)
	if err != nil {
		t.Fatalf("GetContractAsOf for nonexistent contract should not error, got: %v", err)
	}
	if agg.Status() != "" {
		t.Errorf("expected empty status for nonexistent contract, got %q", agg.Status())
	}
	if agg.Version() != 0 {
		t.Errorf("expected version 0 for nonexistent contract, got %d", agg.Version())
	}
}
