package contract

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// advancingClock is a test clock whose time can be moved forward between
// operations, so a suspend and a later resume observe different timestamps
// (a FixedClock would make the suspension duration zero).
type advancingClock struct{ t time.Time }

func (c *advancingClock) Now() time.Time { return c.t }

// newActiveAggregateWithClock builds an Active subscription contract (monthly
// interval) using the supplied clock, so callers can control suspend/resume
// timing.
func newActiveAggregateWithClock(t *testing.T, clock shared.Clock) *ContractAggregate {
	t.Helper()
	agg := NewContractAggregate(shared.ContractID("test-contract-001"), clock)
	meta := newTestMetadata()
	if err := agg.Create(newTestCommand(), meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	return agg
}

func suspensionConfigsEqual(a, b *SuspensionConfiguration) bool {
	if a == nil || b == nil {
		return a == b
	}
	if !a.SuspendedAt.Equal(b.SuspendedAt) ||
		a.BillingBehavior != b.BillingBehavior ||
		a.ExtendContract != b.ExtendContract ||
		a.Reason != b.Reason {
		return false
	}
	switch {
	case a.ResumeDate == nil && b.ResumeDate == nil:
		return true
	case a.ResumeDate == nil || b.ResumeDate == nil:
		return false
	default:
		return a.ResumeDate.Equal(*b.ResumeDate)
	}
}

// TestSuspend_ReplayRoundTrip_FullConfig is the core regression for issue #194:
// a suspension configured with ExtendContract=true (and a ResumeDate) must
// survive the event round-trip. Before the fix, ExtendContract and SuspendedAt
// were dropped by Apply(ContractSuspendedEvent), so the replayed config differed
// from the live one.
func TestSuspend_ReplayRoundTrip_FullConfig(t *testing.T) {
	clock := newTestClock()
	agg := newActiveAggregateWithClock(t, clock)
	meta := newTestMetadata()

	resume := clock.Now().AddDate(0, 0, 30)
	cfg := SuspensionConfiguration{
		SuspendedAt:     clock.Now(),
		BillingBehavior: SuspensionBillingDefer,
		ResumeDate:      &resume,
		ExtendContract:  true,
		Reason:          "non-payment, extend on resume",
	}
	if err := agg.Suspend(cfg, meta); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}

	live := agg.SuspensionConfig()
	if live == nil {
		t.Fatal("expected suspension config after Suspend")
	}
	if !live.ExtendContract {
		t.Error("live SuspensionConfig().ExtendContract must be true (issue #194)")
	}
	if live.SuspendedAt.IsZero() {
		t.Error("live SuspensionConfig().SuspendedAt must not be zero (issue #194)")
	}

	// Replay the full stream into a fresh aggregate.
	replayed := NewContractAggregate(agg.ContractID(), newTestClock())
	if err := replayed.LoadFromHistory(agg.UncommittedEvents()); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	got := replayed.SuspensionConfig()
	if got == nil {
		t.Fatal("expected suspension config after replay")
	}
	if !suspensionConfigsEqual(live, got) {
		t.Errorf("replayed SuspensionConfig differs from live:\n live=%+v\n got =%+v", live, got)
	}
}

func TestContractSuspendedEventUpcaster_CanUpcast(t *testing.T) {
	u := &ContractSuspendedEventUpcaster{}

	if !u.CanUpcast(EventTypeContractSuspended, 1) {
		t.Error("expected CanUpcast=true for ContractSuspended v1")
	}
	// Must match ONLY the exact fromVersion so the fixpoint chain stays
	// order-independent (no <=N antipattern).
	if u.CanUpcast(EventTypeContractSuspended, 2) {
		t.Error("expected CanUpcast=false for ContractSuspended v2")
	}
	if u.CanUpcast(EventTypeContractSuspended, 0) {
		t.Error("expected CanUpcast=false for ContractSuspended v0")
	}
	if u.CanUpcast(EventTypeContractResumed, 1) {
		t.Error("expected CanUpcast=false for a different event type")
	}
}

// TestContractSuspendedEventUpcaster_V1Defaults verifies a legacy v1 payload
// (no extend_contract) upcasts to v2 with extend_contract defaulting to false
// while suspended_at is preserved.
func TestContractSuspendedEventUpcaster_V1Defaults(t *testing.T) {
	u := &ContractSuspendedEventUpcaster{}
	suspendedAt := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)

	legacy := map[string]interface{}{
		"contract_id":      "test-contract-001",
		"suspended_at":     suspendedAt,
		"billing_behavior": "skip",
		"reason":           "legacy suspension",
	}
	data, _ := json.Marshal(legacy)
	event := eventstore.Event{
		Type:          EventTypeContractSuspended,
		SchemaVersion: 1,
		Data:          data,
		OccurredAt:    time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC),
	}

	result, err := u.Upcast(event)
	if err != nil {
		t.Fatalf("Upcast failed: %v", err)
	}
	if result.SchemaVersion != 2 {
		t.Errorf("expected SchemaVersion=2, got %d", result.SchemaVersion)
	}

	domainEvent, err := contractEventRegistry.Deserialize(result.Type, result.Data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}
	susp, ok := domainEvent.(*ContractSuspendedEvent)
	if !ok {
		t.Fatalf("expected *ContractSuspendedEvent, got %T", domainEvent)
	}
	if susp.ExtendContract {
		t.Error("expected extend_contract to default to false for legacy v1 event")
	}
	if !susp.SuspendedAt.Equal(suspendedAt) {
		t.Errorf("expected suspended_at preserved as %s, got %s", suspendedAt, susp.SuspendedAt)
	}
}

// TestContractSuspendedEventUpcaster_MissingSuspendedAt_FallsBackToOccurredAt
// verifies the documented defensive fallback: a v1 payload lacking a real
// suspended_at anchor gets the event's OccurredAt, so the resume-extension math
// is never anchored at the zero time.
func TestContractSuspendedEventUpcaster_MissingSuspendedAt_FallsBackToOccurredAt(t *testing.T) {
	u := &ContractSuspendedEventUpcaster{}
	occurredAt := time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC)

	// No suspended_at key at all.
	legacy := map[string]interface{}{
		"contract_id":      "test-contract-001",
		"billing_behavior": "skip",
		"reason":           "legacy suspension without anchor",
	}
	data, _ := json.Marshal(legacy)
	event := eventstore.Event{
		Type:          EventTypeContractSuspended,
		SchemaVersion: 1,
		Data:          data,
		OccurredAt:    occurredAt,
	}

	result, err := u.Upcast(event)
	if err != nil {
		t.Fatalf("Upcast failed: %v", err)
	}

	domainEvent, err := contractEventRegistry.Deserialize(result.Type, result.Data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}
	susp, ok := domainEvent.(*ContractSuspendedEvent)
	if !ok {
		t.Fatalf("expected *ContractSuspendedEvent, got %T", domainEvent)
	}
	if !susp.SuspendedAt.Equal(occurredAt) {
		t.Errorf("expected suspended_at to fall back to OccurredAt %s, got %s", occurredAt, susp.SuspendedAt)
	}

	// A zero-valued suspended_at must also trigger the fallback.
	legacyZero := map[string]interface{}{
		"contract_id":      "test-contract-001",
		"suspended_at":     time.Time{},
		"billing_behavior": "skip",
	}
	zData, _ := json.Marshal(legacyZero)
	zEvent := eventstore.Event{Type: EventTypeContractSuspended, SchemaVersion: 1, Data: zData, OccurredAt: occurredAt}
	zResult, err := u.Upcast(zEvent)
	if err != nil {
		t.Fatalf("Upcast (zero suspended_at) failed: %v", err)
	}
	zDomain, err := contractEventRegistry.Deserialize(zResult.Type, zResult.Data)
	if err != nil {
		t.Fatalf("deserialize (zero suspended_at) failed: %v", err)
	}
	zSusp, ok := zDomain.(*ContractSuspendedEvent)
	if !ok {
		t.Fatalf("expected *ContractSuspendedEvent, got %T", zDomain)
	}
	if !zSusp.SuspendedAt.Equal(occurredAt) {
		t.Errorf("expected zero suspended_at to fall back to OccurredAt %s", occurredAt)
	}
}

// TestResume_ExtendContract_ExtendsPeriodLiveAndReplay verifies that resuming a
// contract whose suspension had ExtendContract=true pushes the billing period's
// end out by the suspension duration, and that the live mutation and a full
// replay produce identical periods (replay determinism, issue #194).
func TestResume_ExtendContract_ExtendsPeriodLiveAndReplay(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := &advancingClock{t: base}
	agg := newActiveAggregateWithClock(t, clock)
	meta := newTestMetadata()

	// Period established at activation: [2026-01-01, 2026-02-01).
	originalEnd := agg.CurrentPeriod().End()

	suspendTime := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	clock.t = suspendTime
	if err := agg.Suspend(SuspensionConfiguration{
		BillingBehavior: SuspensionBillingSkip,
		ExtendContract:  true,
		Reason:          "extend on resume",
	}, meta); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}

	resumeTime := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	clock.t = resumeTime
	if err := agg.Resume(meta); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	suspensionDuration := resumeTime.Sub(suspendTime) // 5 days
	wantEnd := originalEnd.Add(suspensionDuration)
	if !agg.CurrentPeriod().End().Equal(wantEnd) {
		t.Errorf("live: expected extended period end %s, got %s", wantEnd, agg.CurrentPeriod().End())
	}
	if !agg.CurrentPeriod().Start().Equal(base) {
		t.Errorf("live: period start must be unchanged %s, got %s", base, agg.CurrentPeriod().Start())
	}

	// Replay the full stream (clock-independent: Apply uses event timestamps).
	replayed := NewContractAggregate(agg.ContractID(), &advancingClock{t: base})
	if err := replayed.LoadFromHistory(agg.UncommittedEvents()); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}
	if !replayed.CurrentPeriod().Equals(agg.CurrentPeriod()) {
		t.Errorf("replay not deterministic: live=%s replay=%s", agg.CurrentPeriod(), replayed.CurrentPeriod())
	}
	if replayed.Status() != ContractStatusActive {
		t.Errorf("expected active after replay, got %s", replayed.Status())
	}
	if replayed.SuspensionConfig() != nil {
		t.Error("expected nil suspension config after resume replay")
	}
}

// TestResume_NoExtendContract_LeavesPeriod verifies the period is untouched when
// ExtendContract is false.
func TestResume_NoExtendContract_LeavesPeriod(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := &advancingClock{t: base}
	agg := newActiveAggregateWithClock(t, clock)
	meta := newTestMetadata()
	originalEnd := agg.CurrentPeriod().End()

	clock.t = base.AddDate(0, 0, 10)
	if err := agg.Suspend(SuspensionConfiguration{BillingBehavior: SuspensionBillingSkip}, meta); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}
	clock.t = base.AddDate(0, 0, 20)
	if err := agg.Resume(meta); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if !agg.CurrentPeriod().End().Equal(originalEnd) {
		t.Errorf("period end must be unchanged when ExtendContract=false: want %s, got %s", originalEnd, agg.CurrentPeriod().End())
	}
}

// TestSnapshotRoundTrip_SuspensionExtendContract verifies that a snapshot taken
// while suspended preserves the full SuspensionConfiguration (including
// ExtendContract and SuspendedAt), and that a ContractResumedEvent replayed on
// top of that snapshot still extends the period deterministically (issue #194).
func TestSnapshotRoundTrip_SuspensionExtendContract(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := &advancingClock{t: base}
	agg := newActiveAggregateWithClock(t, clock)
	meta := newTestMetadata()
	originalEnd := agg.CurrentPeriod().End()

	suspendTime := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	clock.t = suspendTime
	if err := agg.Suspend(SuspensionConfiguration{
		BillingBehavior: SuspensionBillingSkip,
		ExtendContract:  true,
		Reason:          "snapshot while suspended",
	}, meta); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}

	// Snapshot while suspended.
	data, err := agg.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot failed: %v", err)
	}
	restored := NewContractAggregate(agg.ContractID(), &advancingClock{t: base})
	if err := restored.LoadFromSnapshot(eventstore.Snapshot{
		StreamID: string(agg.ContractID()),
		Version:  agg.Version(),
		State:    data,
	}); err != nil {
		t.Fatalf("LoadFromSnapshot failed: %v", err)
	}

	sc := restored.SuspensionConfig()
	if sc == nil {
		t.Fatal("expected suspension config to survive snapshot round-trip")
	}
	if !sc.ExtendContract {
		t.Error("ExtendContract must survive snapshot round-trip")
	}
	if !sc.SuspendedAt.Equal(suspendTime) {
		t.Errorf("SuspendedAt must survive snapshot round-trip: want %s, got %s", suspendTime, sc.SuspendedAt)
	}

	// Resume on top of the snapshot must still honor ExtendContract.
	resumeTime := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	resumedEvent := &ContractResumedEvent{ContractID: agg.ContractID(), ResumedAt: resumeTime}
	if err := restored.Apply(resumedEvent); err != nil {
		t.Fatalf("Apply(ContractResumedEvent) failed: %v", err)
	}

	wantEnd := originalEnd.Add(resumeTime.Sub(suspendTime))
	if !restored.CurrentPeriod().End().Equal(wantEnd) {
		t.Errorf("post-snapshot resume: expected extended end %s, got %s", wantEnd, restored.CurrentPeriod().End())
	}
}
