package contract

import (
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

	if err := agg.Renew(agg.GetBillingCycle(), meta); err != nil {
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
	if err := agg.ChangePrice(priceB, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice END_OF_TERM failed: %v", err)
	}

	if !agg.HasPendingChange() {
		t.Error("expected pending change after END_OF_TERM")
	}

	if err := agg.UnscheduleChange("test", meta); err != nil {
		t.Fatalf("UnscheduleChange failed: %v", err)
	}

	if agg.HasPendingChange() {
		t.Error("expected no pending change after unschedule")
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

// --- Event type tests ---

func TestNewEventTypes(t *testing.T) {
	tests := []struct {
		name     string
		event    eventstore.DomainEvent
		expected eventstore.EventType
	}{
		{"PriceChangeScheduledEvent", &PriceChangeScheduledEvent{}, EventTypePriceChangeScheduled},
		{"PriceChangeUnscheduledEvent", &PriceChangeUnscheduledEvent{}, EventTypePriceChangeUnscheduled},
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


// --- Snapshot round-trip tests for new fields ---

func TestSnapshotRoundTrip_WithPendingPriceID(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	priceB := shared.PriceID("price-B")
	if err := agg.ChangePrice(priceB, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice END_OF_TERM failed: %v", err)
	}

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

// --- CreateContractCommand with PriceID ---

func TestCreate_WithPriceID(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()

	cmd := CreateContractCommand{
		AccountID:    shared.AccountID("acc-001"),
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
	if err := agg.ChangePrice(priceB, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice END_OF_TERM failed: %v", err)
	}
	if err := agg.UnscheduleChange("changed mind", meta); err != nil {
		t.Fatalf("UnscheduleChange failed: %v", err)
	}

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

