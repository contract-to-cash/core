package contract

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/pricing"
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
		ContractType: ContractTypeSubscription,
		Interval:     pricing.Monthly(),
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
				return agg.ChangePrice(shared.PriceID("price-new"), ChangePolicyImmediate, nil, meta)
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
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		Interval:     pricing.Monthly(),
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

	// PriceChangedEvent (new format with PriceID)
	err = agg.Apply(&PriceChangedEvent{
		ContractID: shared.ContractID("test-contract-001"),
		OldPriceID: shared.PriceID(""),
		NewPriceID: shared.PriceID("price-new-001"),
		Policy:     ChangePolicyImmediate,
		ChangedAt:  now,
	})
	if err != nil {
		t.Fatalf("Apply PriceChangedEvent failed: %v", err)
	}
	if agg.PriceID() != shared.PriceID("price-new-001") {
		t.Errorf("expected priceID price-new-001, got %s", agg.PriceID())
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
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		Interval:     pricing.Monthly(),
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
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		Interval:     pricing.Monthly(),
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
		Status:       ContractStatusActive,
		ContractType: ContractTypeSubscription,
		Interval:     pricing.Monthly(),
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

	newPriceID := shared.PriceID("price-new-001")
	if err := agg.ChangePrice(newPriceID, ChangePolicyImmediate, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}
	if agg.PriceID() != newPriceID {
		t.Errorf("expected priceID %s, got %s", newPriceID, agg.PriceID())
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

// TestEndTrialConvertedEstablishesPeriod is a regression test for issue #146:
// EndTrial(converted=true) must set an initial billing period from the billing
// interval, matching how Activate establishes it, so the converted contract is
// not dropped from the renewal/billing cycle.
func TestEndTrialConvertedEstablishesPeriod(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()

	_ = agg.Create(newTestCommand(), meta) // Monthly interval
	_ = agg.StartTrial(TrialConfiguration{
		TrialEndDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}, meta)

	if err := agg.EndTrial(true, meta); err != nil {
		t.Fatalf("EndTrial(converted=true) failed: %v", err)
	}

	if agg.CurrentPeriod().IsZero() {
		t.Fatal("expected non-zero billing period after conversion")
	}
	// Fixed clock is 2026-01-01; Monthly interval -> period ends 2026-02-01.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !agg.CurrentPeriod().Start().Equal(now) {
		t.Errorf("expected period start %s, got %s", now, agg.CurrentPeriod().Start())
	}
	if !agg.CurrentPeriod().End().Equal(now.AddDate(0, 1, 0)) {
		t.Errorf("expected period end %s, got %s", now.AddDate(0, 1, 0), agg.CurrentPeriod().End())
	}
}

// TestEndTrialNotConvertedHasNoPeriod verifies a cancelled (non-converted) trial
// leaves the billing period unset — there is nothing to bill.
func TestEndTrialNotConvertedHasNoPeriod(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()

	_ = agg.Create(newTestCommand(), meta)
	_ = agg.StartTrial(TrialConfiguration{
		TrialEndDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}, meta)

	if err := agg.EndTrial(false, meta); err != nil {
		t.Fatalf("EndTrial(converted=false) failed: %v", err)
	}
	if !agg.CurrentPeriod().IsZero() {
		t.Errorf("expected zero billing period for cancelled trial, got %s", agg.CurrentPeriod())
	}
}

// TestActivateFromTrialingCoherentWithEndTrial verifies issue #146's "other
// half": activating a trialing contract via Activate is coherent with the
// batch EndTrial path — it establishes the billing period, clears trialConfig,
// and records a TrialEndedEvent (converted=true) rather than a
// ContractActivatedEvent.
func TestActivateFromTrialingCoherentWithEndTrial(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()

	_ = agg.Create(newTestCommand(), meta)
	_ = agg.StartTrial(TrialConfiguration{
		TrialEndDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}, meta)

	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate from trialing failed: %v", err)
	}

	if agg.Status() != ContractStatusActive {
		t.Errorf("expected active, got %s", agg.Status())
	}
	if agg.CurrentPeriod().IsZero() {
		t.Error("expected non-zero billing period after activation from trialing")
	}
	if agg.TrialConfig() != nil {
		t.Error("expected trialConfig to be cleared after activation from trialing")
	}

	// The conversion must be recorded as a TrialEndedEvent, matching the batch
	// path, so both conversion routes replay and fire hooks identically.
	events := agg.UncommittedEvents()
	last := events[len(events)-1]
	if last.Type != EventTypeTrialEnded {
		t.Errorf("expected last event %s, got %s", EventTypeTrialEnded, last.Type)
	}

	// The resulting state must equal the state produced by EndTrial(true).
	viaEndTrial := newTestAggregate()
	_ = viaEndTrial.Create(newTestCommand(), meta)
	_ = viaEndTrial.StartTrial(TrialConfiguration{
		TrialEndDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}, meta)
	_ = viaEndTrial.EndTrial(true, meta)
	if !agg.CurrentPeriod().Equals(viaEndTrial.CurrentPeriod()) {
		t.Errorf("Activate and EndTrial produced different periods: %s vs %s",
			agg.CurrentPeriod(), viaEndTrial.CurrentPeriod())
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

func TestChangePaymentMethod_FromSuspended(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	_ = agg.Suspend(SuspensionConfiguration{
		BillingBehavior: SuspensionBillingSkip,
		Reason:          "non-payment",
	}, meta)

	pmID := "pm-new-card"
	if err := agg.ChangePaymentMethod(&pmID, meta); err != nil {
		t.Fatalf("ChangePaymentMethod from suspended failed: %v", err)
	}
	if agg.PaymentMethodID() == nil || *agg.PaymentMethodID() != "pm-new-card" {
		t.Errorf("expected pm-new-card, got %v", agg.PaymentMethodID())
	}
}

func TestLoadFromHistory_WithPaymentMethodChanged(t *testing.T) {
	original := newTestAggregate()
	meta := newTestMetadata()

	if err := original.Create(newTestCommand(), meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := original.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	pmID := "pm-history-test"
	if err := original.ChangePaymentMethod(&pmID, meta); err != nil {
		t.Fatalf("ChangePaymentMethod failed: %v", err)
	}

	events := original.UncommittedEvents()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Replay from history (exercises JSON serialize → deserialize → Apply)
	restored := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := restored.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	if restored.PaymentMethodID() == nil || *restored.PaymentMethodID() != "pm-history-test" {
		t.Errorf("expected pm-history-test after history replay, got %v", restored.PaymentMethodID())
	}
	if restored.Status() != ContractStatusActive {
		t.Errorf("expected active, got %s", restored.Status())
	}
	if restored.Version() != 3 {
		t.Errorf("expected version 3, got %d", restored.Version())
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

func createActiveAggregateWithAutoRenew(t *testing.T) *ContractAggregate {
	t.Helper()
	agg := newTestAggregate()
	meta := newTestMetadata()
	cmd := newTestCommand()
	cmd.AutoRenew = true
	if err := agg.Create(cmd, meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	return agg
}

func TestRenew_HappyPath(t *testing.T) {
	agg := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	oldPeriod := agg.CurrentPeriod()

	if err := agg.RenewWithInterval(agg.GetInterval(), meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	if agg.Status() != ContractStatusActive {
		t.Errorf("expected status active, got %s", agg.Status())
	}

	newPeriod := agg.CurrentPeriod()
	if !newPeriod.Start().Equal(oldPeriod.End()) {
		t.Errorf("expected new period start %v, got %v", oldPeriod.End(), newPeriod.Start())
	}
	// Monthly billing cycle: new period end should be 1 month after old period end
	expectedEnd := oldPeriod.End().AddDate(0, 1, 0)
	if !newPeriod.End().Equal(expectedEnd) {
		t.Errorf("expected new period end %v, got %v", expectedEnd, newPeriod.End())
	}
}

func TestRenew_WithPendingPriceID(t *testing.T) {
	agg := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	priceID := shared.PriceID("price-new-001")
	if err := agg.ChangePrice(priceID, ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice END_OF_TERM failed: %v", err)
	}

	if err := agg.RenewWithInterval(agg.GetInterval(), meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	if agg.PendingPriceID() != nil {
		t.Error("expected pendingPriceID to be nil after renewal")
	}
	if agg.PriceID() != priceID {
		t.Errorf("expected priceID %s after renewal, got %s", priceID, agg.PriceID())
	}

	// Verify the event has PriceChanged=true
	events := agg.UncommittedEvents()
	// Events: Create + Activate + PriceChangeScheduled + Renew = 4
	if len(events) < 4 {
		t.Fatalf("expected at least 4 events, got %d", len(events))
	}
	lastEvent := events[len(events)-1]
	if lastEvent.Type != EventTypeContractRenewed {
		t.Fatalf("expected last event type %s, got %s", EventTypeContractRenewed, lastEvent.Type)
	}
	// Deserialize and check PriceChanged
	domainEvent, err := contractEventRegistry.Deserialize(eventstore.EventType(lastEvent.Type), lastEvent.Data)
	if err != nil {
		t.Fatalf("failed to deserialize event: %v", err)
	}
	renewed, ok := domainEvent.(*ContractRenewedEvent)
	if !ok {
		t.Fatalf("expected *ContractRenewedEvent, got %T", domainEvent)
	}
	if !renewed.PriceChanged {
		t.Error("expected PriceChanged=true in ContractRenewedEvent")
	}
	if renewed.NewPriceID != priceID {
		t.Errorf("expected NewPriceID=%s, got %s", priceID, renewed.NewPriceID)
	}
}

func TestRenew_AutoRenewFalse_Expires(t *testing.T) {
	agg := createActiveAggregate(t) // autoRenew defaults to false
	meta := newTestMetadata()

	if err := agg.RenewWithInterval(agg.GetInterval(), meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	if agg.Status() != ContractStatusExpired {
		t.Errorf("expected status expired, got %s", agg.Status())
	}
}

func TestRenew_CancelAtPeriodEnd_Cancels(t *testing.T) {
	agg := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	if err := agg.ScheduleCancellation("customer request", meta); err != nil {
		t.Fatalf("ScheduleCancellation failed: %v", err)
	}

	if err := agg.RenewWithInterval(agg.GetInterval(), meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	if agg.Status() != ContractStatusCancelled {
		t.Errorf("expected status cancelled, got %s", agg.Status())
	}
}

func TestRenew_NotActive_Fails(t *testing.T) {
	meta := newTestMetadata()

	tests := []struct {
		name  string
		setup func() *ContractAggregate
	}{
		{
			name: "from draft",
			setup: func() *ContractAggregate {
				agg := newTestAggregate()
				_ = agg.Create(newTestCommand(), meta)
				return agg
			},
		},
		{
			name: "from cancelled",
			setup: func() *ContractAggregate {
				agg := createActiveAggregate(t)
				_ = agg.Cancel("test", meta)
				return agg
			},
		},
		{
			name: "from suspended",
			setup: func() *ContractAggregate {
				agg := createActiveAggregate(t)
				_ = agg.Suspend(SuspensionConfiguration{
					BillingBehavior: SuspensionBillingSkip,
					Reason:          "test",
				}, meta)
				return agg
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := tt.setup()
			err := agg.RenewWithInterval(agg.GetInterval(), meta)
			if err == nil {
				t.Fatal("expected error for renew from non-active state, got nil")
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

func TestActivate_SetsCurrentPeriod(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()

	if err := agg.Create(newTestCommand(), meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	period := agg.CurrentPeriod()

	if !period.Start().Equal(now) {
		t.Errorf("expected period start %v, got %v", now, period.Start())
	}
	// Monthly billing cycle: end should be 1 month after start
	expectedEnd := now.AddDate(0, 1, 0)
	if !period.End().Equal(expectedEnd) {
		t.Errorf("expected period end %v, got %v", expectedEnd, period.End())
	}
}

func TestApplyContractRenewedEvent(t *testing.T) {
	agg := newTestAggregate()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Set up aggregate to active state with a current period
	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		Interval:     pricing.Monthly(),
		ContractType: ContractTypeSubscription,
		AutoRenew:    true,
		CreatedAt:    now,
	})

	oldPeriodStart := now
	oldPeriodEnd := now.AddDate(0, 1, 0)
	oldPeriod, _ := shared.NewDateRange(oldPeriodStart, oldPeriodEnd)

	_ = agg.Apply(&ContractActivatedEvent{
		ContractID:    shared.ContractID("test-contract-001"),
		ActivatedAt:   now,
		CurrentPeriod: oldPeriod,
	})

	newPeriodStart := oldPeriodEnd
	newPeriodEnd := oldPeriodEnd.AddDate(0, 1, 0)
	newPeriod, _ := shared.NewDateRange(newPeriodStart, newPeriodEnd)

	renewedAt := oldPeriodEnd
	err := agg.Apply(&ContractRenewedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		OldPeriod:    oldPeriod,
		NewPeriod:    newPeriod,
		PriceChanged: false,
		RenewedAt:    renewedAt,
	})
	if err != nil {
		t.Fatalf("Apply ContractRenewedEvent failed: %v", err)
	}

	if !agg.CurrentPeriod().Start().Equal(newPeriodStart) {
		t.Errorf("expected current period start %v, got %v", newPeriodStart, agg.CurrentPeriod().Start())
	}
	if !agg.CurrentPeriod().End().Equal(newPeriodEnd) {
		t.Errorf("expected current period end %v, got %v", newPeriodEnd, agg.CurrentPeriod().End())
	}
	if agg.PendingPriceID() != nil {
		t.Error("expected pendingPriceID to be nil after renewal")
	}
	if !agg.UpdatedAt().Equal(renewedAt) {
		t.Errorf("expected updatedAt %v, got %v", renewedAt, agg.UpdatedAt())
	}
}

func TestApplyContractExpiredEvent(t *testing.T) {
	agg := newTestAggregate()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		Interval:     pricing.Monthly(),
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	period, _ := shared.NewDateRange(now, now.AddDate(0, 1, 0))
	_ = agg.Apply(&ContractActivatedEvent{
		ContractID:    shared.ContractID("test-contract-001"),
		ActivatedAt:   now,
		CurrentPeriod: period,
	})

	expiredAt := now.AddDate(0, 1, 0)
	err := agg.Apply(&ContractExpiredEvent{
		ContractID:  shared.ContractID("test-contract-001"),
		ExpiredAt:   expiredAt,
		FinalPeriod: period,
	})
	if err != nil {
		t.Fatalf("Apply ContractExpiredEvent failed: %v", err)
	}

	if agg.Status() != ContractStatusExpired {
		t.Errorf("expected status expired, got %s", agg.Status())
	}
	if !agg.UpdatedAt().Equal(expiredAt) {
		t.Errorf("expected updatedAt %v, got %v", expiredAt, agg.UpdatedAt())
	}
}

func TestScheduleCancellation_HappyPath(t *testing.T) {
	agg := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	if err := agg.ScheduleCancellation("customer request", meta); err != nil {
		t.Fatalf("ScheduleCancellation failed: %v", err)
	}

	if !agg.CancelAtPeriodEnd() {
		t.Error("expected cancelAtPeriodEnd to be true")
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected status active, got %s", agg.Status())
	}
}

func TestScheduleCancellation_NotActive_Fails(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()
	_ = agg.Create(newTestCommand(), meta)

	err := agg.ScheduleCancellation("test", meta)
	if err == nil {
		t.Fatal("expected error for ScheduleCancellation from draft state, got nil")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
}

func TestScheduleCancellation_AlreadyScheduled_Fails(t *testing.T) {
	agg := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	if err := agg.ScheduleCancellation("first", meta); err != nil {
		t.Fatalf("first ScheduleCancellation failed: %v", err)
	}

	err := agg.ScheduleCancellation("second", meta)
	if err == nil {
		t.Fatal("expected error for duplicate ScheduleCancellation, got nil")
	}
}

func TestUnscheduleCancellation_HappyPath(t *testing.T) {
	agg := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	if err := agg.ScheduleCancellation("customer request", meta); err != nil {
		t.Fatalf("ScheduleCancellation failed: %v", err)
	}
	if err := agg.UnscheduleCancellation(meta); err != nil {
		t.Fatalf("UnscheduleCancellation failed: %v", err)
	}

	if agg.CancelAtPeriodEnd() {
		t.Error("expected cancelAtPeriodEnd to be false after unscheduling")
	}
}

func TestUnscheduleCancellation_NotScheduled_Fails(t *testing.T) {
	agg := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	err := agg.UnscheduleCancellation(meta)
	if err == nil {
		t.Fatal("expected error for UnscheduleCancellation when not scheduled, got nil")
	}
}

func TestUnscheduleCancellation_ThenRenew_Renews(t *testing.T) {
	agg := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	if err := agg.ScheduleCancellation("customer request", meta); err != nil {
		t.Fatalf("ScheduleCancellation failed: %v", err)
	}
	if err := agg.UnscheduleCancellation(meta); err != nil {
		t.Fatalf("UnscheduleCancellation failed: %v", err)
	}

	oldPeriod := agg.CurrentPeriod()
	if err := agg.RenewWithInterval(agg.GetInterval(), meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	if agg.Status() != ContractStatusActive {
		t.Errorf("expected status active, got %s", agg.Status())
	}
	if agg.CurrentPeriod().Start().Equal(oldPeriod.Start()) {
		t.Error("expected period to advance after renewal")
	}
}

func TestApplyCancellationScheduledEvent(t *testing.T) {
	agg := newTestAggregate()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		Interval:     pricing.Monthly(),
		ContractType: ContractTypeSubscription,
		AutoRenew:    true,
		CreatedAt:    now,
	})

	scheduledAt := now.Add(24 * time.Hour)
	err := agg.Apply(&CancellationScheduledEvent{
		ContractID:  shared.ContractID("test-contract-001"),
		Reason:      "customer request",
		ScheduledAt: scheduledAt,
	})
	if err != nil {
		t.Fatalf("Apply CancellationScheduledEvent failed: %v", err)
	}

	if !agg.CancelAtPeriodEnd() {
		t.Error("expected cancelAtPeriodEnd to be true")
	}
	if !agg.UpdatedAt().Equal(scheduledAt) {
		t.Errorf("expected updatedAt %v, got %v", scheduledAt, agg.UpdatedAt())
	}
}

func TestApplyCancellationUnscheduledEvent(t *testing.T) {
	agg := newTestAggregate()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		Interval:     pricing.Monthly(),
		ContractType: ContractTypeSubscription,
		AutoRenew:    true,
		CreatedAt:    now,
	})

	_ = agg.Apply(&CancellationScheduledEvent{
		ContractID:  shared.ContractID("test-contract-001"),
		Reason:      "test",
		ScheduledAt: now,
	})

	unscheduledAt := now.Add(48 * time.Hour)
	err := agg.Apply(&CancellationUnscheduledEvent{
		ContractID:    shared.ContractID("test-contract-001"),
		UnscheduledAt: unscheduledAt,
	})
	if err != nil {
		t.Fatalf("Apply CancellationUnscheduledEvent failed: %v", err)
	}

	if agg.CancelAtPeriodEnd() {
		t.Error("expected cancelAtPeriodEnd to be false")
	}
	if !agg.UpdatedAt().Equal(unscheduledAt) {
		t.Errorf("expected updatedAt %v, got %v", unscheduledAt, agg.UpdatedAt())
	}
}

func TestLoadFromHistory_WithCancellationScheduled(t *testing.T) {
	original := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	if err := original.ScheduleCancellation("customer request", meta); err != nil {
		t.Fatalf("ScheduleCancellation failed: %v", err)
	}

	events := original.UncommittedEvents()
	// Events: Create + Activate + ScheduleCancellation = 3
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	restored := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := restored.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	if !restored.CancelAtPeriodEnd() {
		t.Error("expected cancelAtPeriodEnd to be true after history replay")
	}
	if restored.Status() != ContractStatusActive {
		t.Errorf("expected active, got %s", restored.Status())
	}
	if restored.Version() != 3 {
		t.Errorf("expected version 3, got %d", restored.Version())
	}
}

func TestSnapshotRoundTrip_WithCancelAtPeriodEnd(t *testing.T) {
	agg := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	if err := agg.ScheduleCancellation("customer request", meta); err != nil {
		t.Fatalf("ScheduleCancellation failed: %v", err)
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

	if !restored.CancelAtPeriodEnd() {
		t.Error("expected cancelAtPeriodEnd to be true after snapshot restore")
	}
	if restored.Status() != ContractStatusActive {
		t.Errorf("expected active, got %s", restored.Status())
	}
}

func TestRenew_CancelAtPeriodEnd_ResetsCancelFlag(t *testing.T) {
	agg := createActiveAggregateWithAutoRenew(t)
	meta := newTestMetadata()

	if err := agg.ScheduleCancellation("customer request", meta); err != nil {
		t.Fatalf("ScheduleCancellation failed: %v", err)
	}
	if err := agg.RenewWithInterval(agg.GetInterval(), meta); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	if agg.Status() != ContractStatusCancelled {
		t.Errorf("expected cancelled, got %s", agg.Status())
	}
	if agg.CancelAtPeriodEnd() {
		t.Error("expected cancelAtPeriodEnd to be false after cancellation")
	}
}

func TestCreate_NoInterval_ReturnsError(t *testing.T) {
	agg := newTestAggregate()
	cmd := CreateContractCommand{
		AccountID:    shared.AccountID("acc-001"),
		ContractType: ContractTypeSubscription,
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		// Interval unset
	}
	err := agg.Create(cmd, newTestMetadata())
	if err == nil {
		t.Fatal("expected error when Interval is unset")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if domErr.Code != shared.ErrCodeValidation {
		t.Errorf("expected ErrCodeValidation, got %s", domErr.Code)
	}
}

func TestRenewWithInterval_SetsOldInterval(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()

	// Create with Quarterly interval
	cmd := CreateContractCommand{
		AccountID:    shared.AccountID("acc-001"),
		ContractType: ContractTypeSubscription,
		Interval:     pricing.Quarterly(),
		Price:        newTestMoney(),
		BasePrice:    newTestMoney(),
		AutoRenew:    true,
	}
	if err := agg.Create(cmd, meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	// Renew with SemiAnnual
	if err := agg.RenewWithInterval(pricing.SemiAnnual(), meta); err != nil {
		t.Fatalf("RenewWithInterval failed: %v", err)
	}

	// Check the last uncommitted event contains OldInterval
	events := agg.UncommittedEvents()
	lastEvent := events[len(events)-1]
	var renewed ContractRenewedEvent
	if err := json.Unmarshal(lastEvent.Data, &renewed); err != nil {
		t.Fatalf("unmarshal renewed event: %v", err)
	}
	if !renewed.OldInterval.Equals(pricing.Quarterly()) {
		t.Errorf("expected OldInterval Quarterly, got %v", renewed.OldInterval)
	}
	if !renewed.NewInterval.Equals(pricing.SemiAnnual()) {
		t.Errorf("expected NewInterval SemiAnnual, got %v", renewed.NewInterval)
	}
}
