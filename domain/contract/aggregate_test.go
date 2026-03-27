package contract

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

func newTestClock() shared.Clock {
	return shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func newTestAggregate() *ContractAggregate {
	id := shared.ContractID("test-contract-001")
	return NewContractAggregate(id, newTestClock())
}

func newTestMoney() shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
}

func newTestMetadata() eventstore.EventMetadata {
	return eventstore.EventMetadata{UserID: "user-001"}
}

func newTestCommand() CreateContractCommand {
	return CreateContractCommand{
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		ContractType: ContractTypeSubscription,
		BillingCycle: BillingCycleMonthly,
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
	}
}

func createActiveAggregate(t *testing.T) *ContractAggregate {
	t.Helper()
	agg := newTestAggregate()
	meta := newTestMetadata()
	if err := agg.Create(newTestCommand(), meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	return agg
}

func TestContractLifecycle_Create_Activate_Suspend_Resume_Cancel(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()

	// Create
	if err := agg.Create(newTestCommand(), meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if agg.Status() != ContractStatusDraft {
		t.Errorf("expected status draft, got %s", agg.Status())
	}
	if agg.AccountID() != shared.AccountID("acc-001") {
		t.Errorf("expected account acc-001, got %s", agg.AccountID())
	}
	if agg.PlanID() != shared.PlanID("plan-001") {
		t.Errorf("expected plan plan-001, got %s", agg.PlanID())
	}

	// Activate
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected status active, got %s", agg.Status())
	}

	// Suspend
	suspConfig := SuspensionConfiguration{
		BillingBehavior: SuspensionBillingSkip,
		Reason:          "non-payment",
	}
	if err := agg.Suspend(suspConfig, meta); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}
	if agg.Status() != ContractStatusSuspended {
		t.Errorf("expected status suspended, got %s", agg.Status())
	}
	if agg.SuspensionConfig() == nil {
		t.Error("expected suspension config to be set")
	}
	if agg.SuspensionConfig().Reason != "non-payment" {
		t.Errorf("expected reason 'non-payment', got %s", agg.SuspensionConfig().Reason)
	}

	// Resume
	if err := agg.Resume(meta); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected status active, got %s", agg.Status())
	}
	if agg.SuspensionConfig() != nil {
		t.Error("expected suspension config to be nil after resume")
	}

	// Cancel
	if err := agg.Cancel("customer request", meta); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if agg.Status() != ContractStatusCancelled {
		t.Errorf("expected status cancelled, got %s", agg.Status())
	}

	// Verify uncommitted events count: Create + Activate + Suspend + Resume + Cancel = 5
	events := agg.UncommittedEvents()
	if len(events) != 5 {
		t.Errorf("expected 5 uncommitted events, got %d", len(events))
	}
}

func TestInvalidStateTransitions(t *testing.T) {
	meta := newTestMetadata()

	tests := []struct {
		name   string
		setup  func() *ContractAggregate
		action func(agg *ContractAggregate) error
	}{
		{
			name: "create on already created",
			setup: func() *ContractAggregate {
				agg := newTestAggregate()
				_ = agg.Create(newTestCommand(), meta)
				return agg
			},
			action: func(agg *ContractAggregate) error {
				return agg.Create(newTestCommand(), meta)
			},
		},
		{
			name: "activate from cancelled",
			setup: func() *ContractAggregate {
				agg := createActiveAggregate(t)
				_ = agg.Cancel("test", meta)
				return agg
			},
			action: func(agg *ContractAggregate) error {
				return agg.Activate(meta)
			},
		},
		{
			name: "suspend from draft",
			setup: func() *ContractAggregate {
				agg := newTestAggregate()
				_ = agg.Create(newTestCommand(), meta)
				return agg
			},
			action: func(agg *ContractAggregate) error {
				return agg.Suspend(SuspensionConfiguration{}, meta)
			},
		},
		{
			name: "resume from active",
			setup: func() *ContractAggregate {
				return createActiveAggregate(t)
			},
			action: func(agg *ContractAggregate) error {
				return agg.Resume(meta)
			},
		},
		// draft → cancelled is allowed per design (design-decisions.md)
		{
			name: "start trial from active",
			setup: func() *ContractAggregate {
				return createActiveAggregate(t)
			},
			action: func(agg *ContractAggregate) error {
				return agg.StartTrial(TrialConfiguration{}, meta)
			},
		},
		{
			name: "end trial from active",
			setup: func() *ContractAggregate {
				return createActiveAggregate(t)
			},
			action: func(agg *ContractAggregate) error {
				return agg.EndTrial(true, meta)
			},
		},
		{
			name: "change price from draft",
			setup: func() *ContractAggregate {
				agg := newTestAggregate()
				_ = agg.Create(newTestCommand(), meta)
				return agg
			},
			action: func(agg *ContractAggregate) error {
				return agg.ChangePrice(newTestMoney(), time.Now(), meta)
			},
		},
		{
			name: "change plan from draft",
			setup: func() *ContractAggregate {
				agg := newTestAggregate()
				_ = agg.Create(newTestCommand(), meta)
				return agg
			},
			action: func(agg *ContractAggregate) error {
				return agg.ChangePlan(shared.PlanID("plan-002"), nil, meta)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := tt.setup()
			err := tt.action(agg)
			if err == nil {
				t.Fatal("expected error for invalid state transition, got nil")
			}
			var domErr *shared.DomainError
			if !errors.As(err, &domErr) {
				t.Fatalf("expected DomainError, got %T: %v", err, err)
			}
			if domErr.Code != shared.ErrCodeInvalidStateTransition {
				t.Errorf("expected error code %s, got %s", shared.ErrCodeInvalidStateTransition, domErr.Code)
			}
		})
	}
}

func TestApplyAllEvents(t *testing.T) {
	agg := newTestAggregate()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// ContractCreatedEvent
	err := agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		BillingCycle: BillingCycleMonthly,
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})
	if err != nil {
		t.Fatalf("Apply ContractCreatedEvent failed: %v", err)
	}
	if agg.Status() != ContractStatusDraft {
		t.Errorf("expected draft, got %s", agg.Status())
	}

	// ContractActivatedEvent
	err = agg.Apply(&ContractActivatedEvent{
		ContractID:  shared.ContractID("test-contract-001"),
		ActivatedAt: now,
	})
	if err != nil {
		t.Fatalf("Apply ContractActivatedEvent failed: %v", err)
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected active, got %s", agg.Status())
	}

	// PriceChangedEvent
	newPrice := shared.NewMoney(new(big.Rat).SetInt64(2000), shared.CurrencyJPY)
	err = agg.Apply(&PriceChangedEvent{
		ContractID:  shared.ContractID("test-contract-001"),
		OldPrice:    newTestMoney(),
		NewPrice:    newPrice,
		ChangedAt:   now,
		EffectiveAt: now,
	})
	if err != nil {
		t.Fatalf("Apply PriceChangedEvent failed: %v", err)
	}
	if agg.Price().Amount().Cmp(new(big.Rat).SetInt64(2000)) != 0 {
		t.Error("price was not updated")
	}

	// PlanChangedEvent
	err = agg.Apply(&PlanChangedEvent{
		ContractID: shared.ContractID("test-contract-001"),
		OldPlanID:  shared.PlanID("plan-001"),
		NewPlanID:  shared.PlanID("plan-002"),
		ChangedAt:  now,
	})
	if err != nil {
		t.Fatalf("Apply PlanChangedEvent failed: %v", err)
	}
	if agg.PlanID() != shared.PlanID("plan-002") {
		t.Errorf("expected plan-002, got %s", agg.PlanID())
	}

	// ContractSuspendedEvent
	err = agg.Apply(&ContractSuspendedEvent{
		ContractID:      shared.ContractID("test-contract-001"),
		SuspendedAt:     now,
		BillingBehavior: SuspensionBillingSkip,
		Reason:          "test",
	})
	if err != nil {
		t.Fatalf("Apply ContractSuspendedEvent failed: %v", err)
	}
	if agg.Status() != ContractStatusSuspended {
		t.Errorf("expected suspended, got %s", agg.Status())
	}

	// ContractResumedEvent
	err = agg.Apply(&ContractResumedEvent{
		ContractID: shared.ContractID("test-contract-001"),
		ResumedAt:  now,
	})
	if err != nil {
		t.Fatalf("Apply ContractResumedEvent failed: %v", err)
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected active, got %s", agg.Status())
	}

	// ContractCancelledEvent
	err = agg.Apply(&ContractCancelledEvent{
		ContractID:  shared.ContractID("test-contract-001"),
		CancelledAt: now,
		Reason:      "done",
	})
	if err != nil {
		t.Fatalf("Apply ContractCancelledEvent failed: %v", err)
	}
	if agg.Status() != ContractStatusCancelled {
		t.Errorf("expected cancelled, got %s", agg.Status())
	}
}

func TestApplyTrialEvents(t *testing.T) {
	agg := newTestAggregate()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create
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

	// TrialStartedEvent
	trialEnd := now.AddDate(0, 0, 14)
	err := agg.Apply(&TrialStartedEvent{
		ContractID: shared.ContractID("test-contract-001"),
		TrialConfig: TrialConfiguration{
			TrialEndDate:           trialEnd,
			AutoConvert:            true,
			RequirePaymentMethod:   false,
			ConversionReminderDays: []int{3},
		},
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("Apply TrialStartedEvent failed: %v", err)
	}
	if agg.Status() != ContractStatusTrialing {
		t.Errorf("expected trialing, got %s", agg.Status())
	}
	if agg.TrialConfig() == nil {
		t.Fatal("expected trial config to be set")
	}

	// TrialEndedEvent (converted)
	err = agg.Apply(&TrialEndedEvent{
		ContractID: shared.ContractID("test-contract-001"),
		EndedAt:    trialEnd,
		Converted:  true,
	})
	if err != nil {
		t.Fatalf("Apply TrialEndedEvent failed: %v", err)
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected active after conversion, got %s", agg.Status())
	}
	if agg.TrialConfig() != nil {
		t.Error("expected trial config to be nil after trial end")
	}
}

func TestApplyTrialEndedNotConverted(t *testing.T) {
	agg := newTestAggregate()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

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
	_ = agg.Apply(&TrialStartedEvent{
		ContractID:  shared.ContractID("test-contract-001"),
		TrialConfig: TrialConfiguration{TrialEndDate: now.AddDate(0, 0, 14)},
		StartedAt:   now,
	})

	err := agg.Apply(&TrialEndedEvent{
		ContractID: shared.ContractID("test-contract-001"),
		EndedAt:    now.AddDate(0, 0, 14),
		Converted:  false,
	})
	if err != nil {
		t.Fatalf("Apply TrialEndedEvent (not converted) failed: %v", err)
	}
	if agg.Status() != ContractStatusCancelled {
		t.Errorf("expected cancelled after non-conversion, got %s", agg.Status())
	}
}

// testUnknownEvent implements DomainEvent for testing unknown event handling.
type testUnknownEvent struct{}

func (e *testUnknownEvent) EventType() eventstore.EventType { return "unknown.event" }

func TestApplyUnknownEvent(t *testing.T) {
	agg := newTestAggregate()

	err := agg.Apply(&testUnknownEvent{})
	if err == nil {
		t.Fatal("expected error for unknown event type")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("expected DomainError, got %T", err)
	}
	if domErr.Code != shared.ErrCodeUnknownEvent {
		t.Errorf("expected error code %s, got %s", shared.ErrCodeUnknownEvent, domErr.Code)
	}
}

func TestLoadFromHistory(t *testing.T) {
	// First, create events via an aggregate
	original := newTestAggregate()
	meta := newTestMetadata()

	if err := original.Create(newTestCommand(), meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := original.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	events := original.UncommittedEvents()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Replay from history
	restored := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := restored.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	if restored.Status() != ContractStatusActive {
		t.Errorf("expected active status, got %s", restored.Status())
	}
	if restored.AccountID() != shared.AccountID("acc-001") {
		t.Errorf("expected account acc-001, got %s", restored.AccountID())
	}
	if restored.Version() != 2 {
		t.Errorf("expected version 2, got %d", restored.Version())
	}
}

func TestLoadFromSnapshot(t *testing.T) {
	state := contractSnapshotState{
		ContractID:   shared.ContractID("snap-001"),
		AccountID:    shared.AccountID("acc-001"),
		PlanID:       shared.PlanID("plan-001"),
		Status:       ContractStatusActive,
		ContractType: ContractTypeSubscription,
		BillingCycle: BillingCycleMonthly,
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal snapshot state: %v", err)
	}

	snapshot := eventstore.Snapshot{
		StreamID: "snap-001",
		Version:  5,
		State:    data,
	}

	agg := NewContractAggregate(shared.ContractID("snap-001"), newTestClock())
	if err := agg.LoadFromSnapshot(snapshot); err != nil {
		t.Fatalf("LoadFromSnapshot failed: %v", err)
	}

	if agg.Status() != ContractStatusActive {
		t.Errorf("expected active, got %s", agg.Status())
	}
	if agg.Version() != 5 {
		t.Errorf("expected version 5, got %d", agg.Version())
	}
	if agg.ContractID() != shared.ContractID("snap-001") {
		t.Errorf("expected contract ID snap-001, got %s", agg.ContractID())
	}
}

func TestChangePrice(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	newPrice := shared.NewMoney(new(big.Rat).SetInt64(2000), shared.CurrencyJPY)
	effectiveAt := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	if err := agg.ChangePrice(newPrice, effectiveAt, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}
	if agg.Price().Amount().Cmp(new(big.Rat).SetInt64(2000)) != 0 {
		t.Error("price was not updated")
	}
}

func TestChangePlan(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	proration := &PlanChangeProration{
		CreditAmount:     newTestMoney(),
		ChargeAmount:     newTestMoney(),
		AdjustmentAmount: shared.NewMoney(new(big.Rat).SetInt64(0), shared.CurrencyJPY),
		EffectiveDate:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := agg.ChangePlan(shared.PlanID("plan-002"), proration, meta); err != nil {
		t.Fatalf("ChangePlan failed: %v", err)
	}
	if agg.PlanID() != shared.PlanID("plan-002") {
		t.Errorf("expected plan-002, got %s", agg.PlanID())
	}
}

func TestTrialLifecycle(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()

	if err := agg.Create(newTestCommand(), meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	trialConfig := TrialConfiguration{
		TrialEndDate:           time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		AutoConvert:            true,
		RequirePaymentMethod:   true,
		ConversionReminderDays: []int{3},
	}

	if err := agg.StartTrial(trialConfig, meta); err != nil {
		t.Fatalf("StartTrial failed: %v", err)
	}
	if agg.Status() != ContractStatusTrialing {
		t.Errorf("expected trialing, got %s", agg.Status())
	}
	if agg.TrialConfig() == nil {
		t.Fatal("expected trial config")
	}

	// Activate from trialing
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate from trialing failed: %v", err)
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected active, got %s", agg.Status())
	}
}

func TestEndTrialConverted(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()

	_ = agg.Create(newTestCommand(), meta)
	_ = agg.StartTrial(TrialConfiguration{
		TrialEndDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}, meta)

	if err := agg.EndTrial(true, meta); err != nil {
		t.Fatalf("EndTrial(converted=true) failed: %v", err)
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected active, got %s", agg.Status())
	}
}

func TestEndTrialNotConverted(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()

	_ = agg.Create(newTestCommand(), meta)
	_ = agg.StartTrial(TrialConfiguration{
		TrialEndDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}, meta)

	if err := agg.EndTrial(false, meta); err != nil {
		t.Fatalf("EndTrial(converted=false) failed: %v", err)
	}
	if agg.Status() != ContractStatusCancelled {
		t.Errorf("expected cancelled, got %s", agg.Status())
	}
}

func TestChangePaymentMethod(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	pmID := "pm-visa-1234"
	if err := agg.ChangePaymentMethod(&pmID, meta); err != nil {
		t.Fatalf("ChangePaymentMethod failed: %v", err)
	}
	if agg.PaymentMethodID() == nil || *agg.PaymentMethodID() != "pm-visa-1234" {
		t.Errorf("expected pm-visa-1234, got %v", agg.PaymentMethodID())
	}

	// Change to a different payment method
	pmID2 := "pm-mastercard-5678"
	if err := agg.ChangePaymentMethod(&pmID2, meta); err != nil {
		t.Fatalf("ChangePaymentMethod (second) failed: %v", err)
	}
	if *agg.PaymentMethodID() != "pm-mastercard-5678" {
		t.Errorf("expected pm-mastercard-5678, got %s", *agg.PaymentMethodID())
	}

	// Clear payment method (set to nil)
	if err := agg.ChangePaymentMethod(nil, meta); err != nil {
		t.Fatalf("ChangePaymentMethod (clear) failed: %v", err)
	}
	if agg.PaymentMethodID() != nil {
		t.Errorf("expected nil, got %v", agg.PaymentMethodID())
	}
}

func TestChangePaymentMethod_InvalidStates(t *testing.T) {
	meta := newTestMetadata()
	pmID := "pm-test"

	tests := []struct {
		name  string
		setup func() *ContractAggregate
	}{
		{
			name: "from cancelled",
			setup: func() *ContractAggregate {
				agg := createActiveAggregate(t)
				_ = agg.Cancel("test", meta)
				return agg
			},
		},
		{
			name: "from uninitialized",
			setup: func() *ContractAggregate {
				return newTestAggregate()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := tt.setup()
			err := agg.ChangePaymentMethod(&pmID, meta)
			if err == nil {
				t.Fatal("expected error for invalid state transition, got nil")
			}
		})
	}
}

func TestChangePaymentMethod_FromDraftAndTrialing(t *testing.T) {
	meta := newTestMetadata()
	pmID := "pm-test"

	// Draft
	agg := newTestAggregate()
	_ = agg.Create(newTestCommand(), meta)
	if err := agg.ChangePaymentMethod(&pmID, meta); err != nil {
		t.Fatalf("ChangePaymentMethod from draft failed: %v", err)
	}

	// Trialing
	agg2 := newTestAggregate()
	_ = agg2.Create(newTestCommand(), meta)
	_ = agg2.StartTrial(TrialConfiguration{
		TrialEndDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}, meta)
	if err := agg2.ChangePaymentMethod(&pmID, meta); err != nil {
		t.Fatalf("ChangePaymentMethod from trialing failed: %v", err)
	}
}

func TestSnapshotRoundTrip_WithPaymentMethod(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	pmID := "pm-snapshot-test"
	_ = agg.ChangePaymentMethod(&pmID, meta)

	// Marshal snapshot
	data, err := agg.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot failed: %v", err)
	}

	// Restore from snapshot
	restored := NewContractAggregate(agg.ContractID(), newTestClock())
	snapshot := eventstore.Snapshot{
		StreamID: string(agg.ContractID()),
		Version:  agg.Version(),
		State:    data,
	}
	if err := restored.LoadFromSnapshot(snapshot); err != nil {
		t.Fatalf("LoadFromSnapshot failed: %v", err)
	}

	if restored.PaymentMethodID() == nil || *restored.PaymentMethodID() != "pm-snapshot-test" {
		t.Errorf("expected pm-snapshot-test after snapshot restore, got %v", restored.PaymentMethodID())
	}
}

func TestCancelFromSuspended(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	_ = agg.Suspend(SuspensionConfiguration{
		BillingBehavior: SuspensionBillingDefer,
		Reason:          "test",
	}, meta)

	if err := agg.Cancel("closing account", meta); err != nil {
		t.Fatalf("Cancel from suspended failed: %v", err)
	}
	if agg.Status() != ContractStatusCancelled {
		t.Errorf("expected cancelled, got %s", agg.Status())
	}
}
