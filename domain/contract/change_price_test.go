package contract

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// === TDD Tests for Issue #9: ChangePrice with ChangePolicy ===

// --- Behavior Matrix Tests ---

func TestChangePrice_Immediate_NoPending(t *testing.T) {
	// priceID=A, pending=nil → ChangePrice(B, IMMEDIATE) → priceID=B, pending=nil
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	newPriceID := shared.PriceID("price-B")
	err := agg.ChangePrice(newPriceID, ChangePolicyImmediate, nil, meta)
	if err != nil {
		t.Fatalf("ChangePrice IMMEDIATE failed: %v", err)
	}

	if agg.PriceID() != newPriceID {
		t.Errorf("expected priceID %s, got %s", newPriceID, agg.PriceID())
	}
	if agg.PendingPriceID() != nil {
		t.Error("expected pendingPriceID to be nil")
	}
}

func TestChangePrice_EndOfTerm_NoPending(t *testing.T) {
	// priceID=A, pending=nil → ChangePrice(B, END_OF_TERM) → priceID=A, pending=B
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	oldPriceID := agg.PriceID()
	newPriceID := shared.PriceID("price-B")
	err := agg.ChangePrice(newPriceID, ChangePolicyEndOfTerm, nil, meta)
	if err != nil {
		t.Fatalf("ChangePrice END_OF_TERM failed: %v", err)
	}

	if agg.PriceID() != oldPriceID {
		t.Errorf("expected priceID unchanged %s, got %s", oldPriceID, agg.PriceID())
	}
	if agg.PendingPriceID() == nil {
		t.Fatal("expected pendingPriceID to be set")
	}
	if *agg.PendingPriceID() != newPriceID {
		t.Errorf("expected pendingPriceID %s, got %s", newPriceID, *agg.PendingPriceID())
	}
}

func TestChangePrice_Immediate_ClearsPending(t *testing.T) {
	// priceID=A, pending=B → ChangePrice(C, IMMEDIATE) → priceID=C, pending=nil
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	// First schedule a pending change
	priceB := shared.PriceID("price-B")
	if err := agg.ChangePrice(priceB, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("schedule pending failed: %v", err)
	}
	if agg.PendingPriceID() == nil {
		t.Fatal("precondition: pendingPriceID should be set")
	}

	// Now apply IMMEDIATE change — should clear pending
	priceC := shared.PriceID("price-C")
	if err := agg.ChangePrice(priceC, ChangePolicyImmediate, nil, meta); err != nil {
		t.Fatalf("ChangePrice IMMEDIATE failed: %v", err)
	}

	if agg.PriceID() != priceC {
		t.Errorf("expected priceID %s, got %s", priceC, agg.PriceID())
	}
	if agg.PendingPriceID() != nil {
		t.Error("expected pendingPriceID to be nil after IMMEDIATE change")
	}
}

func TestChangePrice_EndOfTerm_OverwritesPending(t *testing.T) {
	// priceID=A, pending=B → ChangePrice(C, END_OF_TERM) → priceID=A, pending=C
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	oldPriceID := agg.PriceID()

	// Schedule B
	priceB := shared.PriceID("price-B")
	if err := agg.ChangePrice(priceB, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("schedule B failed: %v", err)
	}

	// Schedule C (overwrites B)
	priceC := shared.PriceID("price-C")
	if err := agg.ChangePrice(priceC, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("schedule C failed: %v", err)
	}

	if agg.PriceID() != oldPriceID {
		t.Errorf("expected priceID unchanged %s, got %s", oldPriceID, agg.PriceID())
	}
	if agg.PendingPriceID() == nil || *agg.PendingPriceID() != priceC {
		t.Errorf("expected pendingPriceID to be %s, got %v", priceC, agg.PendingPriceID())
	}
}

func TestUnscheduleChange_ClearsPending(t *testing.T) {
	// priceID=A, pending=B → UnscheduleChange() → priceID=A, pending=nil
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	oldPriceID := agg.PriceID()

	priceB := shared.PriceID("price-B")
	if err := agg.ChangePrice(priceB, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("schedule failed: %v", err)
	}

	if err := agg.UnscheduleChange("changed mind", meta); err != nil {
		t.Fatalf("UnscheduleChange failed: %v", err)
	}

	if agg.PriceID() != oldPriceID {
		t.Errorf("expected priceID unchanged %s, got %s", oldPriceID, agg.PriceID())
	}
	if agg.PendingPriceID() != nil {
		t.Error("expected pendingPriceID to be nil")
	}
}

func TestRenew_AppliesPending(t *testing.T) {
	// priceID=A, pending=B → Renew() → priceID=B, pending=nil
	agg := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	priceB := shared.PriceID("price-B")
	if err := agg.ChangePrice(priceB, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("schedule failed: %v", err)
	}

	if err := agg.Renew(meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	if agg.PriceID() != priceB {
		t.Errorf("expected priceID %s after renewal, got %s", priceB, agg.PriceID())
	}
	if agg.PendingPriceID() != nil {
		t.Error("expected pendingPriceID to be nil after renewal")
	}
}

// --- ChangePrice validation tests ---

func TestChangePrice_NotActive_Fails(t *testing.T) {
	meta := newTestMetadata()
	agg := newTestAggregate()
	_ = agg.Create(newTestCommand(), meta)

	err := agg.ChangePrice(shared.PriceID("price-X"), ChangePolicyImmediate, nil, meta)
	if err == nil {
		t.Fatal("expected error for ChangePrice from draft state")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("expected DomainError, got %T", err)
	}
	if domErr.Code != shared.ErrCodeInvalidStateTransition {
		t.Errorf("expected error code %s, got %s", shared.ErrCodeInvalidStateTransition, domErr.Code)
	}
}

func TestChangePrice_UnknownPolicy_Fails(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	err := agg.ChangePrice(shared.PriceID("price-X"), ChangePolicy("unknown"), nil, meta)
	if err == nil {
		t.Fatal("expected error for unknown change policy")
	}
}

func TestChangePrice_Immediate_WithProration(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	proration := &PlanChangeProration{
		CreditAmount:     newTestMoney(),
		ChargeAmount:     shared.NewMoney(new(big.Rat).SetInt64(2000), shared.CurrencyJPY),
		AdjustmentAmount: shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY),
		EffectiveDate:    agg.Clock().Now(),
	}

	newPriceID := shared.PriceID("price-upgraded")
	err := agg.ChangePrice(newPriceID, ChangePolicyImmediate, proration, meta)
	if err != nil {
		t.Fatalf("ChangePrice IMMEDIATE with proration failed: %v", err)
	}

	if agg.PriceID() != newPriceID {
		t.Errorf("expected priceID %s, got %s", newPriceID, agg.PriceID())
	}

	// Verify event contains proration data
	events := agg.UncommittedEvents()
	lastEvent := events[len(events)-1]
	domainEvent, err := contractEventRegistry.Deserialize(lastEvent.Type, lastEvent.Data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}
	pce, ok := domainEvent.(*PriceChangedEvent)
	if !ok {
		t.Fatalf("expected *PriceChangedEvent, got %T", domainEvent)
	}
	if pce.Proration == nil {
		t.Error("expected proration in event")
	}
	if pce.Policy != ChangePolicyImmediate {
		t.Errorf("expected policy %s, got %s", ChangePolicyImmediate, pce.Policy)
	}
}

// --- UnscheduleChange tests ---

func TestUnscheduleChange_NoPending_Fails(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	err := agg.UnscheduleChange("no reason", meta)
	if err == nil {
		t.Fatal("expected error when no pending change to cancel")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("expected DomainError, got %T", err)
	}
	if domErr.Code != shared.ErrCodeBusinessRule {
		t.Errorf("expected error code %s, got %s", shared.ErrCodeBusinessRule, domErr.Code)
	}
}

// --- HasPendingChange getter tests ---

func TestHasPendingChange(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	if agg.HasPendingChange() {
		t.Error("expected no pending change initially")
	}

	priceB := shared.PriceID("price-B")
	_ = agg.ChangePrice(priceB, ChangePolicyEndOfTerm, nil, meta)

	if !agg.HasPendingChange() {
		t.Error("expected pending change after END_OF_TERM")
	}

	_ = agg.UnscheduleChange("test", meta)

	if agg.HasPendingChange() {
		t.Error("expected no pending change after unschedule")
	}
}

// --- PriceOverride tests ---

func TestSetPriceOverride(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	override := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	err := agg.SetPriceOverride(override, meta)
	if err != nil {
		t.Fatalf("SetPriceOverride failed: %v", err)
	}

	if agg.PriceOverride() == nil {
		t.Fatal("expected priceOverride to be set")
	}
	if agg.PriceOverride().Amount().Cmp(new(big.Rat).SetInt64(500)) != 0 {
		t.Errorf("expected override amount 500, got %s", agg.PriceOverride().Amount().RatString())
	}
}

func TestSetPriceOverride_NotActive_Fails(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()
	_ = agg.Create(newTestCommand(), meta)

	override := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	err := agg.SetPriceOverride(override, meta)
	if err == nil {
		t.Fatal("expected error for SetPriceOverride from draft state")
	}
}

func TestClearPriceOverride(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	override := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	_ = agg.SetPriceOverride(override, meta)

	err := agg.ClearPriceOverride(meta)
	if err != nil {
		t.Fatalf("ClearPriceOverride failed: %v", err)
	}

	if agg.PriceOverride() != nil {
		t.Error("expected priceOverride to be nil after clear")
	}
}

func TestClearPriceOverride_NoneSet_Fails(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	err := agg.ClearPriceOverride(meta)
	if err == nil {
		t.Fatal("expected error when no price override to clear")
	}
}

// --- Apply handler tests for new events ---

func TestApply_PriceChangedEvent_NewFormat(t *testing.T) {
	agg := newTestAggregate()
	now := agg.Clock().Now()

	// Setup to active state
	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		BillingCycle: BillingCycleMonthly,
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	// Set a pending price to verify IMMEDIATE clears it
	pendingID := shared.PriceID("pending-old")
	agg.pendingPriceID = &pendingID

	err := agg.Apply(&PriceChangedEvent{
		ContractID: shared.ContractID("test-contract-001"),
		OldPriceID: shared.PriceID(""),
		NewPriceID: shared.PriceID("price-new"),
		Policy:     ChangePolicyImmediate,
		ChangedAt:  now,
	})
	if err != nil {
		t.Fatalf("Apply PriceChangedEvent failed: %v", err)
	}

	if agg.PriceID() != shared.PriceID("price-new") {
		t.Errorf("expected priceID price-new, got %s", agg.PriceID())
	}
	if agg.PendingPriceID() != nil {
		t.Error("expected pendingPriceID to be nil after IMMEDIATE price change")
	}
}

func TestApply_PriceChangeScheduledEvent(t *testing.T) {
	agg := newTestAggregate()
	now := agg.Clock().Now()

	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		BillingCycle: BillingCycleMonthly,
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	err := agg.Apply(&PriceChangeScheduledEvent{
		ContractID:     shared.ContractID("test-contract-001"),
		CurrentPriceID: shared.PriceID(""),
		NewPriceID:     shared.PriceID("price-scheduled"),
		Policy:         ChangePolicyEndOfTerm,
		ScheduledAt:    now,
	})
	if err != nil {
		t.Fatalf("Apply PriceChangeScheduledEvent failed: %v", err)
	}

	if agg.PendingPriceID() == nil || *agg.PendingPriceID() != shared.PriceID("price-scheduled") {
		t.Errorf("expected pendingPriceID=price-scheduled, got %v", agg.PendingPriceID())
	}
}

func TestApply_PriceChangeUnscheduledEvent(t *testing.T) {
	agg := newTestAggregate()
	now := agg.Clock().Now()

	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		BillingCycle: BillingCycleMonthly,
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	// Set pending
	pendingID := shared.PriceID("pending-to-cancel")
	agg.pendingPriceID = &pendingID

	err := agg.Apply(&PriceChangeUnscheduledEvent{
		ContractID:       shared.ContractID("test-contract-001"),
		CancelledPriceID: pendingID,
		Reason:           "changed mind",
		UnscheduledAt:    now,
	})
	if err != nil {
		t.Fatalf("Apply PriceChangeUnscheduledEvent failed: %v", err)
	}

	if agg.PendingPriceID() != nil {
		t.Error("expected pendingPriceID to be nil after unschedule")
	}
}

func TestApply_PriceOverrideSetEvent(t *testing.T) {
	agg := newTestAggregate()
	now := agg.Clock().Now()

	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		BillingCycle: BillingCycleMonthly,
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	override := shared.NewMoney(new(big.Rat).SetInt64(800), shared.CurrencyJPY)
	err := agg.Apply(&PriceOverrideSetEvent{
		ContractID: shared.ContractID("test-contract-001"),
		Override:   override,
		SetAt:      now,
	})
	if err != nil {
		t.Fatalf("Apply PriceOverrideSetEvent failed: %v", err)
	}

	if agg.PriceOverride() == nil {
		t.Fatal("expected priceOverride to be set")
	}
}

func TestApply_PriceOverrideClearedEvent(t *testing.T) {
	agg := newTestAggregate()
	now := agg.Clock().Now()

	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		BillingCycle: BillingCycleMonthly,
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	// Set override first
	override := shared.NewMoney(new(big.Rat).SetInt64(800), shared.CurrencyJPY)
	_ = agg.Apply(&PriceOverrideSetEvent{
		ContractID: shared.ContractID("test-contract-001"),
		Override:   override,
		SetAt:      now,
	})

	err := agg.Apply(&PriceOverrideClearedEvent{
		ContractID: shared.ContractID("test-contract-001"),
		ClearedAt:  now,
	})
	if err != nil {
		t.Fatalf("Apply PriceOverrideClearedEvent failed: %v", err)
	}

	if agg.PriceOverride() != nil {
		t.Error("expected priceOverride to be nil after clear")
	}
}

// --- Event type tests ---

func TestNewEventTypes(t *testing.T) {
	tests := []struct {
		name     string
		event    eventstore.DomainEvent
		expected eventstore.EventType
	}{
		{"PriceChangeScheduledEvent", &PriceChangeScheduledEvent{}, EventTypePriceChangeScheduled},
		{"PriceChangeUnscheduledEvent", &PriceChangeUnscheduledEvent{}, EventTypePriceChangeUnscheduled},
		{"PriceOverrideSetEvent", &PriceOverrideSetEvent{}, EventTypePriceOverrideSet},
		{"PriceOverrideClearedEvent", &PriceOverrideClearedEvent{}, EventTypePriceOverrideCleared},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.EventType(); got != tt.expected {
				t.Errorf("EventType() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// --- Backward compatibility tests ---

func TestApply_LegacyPriceChangedEvent(t *testing.T) {
	// Legacy PriceChangedEvent (with OldPrice/NewPrice Money fields) should still work
	agg := newTestAggregate()
	now := agg.Clock().Now()

	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		BillingCycle: BillingCycleMonthly,
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	// Old-format PriceChangedEvent with Money fields still in struct
	newPrice := shared.NewMoney(new(big.Rat).SetInt64(2000), shared.CurrencyJPY)
	err := agg.Apply(&PriceChangedEvent{
		ContractID: shared.ContractID("test-contract-001"),
		OldPrice:   newTestMoney(),
		NewPrice:   newPrice,
		ChangedAt:  now,
	})
	if err != nil {
		t.Fatalf("Apply legacy PriceChangedEvent failed: %v", err)
	}
}

func TestApply_LegacyPlanChangedEvent(t *testing.T) {
	// Legacy PlanChangedEvent should still apply
	agg := newTestAggregate()
	now := agg.Clock().Now()

	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		BillingCycle: BillingCycleMonthly,
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	err := agg.Apply(&PlanChangedEvent{
		ContractID: shared.ContractID("test-contract-001"),
		OldPlanID:  shared.PlanID("plan-001"),
		NewPlanID:  shared.PlanID("plan-002"),
		ChangedAt:  now,
	})
	if err != nil {
		t.Fatalf("Apply legacy PlanChangedEvent failed: %v", err)
	}
	if agg.PlanID() != shared.PlanID("plan-002") {
		t.Errorf("expected plan-002, got %s", agg.PlanID())
	}
}

// --- Snapshot round-trip tests for new fields ---

func TestSnapshotRoundTrip_WithPriceOverride(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	override := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	_ = agg.SetPriceOverride(override, meta)

	data, err := agg.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot failed: %v", err)
	}

	restored := NewContractAggregate(agg.ContractID(), newTestClock())
	snapshot := eventstore.Snapshot{
		StreamID: string(agg.ContractID()),
		Version:  agg.Version(),
		State:    data,
	}
	if err := restored.LoadFromSnapshot(snapshot); err != nil {
		t.Fatalf("LoadFromSnapshot failed: %v", err)
	}

	if restored.PriceOverride() == nil {
		t.Fatal("expected priceOverride to be restored from snapshot")
	}
	if restored.PriceOverride().Amount().Cmp(new(big.Rat).SetInt64(500)) != 0 {
		t.Errorf("expected override 500, got %s", restored.PriceOverride().Amount().RatString())
	}
}

func TestSnapshotRoundTrip_WithPendingPriceID(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	priceB := shared.PriceID("price-B")
	_ = agg.ChangePrice(priceB, ChangePolicyEndOfTerm, nil, meta)

	data, err := agg.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot failed: %v", err)
	}

	restored := NewContractAggregate(agg.ContractID(), newTestClock())
	snapshot := eventstore.Snapshot{
		StreamID: string(agg.ContractID()),
		Version:  agg.Version(),
		State:    data,
	}
	if err := restored.LoadFromSnapshot(snapshot); err != nil {
		t.Fatalf("LoadFromSnapshot failed: %v", err)
	}

	if restored.PendingPriceID() == nil || *restored.PendingPriceID() != priceB {
		t.Errorf("expected pendingPriceID %s, got %v", priceB, restored.PendingPriceID())
	}
}

// --- LoadFromHistory with new events ---

func TestLoadFromHistory_WithPriceChangeScheduled(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	priceB := shared.PriceID("price-B")
	if err := agg.ChangePrice(priceB, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}

	events := agg.UncommittedEvents()

	restored := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := restored.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	if restored.PendingPriceID() == nil || *restored.PendingPriceID() != priceB {
		t.Errorf("expected pendingPriceID %s, got %v", priceB, restored.PendingPriceID())
	}
}

func TestLoadFromHistory_WithPriceOverride(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	override := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	if err := agg.SetPriceOverride(override, meta); err != nil {
		t.Fatalf("SetPriceOverride failed: %v", err)
	}

	events := agg.UncommittedEvents()

	restored := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := restored.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	if restored.PriceOverride() == nil {
		t.Fatal("expected priceOverride after history replay")
	}
}

// --- CreateContractCommand with PriceID ---

func TestCreate_WithPriceID(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()

	cmd := CreateContractCommand{
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		PriceID:      shared.PriceID("price-001"),
		ContractType: ContractTypeSubscription,
		BillingCycle: BillingCycleMonthly,
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		AutoRenew:    true,
	}

	if err := agg.Create(cmd, meta); err != nil {
		t.Fatalf("Create with PriceID failed: %v", err)
	}

	if agg.PriceID() != shared.PriceID("price-001") {
		t.Errorf("expected priceID price-001, got %s", agg.PriceID())
	}
}

// --- PriceChangedEvent updated format ---

func TestPriceChangedEvent_HasPolicyAndPriceIDs(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	newPriceID := shared.PriceID("price-new")
	if err := agg.ChangePrice(newPriceID, ChangePolicyImmediate, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}

	events := agg.UncommittedEvents()
	lastEvent := events[len(events)-1]
	if lastEvent.Type != EventTypePriceChanged {
		t.Fatalf("expected event type %s, got %s", EventTypePriceChanged, lastEvent.Type)
	}

	domainEvent, err := contractEventRegistry.Deserialize(lastEvent.Type, lastEvent.Data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}

	pce, ok := domainEvent.(*PriceChangedEvent)
	if !ok {
		t.Fatalf("expected *PriceChangedEvent, got %T", domainEvent)
	}

	if pce.NewPriceID != newPriceID {
		t.Errorf("expected NewPriceID=%s, got %s", newPriceID, pce.NewPriceID)
	}
	if pce.Policy != ChangePolicyImmediate {
		t.Errorf("expected Policy=immediate, got %s", pce.Policy)
	}
}

func TestPriceChangeScheduledEvent_Serialization(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	newPriceID := shared.PriceID("price-scheduled")
	if err := agg.ChangePrice(newPriceID, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice END_OF_TERM failed: %v", err)
	}

	events := agg.UncommittedEvents()
	lastEvent := events[len(events)-1]
	if lastEvent.Type != EventTypePriceChangeScheduled {
		t.Fatalf("expected event type %s, got %s", EventTypePriceChangeScheduled, lastEvent.Type)
	}

	// Verify JSON round-trip
	domainEvent, err := contractEventRegistry.Deserialize(lastEvent.Type, lastEvent.Data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}

	pcs, ok := domainEvent.(*PriceChangeScheduledEvent)
	if !ok {
		t.Fatalf("expected *PriceChangeScheduledEvent, got %T", domainEvent)
	}
	if pcs.NewPriceID != newPriceID {
		t.Errorf("expected NewPriceID=%s, got %s", newPriceID, pcs.NewPriceID)
	}
}

func TestPriceChangeUnscheduledEvent_Serialization(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	priceB := shared.PriceID("price-B")
	_ = agg.ChangePrice(priceB, ChangePolicyEndOfTerm, nil, meta)
	_ = agg.UnscheduleChange("changed mind", meta)

	events := agg.UncommittedEvents()
	lastEvent := events[len(events)-1]
	if lastEvent.Type != EventTypePriceChangeUnscheduled {
		t.Fatalf("expected event type %s, got %s", EventTypePriceChangeUnscheduled, lastEvent.Type)
	}

	domainEvent, err := contractEventRegistry.Deserialize(lastEvent.Type, lastEvent.Data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}

	pcu, ok := domainEvent.(*PriceChangeUnscheduledEvent)
	if !ok {
		t.Fatalf("expected *PriceChangeUnscheduledEvent, got %T", domainEvent)
	}
	if pcu.CancelledPriceID != priceB {
		t.Errorf("expected CancelledPriceID=%s, got %s", priceB, pcu.CancelledPriceID)
	}
	if pcu.Reason != "changed mind" {
		t.Errorf("expected reason 'changed mind', got %s", pcu.Reason)
	}
}

func TestPriceOverrideEvents_Serialization(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	override := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	_ = agg.SetPriceOverride(override, meta)
	_ = agg.ClearPriceOverride(meta)

	events := agg.UncommittedEvents()

	// Check SetEvent
	setEvent := events[len(events)-2]
	if setEvent.Type != EventTypePriceOverrideSet {
		t.Fatalf("expected event type %s, got %s", EventTypePriceOverrideSet, setEvent.Type)
	}
	setDomain, err := contractEventRegistry.Deserialize(setEvent.Type, setEvent.Data)
	if err != nil {
		t.Fatalf("deserialize set event failed: %v", err)
	}
	if _, ok := setDomain.(*PriceOverrideSetEvent); !ok {
		t.Fatalf("expected *PriceOverrideSetEvent, got %T", setDomain)
	}

	// Check ClearEvent
	clearEvent := events[len(events)-1]
	if clearEvent.Type != EventTypePriceOverrideCleared {
		t.Fatalf("expected event type %s, got %s", EventTypePriceOverrideCleared, clearEvent.Type)
	}
	clearDomain, err := contractEventRegistry.Deserialize(clearEvent.Type, clearEvent.Data)
	if err != nil {
		t.Fatalf("deserialize clear event failed: %v", err)
	}
	if _, ok := clearDomain.(*PriceOverrideClearedEvent); !ok {
		t.Fatalf("expected *PriceOverrideClearedEvent, got %T", clearDomain)
	}
}

// --- Legacy backward compatibility: LoadFromHistory with old PlanChangedEvent ---

func TestLoadFromHistory_LegacyPlanChangedEvent(t *testing.T) {
	// Simulate loading historical events that include the old PlanChangedEvent
	// This should still work for backward compat
	now := newTestClock().Now()

	createData, _ := json.Marshal(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		BillingCycle: BillingCycleMonthly,
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	activateData, _ := json.Marshal(&ContractActivatedEvent{
		ContractID:  shared.ContractID("test-contract-001"),
		ActivatedAt: now,
	})

	planChangeData, _ := json.Marshal(&PlanChangedEvent{
		ContractID: shared.ContractID("test-contract-001"),
		OldPlanID:  shared.PlanID("plan-001"),
		NewPlanID:  shared.PlanID("plan-002"),
		ChangedAt:  now,
	})

	events := []eventstore.Event{
		{Type: EventTypeContractCreated, Data: createData},
		{Type: EventTypeContractActivated, Data: activateData},
		{Type: EventTypePlanChanged, Data: planChangeData},
	}

	agg := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := agg.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory with legacy PlanChangedEvent failed: %v", err)
	}

	if agg.PlanID() != shared.PlanID("plan-002") {
		t.Errorf("expected plan-002, got %s", agg.PlanID())
	}
}
