package contract

import (
	"encoding/json"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

// === TDD Tests for Issue #20: Sync Contract billingCycle from Price on Renewal ===

func createActiveAggregateWithCycle(t *testing.T, cycle BillingCycle) *ContractAggregate {
	t.Helper()
	agg := newTestAggregate()
	meta := newTestMetadata()
	cmd := newTestCommand()
	cmd.AutoRenew = true
	cmd.BillingCycle = cycle
	if err := agg.Create(cmd, meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	return agg
}

// --- Renew with billingCycle parameter ---

func TestRenew_SameBillingCycle_PeriodUnchanged(t *testing.T) {
	// Monthly contract, renew with monthly → period advances 1 month
	agg := createActiveAggregateWithCycle(t, BillingCycleMonthly)
	meta := newTestMetadata()

	oldPeriod := agg.CurrentPeriod()
	if err := agg.Renew(BillingCycleMonthly, meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	// New period should start where old ended
	if agg.CurrentPeriod().Start() != oldPeriod.End() {
		t.Errorf("expected new period start %v, got %v", oldPeriod.End(), agg.CurrentPeriod().Start())
	}
	// billingCycle should remain monthly
	if agg.GetBillingCycle() != BillingCycleMonthly {
		t.Errorf("expected billingCycle monthly, got %s", agg.GetBillingCycle())
	}
}

func TestRenew_CycleChangeMonthlyToYearly(t *testing.T) {
	// Monthly contract with pending yearly Price → Renew with yearly cycle
	agg := createActiveAggregateWithCycle(t, BillingCycleMonthly)
	meta := newTestMetadata()

	// Schedule a price change to a yearly Price
	yearlyPriceID := shared.PriceID("price-yearly")
	if err := agg.ChangePrice(yearlyPriceID, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}

	oldPeriod := agg.CurrentPeriod()
	// Renew with yearly cycle (as resolved from the new Price)
	if err := agg.Renew(BillingCycleYearly, meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	// Period should advance by 1 year, not 1 month
	expectedEnd := oldPeriod.End().AddDate(1, 0, 0)
	if agg.CurrentPeriod().End() != expectedEnd {
		t.Errorf("expected period end %v (yearly), got %v", expectedEnd, agg.CurrentPeriod().End())
	}
	// billingCycle should be updated to yearly
	if agg.GetBillingCycle() != BillingCycleYearly {
		t.Errorf("expected billingCycle yearly, got %s", agg.GetBillingCycle())
	}
	// Price should be promoted
	if agg.PriceID() != yearlyPriceID {
		t.Errorf("expected priceID %s, got %s", yearlyPriceID, agg.PriceID())
	}
	if agg.PendingPriceID() != nil {
		t.Error("expected pendingPriceID to be nil after renewal")
	}
}

func TestRenew_CycleChangeYearlyToMonthly(t *testing.T) {
	// Yearly contract with pending monthly Price → Renew with monthly cycle
	agg := createActiveAggregateWithCycle(t, BillingCycleYearly)
	meta := newTestMetadata()

	monthlyPriceID := shared.PriceID("price-monthly")
	if err := agg.ChangePrice(monthlyPriceID, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}

	oldPeriod := agg.CurrentPeriod()
	if err := agg.Renew(BillingCycleMonthly, meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	// Period should advance by 1 month, not 1 year
	expectedEnd := oldPeriod.End().AddDate(0, 1, 0)
	if agg.CurrentPeriod().End() != expectedEnd {
		t.Errorf("expected period end %v (monthly), got %v", expectedEnd, agg.CurrentPeriod().End())
	}
	if agg.GetBillingCycle() != BillingCycleMonthly {
		t.Errorf("expected billingCycle monthly, got %s", agg.GetBillingCycle())
	}
}

func TestRenew_NoPendingChange_CycleSameAsContract(t *testing.T) {
	// No pending change: billingCycle passed should match contract's current cycle
	agg := createActiveAggregateWithCycle(t, BillingCycleMonthly)
	meta := newTestMetadata()

	oldPeriod := agg.CurrentPeriod()
	if err := agg.Renew(BillingCycleMonthly, meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	expectedEnd := oldPeriod.End().AddDate(0, 1, 0)
	if agg.CurrentPeriod().End() != expectedEnd {
		t.Errorf("expected period end %v, got %v", expectedEnd, agg.CurrentPeriod().End())
	}
	if agg.GetBillingCycle() != BillingCycleMonthly {
		t.Errorf("expected billingCycle monthly, got %s", agg.GetBillingCycle())
	}
}

// --- Event recording ---

func TestRenew_EventContainsBillingCycle(t *testing.T) {
	agg := createActiveAggregateWithCycle(t, BillingCycleMonthly)
	meta := newTestMetadata()

	yearlyPriceID := shared.PriceID("price-yearly")
	if err := agg.ChangePrice(yearlyPriceID, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}

	if err := agg.Renew(BillingCycleYearly, meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
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
	if renewedEvent.NewBillingCycle != BillingCycleYearly {
		t.Errorf("expected NewBillingCycle yearly, got %s", renewedEvent.NewBillingCycle)
	}
	if renewedEvent.OldBillingCycle != BillingCycleMonthly {
		t.Errorf("expected OldBillingCycle monthly, got %s", renewedEvent.OldBillingCycle)
	}
}

// --- LoadFromHistory preserves billingCycle ---

func TestLoadFromHistory_WithBillingCycleChange(t *testing.T) {
	original := newTestAggregate()
	meta := newTestMetadata()
	cmd := newTestCommand()
	cmd.AutoRenew = true
	cmd.BillingCycle = BillingCycleMonthly

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
	if err := original.Renew(BillingCycleYearly, meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	events := original.UncommittedEvents()

	// Replay from history
	restored := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := restored.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	if restored.GetBillingCycle() != BillingCycleYearly {
		t.Errorf("expected billingCycle yearly after replay, got %s", restored.GetBillingCycle())
	}
	if restored.PriceID() != yearlyPriceID {
		t.Errorf("expected priceID %s after replay, got %s", yearlyPriceID, restored.PriceID())
	}
}

// --- Snapshot round-trip preserves billingCycle ---

func TestSnapshot_PreservesBillingCycleAfterRenewal(t *testing.T) {
	agg := createActiveAggregateWithCycle(t, BillingCycleMonthly)
	meta := newTestMetadata()

	yearlyPriceID := shared.PriceID("price-yearly")
	if err := agg.ChangePrice(yearlyPriceID, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}
	if err := agg.Renew(BillingCycleYearly, meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	// Marshal snapshot
	data, err := agg.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot failed: %v", err)
	}

	// Verify billingCycle is in the snapshot JSON
	var snapshotMap map[string]interface{}
	if err := json.Unmarshal(data, &snapshotMap); err != nil {
		t.Fatalf("failed to parse snapshot JSON: %v", err)
	}
	if bc, ok := snapshotMap["billing_cycle"].(string); !ok || bc != string(BillingCycleYearly) {
		t.Errorf("expected billing_cycle yearly in snapshot, got %v", snapshotMap["billing_cycle"])
	}
}

// --- JSON backward compatibility ---

func TestContractRenewedEvent_JSONBackwardCompat(t *testing.T) {
	// Old events without NewBillingCycle/OldBillingCycle should deserialize with zero values
	oldJSON := `{
		"contract_id": "c1",
		"old_period": {"start": "2026-01-01T00:00:00Z", "end": "2026-02-01T00:00:00Z"},
		"new_period": {"start": "2026-02-01T00:00:00Z", "end": "2026-03-01T00:00:00Z"},
		"old_price_id": "price-A",
		"new_price_id": "price-A",
		"price_changed": false,
		"renewed_at": "2026-02-01T00:00:00Z"
	}`

	var event ContractRenewedEvent
	if err := json.Unmarshal([]byte(oldJSON), &event); err != nil {
		t.Fatalf("failed to unmarshal old event format: %v", err)
	}

	// Old events should have empty billing cycle (zero value)
	if event.NewBillingCycle != "" {
		t.Errorf("expected empty NewBillingCycle for old event, got %s", event.NewBillingCycle)
	}
}
