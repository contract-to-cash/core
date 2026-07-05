package contract

import (
	"encoding/json"
	"testing"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// === Tests for Issue #20 (interval-based): Sync Contract interval from Price on Renewal ===

func createActiveAggregateWithInterval(t *testing.T, interval pricing.BillingInterval) *ContractAggregate {
	t.Helper()
	agg := newTestAggregate()
	meta := newTestMetadata()
	cmd := newTestCommand()
	cmd.AutoRenew = true
	cmd.Interval = interval
	if err := agg.Create(cmd, meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	return agg
}

// --- RenewWithInterval ---

func TestRenew_SameInterval_PeriodUnchanged(t *testing.T) {
	// Monthly contract, renew with monthly → period advances 1 month
	agg := createActiveAggregateWithInterval(t, pricing.Monthly())
	meta := newTestMetadata()

	oldPeriod := agg.CurrentPeriod()
	if err := agg.RenewWithInterval(pricing.Monthly(), meta); err != nil {
		t.Fatalf("RenewWithInterval failed: %v", err)
	}

	// New period should start where old ended
	if agg.CurrentPeriod().Start() != oldPeriod.End() {
		t.Errorf("expected new period start %v, got %v", oldPeriod.End(), agg.CurrentPeriod().Start())
	}
	// interval should remain monthly
	if !agg.GetInterval().Equals(pricing.Monthly()) {
		t.Errorf("expected interval monthly, got %s", agg.GetInterval())
	}
}

func TestRenew_IntervalChangeMonthlyToYearly(t *testing.T) {
	// Monthly contract with pending yearly Price → Renew with yearly interval
	agg := createActiveAggregateWithInterval(t, pricing.Monthly())
	meta := newTestMetadata()

	// Schedule a price change to a yearly Price
	yearlyPriceID := shared.PriceID("price-yearly")
	if err := agg.ChangePrice(yearlyPriceID, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}

	oldPeriod := agg.CurrentPeriod()
	// Renew with yearly interval (as resolved from the new Price)
	if err := agg.RenewWithInterval(pricing.Yearly(), meta); err != nil {
		t.Fatalf("RenewWithInterval failed: %v", err)
	}

	// Period should advance by 1 year, not 1 month
	expectedEnd := oldPeriod.End().AddDate(1, 0, 0)
	if agg.CurrentPeriod().End() != expectedEnd {
		t.Errorf("expected period end %v (yearly), got %v", expectedEnd, agg.CurrentPeriod().End())
	}
	// interval should be updated to yearly
	if !agg.GetInterval().Equals(pricing.Yearly()) {
		t.Errorf("expected interval yearly, got %s", agg.GetInterval())
	}
	// Price should be promoted
	if agg.PriceID() != yearlyPriceID {
		t.Errorf("expected priceID %s, got %s", yearlyPriceID, agg.PriceID())
	}
	if agg.PendingPriceID() != nil {
		t.Error("expected pendingPriceID to be nil after renewal")
	}
}

func TestRenew_IntervalChangeYearlyToMonthly(t *testing.T) {
	// Yearly contract with pending monthly Price → Renew with monthly interval
	agg := createActiveAggregateWithInterval(t, pricing.Yearly())
	meta := newTestMetadata()

	monthlyPriceID := shared.PriceID("price-monthly")
	if err := agg.ChangePrice(monthlyPriceID, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}

	oldPeriod := agg.CurrentPeriod()
	if err := agg.RenewWithInterval(pricing.Monthly(), meta); err != nil {
		t.Fatalf("RenewWithInterval failed: %v", err)
	}

	// Period should advance by 1 month, not 1 year
	expectedEnd := oldPeriod.End().AddDate(0, 1, 0)
	if agg.CurrentPeriod().End() != expectedEnd {
		t.Errorf("expected period end %v (monthly), got %v", expectedEnd, agg.CurrentPeriod().End())
	}
	if !agg.GetInterval().Equals(pricing.Monthly()) {
		t.Errorf("expected interval monthly, got %s", agg.GetInterval())
	}
}

// --- Event recording ---

func TestRenew_EventContainsInterval(t *testing.T) {
	agg := createActiveAggregateWithInterval(t, pricing.Monthly())
	meta := newTestMetadata()

	yearlyPriceID := shared.PriceID("price-yearly")
	if err := agg.ChangePrice(yearlyPriceID, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}

	if err := agg.RenewWithInterval(pricing.Yearly(), meta); err != nil {
		t.Fatalf("RenewWithInterval failed: %v", err)
	}

	events := agg.UncommittedEvents()
	// Find the ContractRenewedEvent
	var renewedEvent *ContractRenewedEvent
	for _, e := range events {
		domainEvent, err := contractEventRegistry.Deserialize(e.Type, e.Data)
		if err != nil {
			continue
		}
		if re, ok := domainEvent.(*ContractRenewedEvent); ok {
			renewedEvent = re
		}
	}

	if renewedEvent == nil {
		t.Fatal("expected ContractRenewedEvent in uncommitted events")
	}
	if !renewedEvent.NewInterval.Equals(pricing.Yearly()) {
		t.Errorf("expected NewInterval yearly, got %s", renewedEvent.NewInterval)
	}
	if !renewedEvent.OldInterval.Equals(pricing.Monthly()) {
		t.Errorf("expected OldInterval monthly, got %s", renewedEvent.OldInterval)
	}
}

// --- LoadFromHistory preserves interval ---

func TestLoadFromHistory_WithIntervalChange(t *testing.T) {
	original := newTestAggregate()
	meta := newTestMetadata()
	cmd := newTestCommand()
	cmd.AutoRenew = true
	cmd.Interval = pricing.Monthly()

	if err := original.Create(cmd, meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := original.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	yearlyPriceID := shared.PriceID("price-yearly")
	if err := original.ChangePrice(yearlyPriceID, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}
	if err := original.RenewWithInterval(pricing.Yearly(), meta); err != nil {
		t.Fatalf("RenewWithInterval failed: %v", err)
	}

	events := original.UncommittedEvents()

	// Replay from history
	restored := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := restored.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	if !restored.GetInterval().Equals(pricing.Yearly()) {
		t.Errorf("expected interval yearly after replay, got %s", restored.GetInterval())
	}
	if restored.PriceID() != yearlyPriceID {
		t.Errorf("expected priceID %s after replay, got %s", yearlyPriceID, restored.PriceID())
	}
}

// --- Snapshot round-trip preserves interval ---

func TestSnapshot_PreservesIntervalAfterRenewal(t *testing.T) {
	agg := createActiveAggregateWithInterval(t, pricing.Monthly())
	meta := newTestMetadata()

	yearlyPriceID := shared.PriceID("price-yearly")
	if err := agg.ChangePrice(yearlyPriceID, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}
	if err := agg.RenewWithInterval(pricing.Yearly(), meta); err != nil {
		t.Fatalf("RenewWithInterval failed: %v", err)
	}

	// Marshal snapshot
	data, err := agg.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot failed: %v", err)
	}

	// Verify the snapshot no longer carries the removed billing_cycle field and
	// records the current schema version.
	var snapshotMap map[string]interface{}
	if err := json.Unmarshal(data, &snapshotMap); err != nil {
		t.Fatalf("failed to parse snapshot JSON: %v", err)
	}
	if _, ok := snapshotMap["billing_cycle"]; ok {
		t.Errorf("expected no billing_cycle in snapshot, got %v", snapshotMap["billing_cycle"])
	}
	if v, ok := snapshotMap["schema_version"].(float64); !ok || int(v) != contractSnapshotSchemaVersion {
		t.Errorf("expected schema_version %d in snapshot, got %v", contractSnapshotSchemaVersion, snapshotMap["schema_version"])
	}

	// Round-trip restores the interval.
	restored := newTestAggregate()
	snapshot := eventstore.Snapshot{
		StreamID: string(agg.ContractID()),
		Version:  agg.Version(),
		State:    data,
	}
	if err := restored.LoadFromSnapshot(snapshot); err != nil {
		t.Fatalf("LoadFromSnapshot failed: %v", err)
	}
	if !restored.GetInterval().Equals(pricing.Yearly()) {
		t.Errorf("expected interval yearly after snapshot restore, got %s", restored.GetInterval())
	}
}
