package contract

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// clockAt returns a FixedClock anchored at the given instant.
func clockAt(tm time.Time) shared.Clock { return shared.FixedClock{FixedTime: tm} }

// newAnchorAggregate creates an active, auto-renewing monthly contract whose
// activation instant is `activatedAt`, so the billing anchor day is
// activatedAt.Day().
func newAnchorAggregate(t *testing.T, activatedAt time.Time) *ContractAggregate {
	t.Helper()
	agg := NewContractAggregate(shared.ContractID("anchor-contract-001"), clockAt(activatedAt))
	cmd := newTestCommand()
	cmd.AutoRenew = true
	cmd.Interval = pricing.Monthly()
	if err := agg.Create(cmd, newTestMetadata()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(newTestMetadata()); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	return agg
}

// TestBillingAnchor_MonthEnd_NoDriftAcrossRenewals is the canonical issue #186
// scenario: a Jan 31 subscription must bill Feb 28 -> Mar 31 -> Apr 30 -> May 31,
// recovering the 31st anchor whenever the target month is long enough, and the
// stored anchor day must remain 31 throughout.
func TestBillingAnchor_MonthEnd_NoDriftAcrossRenewals(t *testing.T) {
	activatedAt := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	agg := newAnchorAggregate(t, activatedAt)

	if got := agg.BillingAnchorDay(); got != 31 {
		t.Fatalf("expected billing anchor day 31, got %d", got)
	}
	// Initial period end clamps Jan 31 + 1mo -> Feb 28.
	if want := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC); !agg.CurrentPeriod().End().Equal(want) {
		t.Fatalf("initial period end: expected %v, got %v", want, agg.CurrentPeriod().End())
	}

	wantEnds := []time.Time{
		time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
	}
	for i, want := range wantEnds {
		if err := agg.RenewWithInterval(pricing.Monthly(), newTestMetadata()); err != nil {
			t.Fatalf("renewal %d failed: %v", i+1, err)
		}
		if !agg.CurrentPeriod().End().Equal(want) {
			t.Fatalf("renewal %d: expected period end %v, got %v", i+1, want, agg.CurrentPeriod().End())
		}
		if got := agg.BillingAnchorDay(); got != 31 {
			t.Fatalf("renewal %d: anchor day drifted to %d (want 31)", i+1, got)
		}
	}
}

// TestBillingAnchor_MidMonth_Unaffected confirms non-month-end anchors are
// unchanged (behavior identical to before the fix).
func TestBillingAnchor_MidMonth_Unaffected(t *testing.T) {
	agg := newAnchorAggregate(t, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	if got := agg.BillingAnchorDay(); got != 15 {
		t.Fatalf("expected anchor 15, got %d", got)
	}
	if err := agg.RenewWithInterval(pricing.Monthly(), newTestMetadata()); err != nil {
		t.Fatalf("renew failed: %v", err)
	}
	if want := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC); !agg.CurrentPeriod().End().Equal(want) {
		t.Fatalf("expected %v, got %v", want, agg.CurrentPeriod().End())
	}
}

// TestBillingAnchor_TrialConversion_SetsAnchor verifies a converted trial
// establishes the anchor from its initial period, mirroring Activate.
func TestBillingAnchor_TrialConversion_SetsAnchor(t *testing.T) {
	endedAt := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	agg := NewContractAggregate(shared.ContractID("anchor-trial-001"), clockAt(endedAt))
	cmd := newTestCommand()
	cmd.AutoRenew = true
	cmd.Interval = pricing.Monthly()
	if err := agg.Create(cmd, newTestMetadata()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.StartTrial(TrialConfiguration{}, newTestMetadata()); err != nil {
		t.Fatalf("StartTrial failed: %v", err)
	}
	if err := agg.EndTrial(true, newTestMetadata()); err != nil {
		t.Fatalf("EndTrial failed: %v", err)
	}
	if got := agg.BillingAnchorDay(); got != 31 {
		t.Fatalf("expected anchor 31 after trial conversion, got %d", got)
	}
	if err := agg.RenewWithInterval(pricing.Monthly(), newTestMetadata()); err != nil {
		t.Fatalf("renew failed: %v", err)
	}
	// Feb 28 -> Mar 31 (anchor recovered).
	if want := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC); !agg.CurrentPeriod().End().Equal(want) {
		t.Fatalf("expected %v, got %v", want, agg.CurrentPeriod().End())
	}
}

// TestBillingAnchor_ReplayReconstructsAnchor proves event-history replay
// rehydrates the aggregate identically and reconstructs the billing anchor from
// the persisted stream (the anchor is derived, not stored on events).
func TestBillingAnchor_ReplayReconstructsAnchor(t *testing.T) {
	activatedAt := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	live := newAnchorAggregate(t, activatedAt)
	for i := 0; i < 3; i++ {
		if err := live.RenewWithInterval(pricing.Monthly(), newTestMetadata()); err != nil {
			t.Fatalf("renew failed: %v", err)
		}
	}

	// Rehydrate from the raw event stream.
	replayed := NewContractAggregate(live.ContractID(), clockAt(activatedAt))
	if err := replayed.LoadFromHistory(live.UncommittedEvents()); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	if replayed.BillingAnchorDay() != live.BillingAnchorDay() {
		t.Fatalf("anchor mismatch: replayed=%d live=%d", replayed.BillingAnchorDay(), live.BillingAnchorDay())
	}
	if replayed.BillingAnchorDay() != 31 {
		t.Fatalf("expected reconstructed anchor 31, got %d", replayed.BillingAnchorDay())
	}
	if !replayed.CurrentPeriod().Equals(live.CurrentPeriod()) {
		t.Fatalf("period mismatch: replayed=%s live=%s", replayed.CurrentPeriod(), live.CurrentPeriod())
	}
	if replayed.Status() != live.Status() {
		t.Fatalf("status mismatch: replayed=%s live=%s", replayed.Status(), live.Status())
	}

	// A subsequent live renewal continues to honor the anchor.
	if err := replayed.RenewWithInterval(pricing.Monthly(), newTestMetadata()); err != nil {
		t.Fatalf("post-replay renew failed: %v", err)
	}
	// Sequence: initial end Feb28; renewals -> Mar31, Apr30, May31; +1 -> Jun30.
	if want := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC); !replayed.CurrentPeriod().End().Equal(want) {
		t.Fatalf("post-replay renewal end: expected %v, got %v", want, replayed.CurrentPeriod().End())
	}
}

// rawEvent marshals a domain event into an eventstore.Event envelope at the
// given (current) schema version, mimicking a persisted historical record.
func rawEvent(t *testing.T, streamID string, version, schemaVersion int, e eventstore.DomainEvent) eventstore.Event {
	t.Helper()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return eventstore.Event{
		StreamID:      streamID,
		Type:          e.EventType(),
		Version:       version,
		SchemaVersion: schemaVersion,
		Data:          data,
	}
}

// TestBillingAnchor_LegacyDriftedStream_RehydratesFaithfully_AndHeals verifies
// replay compatibility with a PRE-FIX (drifted) stream: the historical periods
// computed by the old overflow-normalizing AddDate are replayed verbatim (the
// append-only history is never rewritten), while the anchor is reconstructed
// from the original activation day so the NEXT live renewal snaps back to the
// intended anchor.
func TestBillingAnchor_LegacyDriftedStream_RehydratesFaithfully_AndHeals(t *testing.T) {
	streamID := "legacy-drift-001"

	// A pre-fix Jan 31 activation drifted the very first period end to Mar 3
	// (Go's AddDate normalized Feb 31 -> Mar 3), then a renewal to Apr 3.
	jan31 := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	mar3 := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	apr3 := time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC)

	activationPeriod, err := shared.NewDateRange(jan31, mar3)
	if err != nil {
		t.Fatalf("NewDateRange: %v", err)
	}
	renewalPeriod, err := shared.NewDateRange(mar3, apr3)
	if err != nil {
		t.Fatalf("NewDateRange: %v", err)
	}

	created := &ContractCreatedEvent{
		ContractID:     shared.ContractID(streamID),
		AccountID:      shared.AccountID("acc-legacy"),
		IdempotencyKey: "idem-legacy",
		Price:          newTestMoney(),
		BasePrice:      newTestMoney(),
		Interval:       pricing.Monthly(),
		ContractType:   ContractTypeSubscription,
		AutoRenew:      true,
		CreatedAt:      jan31,
	}
	activated := &ContractActivatedEvent{
		ContractID:    shared.ContractID(streamID),
		ActivatedAt:   jan31,
		CurrentPeriod: activationPeriod,
	}
	renewed := &ContractRenewedEvent{
		ContractID:  shared.ContractID(streamID),
		OldPeriod:   activationPeriod,
		NewPeriod:   renewalPeriod,
		OldPriceID:  "",
		NewPriceID:  "",
		OldInterval: pricing.Monthly(),
		NewInterval: pricing.Monthly(),
		RenewedAt:   mar3,
	}

	stream := []eventstore.Event{
		rawEvent(t, streamID, 1, created.CurrentSchemaVersion(), created),
		rawEvent(t, streamID, 2, 1, activated),
		rawEvent(t, streamID, 3, renewed.CurrentSchemaVersion(), renewed),
	}

	agg := NewContractAggregate(shared.ContractID(streamID), clockAt(apr3))
	if err := agg.LoadFromHistory(stream); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	// History is preserved verbatim: the drifted period is not rewritten.
	if !agg.CurrentPeriod().End().Equal(apr3) {
		t.Fatalf("expected replayed period end %v (verbatim), got %v", apr3, agg.CurrentPeriod().End())
	}
	// Anchor reconstructed from the original activation day (31), not the
	// drifted current start day (3).
	if got := agg.BillingAnchorDay(); got != 31 {
		t.Fatalf("expected reconstructed anchor 31, got %d", got)
	}

	// The next live renewal heals the drift back toward the 31st anchor:
	// Apr 3 -> May 31.
	if err := agg.RenewWithInterval(pricing.Monthly(), newTestMetadata()); err != nil {
		t.Fatalf("renew failed: %v", err)
	}
	if want := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC); !agg.CurrentPeriod().End().Equal(want) {
		t.Fatalf("expected healed period end %v, got %v", want, agg.CurrentPeriod().End())
	}
}

// TestBillingAnchor_SnapshotRoundTrip verifies the anchor survives a
// snapshot marshal/load cycle, and that a legacy snapshot without the field
// falls back to the current period's start day.
func TestBillingAnchor_SnapshotRoundTrip(t *testing.T) {
	activatedAt := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	agg := newAnchorAggregate(t, activatedAt)
	if err := agg.RenewWithInterval(pricing.Monthly(), newTestMetadata()); err != nil {
		t.Fatalf("renew failed: %v", err)
	}
	// Now anchor=31, current period end = Mar 31.

	data, err := agg.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot failed: %v", err)
	}

	restored := NewContractAggregate(agg.ContractID(), clockAt(activatedAt))
	if err := restored.LoadFromSnapshot(eventstore.Snapshot{State: data, Version: agg.Version()}); err != nil {
		t.Fatalf("LoadFromSnapshot failed: %v", err)
	}
	if got := restored.BillingAnchorDay(); got != 31 {
		t.Fatalf("expected anchor 31 after snapshot round-trip, got %d", got)
	}

	// Legacy snapshot: strip billing_anchor_day, expect fallback to current
	// period start day (Feb 28 -> 28).
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	delete(m, "billing_anchor_day")
	legacy, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal legacy snapshot: %v", err)
	}
	legacyAgg := NewContractAggregate(agg.ContractID(), clockAt(activatedAt))
	if err := legacyAgg.LoadFromSnapshot(eventstore.Snapshot{State: legacy, Version: agg.Version()}); err != nil {
		t.Fatalf("LoadFromSnapshot (legacy) failed: %v", err)
	}
	wantFallback := agg.CurrentPeriod().Start().Day() // Feb 28 -> 28
	if got := legacyAgg.BillingAnchorDay(); got != wantFallback {
		t.Fatalf("expected legacy fallback anchor %d, got %d", wantFallback, got)
	}
}
