// pointer_isolation_test.go verifies that ContractAggregate getters and
// state-transition intakes do NOT leak internal pointer state. This aligns
// contract-domain entities with the domain/shared/money.go precedent and
// extends issue #96 coverage to domain/contract. See issue #109.
package contract

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

func fixedClock() shared.Clock {
	return shared.FixedClock{FixedTime: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}
}

func newActiveAggregate(t *testing.T) *ContractAggregate {
	t.Helper()
	clock := fixedClock()
	agg := NewContractAggregate(shared.NewContractID(), clock)
	metadata := eventstore.EventMetadata{UserID: "test"}
	if err := agg.Create(CreateContractCommand{
		AccountID:    shared.NewAccountID(),
		PriceID:      shared.NewPriceID(),
		ContractType: ContractTypeSubscription,
		BillingCycle: BillingCycleMonthly,
		Price:        shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AutoRenew:    true,
	}, metadata); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := agg.Activate(metadata); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	return agg
}

// --- PaymentMethodID ---

func TestAggregate_PaymentMethodID_GetterIsDefensivelyCopied(t *testing.T) {
	agg := newActiveAggregate(t)
	pm := "pm-original"
	if err := agg.ChangePaymentMethod(&pm, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("ChangePaymentMethod: %v", err)
	}

	got := agg.PaymentMethodID()
	if got == nil {
		t.Fatal("PaymentMethodID must not be nil")
	}
	*got = "pm-hacked"

	again := agg.PaymentMethodID()
	if again == nil || *again != "pm-original" {
		t.Errorf("ContractAggregate.PaymentMethodID() leaks internal pointer: got %v, want pm-original", again)
	}
}

func TestAggregate_PaymentMethodID_IntakeIsDefensivelyCopied(t *testing.T) {
	agg := newActiveAggregate(t)
	pm := "pm-original"
	if err := agg.ChangePaymentMethod(&pm, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("ChangePaymentMethod: %v", err)
	}

	pm = "pm-hacked-via-intake"

	got := agg.PaymentMethodID()
	if got == nil || *got != "pm-original" {
		t.Errorf("ChangePaymentMethod does not defend paymentMethodID at intake: got %v, want pm-original", got)
	}
}

func TestAggregate_PaymentMethodID_NilSafe(t *testing.T) {
	agg := newActiveAggregate(t)
	if agg.PaymentMethodID() != nil {
		t.Errorf("expected nil PaymentMethodID before ChangePaymentMethod, got %v", agg.PaymentMethodID())
	}
}

// --- PendingPriceID ---

func TestAggregate_PendingPriceID_GetterIsDefensivelyCopied(t *testing.T) {
	agg := newActiveAggregate(t)
	newPriceID := shared.PriceID("price-new")
	if err := agg.ChangePrice(newPriceID, ChangePolicyEndOfTerm, nil, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("ChangePrice: %v", err)
	}

	got := agg.PendingPriceID()
	if got == nil {
		t.Fatal("PendingPriceID must not be nil")
	}
	*got = shared.PriceID("price-hacked")

	again := agg.PendingPriceID()
	if again == nil || *again != newPriceID {
		t.Errorf("ContractAggregate.PendingPriceID() leaks internal pointer: got %v, want %v", again, newPriceID)
	}
}

func TestAggregate_PendingPriceID_NilSafe(t *testing.T) {
	agg := newActiveAggregate(t)
	if agg.PendingPriceID() != nil {
		t.Errorf("expected nil PendingPriceID, got %v", agg.PendingPriceID())
	}
}

// --- TrialConfig ---

func TestAggregate_TrialConfig_GetterIsDefensivelyCopied(t *testing.T) {
	clock := fixedClock()
	agg := NewContractAggregate(shared.NewContractID(), clock)
	if err := agg.Create(CreateContractCommand{
		AccountID:    shared.NewAccountID(),
		PriceID:      shared.NewPriceID(),
		ContractType: ContractTypeSubscription,
		BillingCycle: BillingCycleMonthly,
		Price:        shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
	}, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	reminder := []int{3, 1}
	cfg := TrialConfiguration{
		TrialEndDate:           clock.Now().AddDate(0, 0, 14),
		AutoConvert:            true,
		ConversionReminderDays: reminder,
	}
	if err := agg.StartTrial(cfg, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("StartTrial: %v", err)
	}

	got := agg.TrialConfig()
	if got == nil {
		t.Fatal("TrialConfig must not be nil")
	}
	got.AutoConvert = false
	got.ConversionReminderDays[0] = 999

	again := agg.TrialConfig()
	if again == nil {
		t.Fatal("TrialConfig must not be nil on second read")
	}
	if !again.AutoConvert {
		t.Error("ContractAggregate.TrialConfig() leaks struct fields: AutoConvert was mutated")
	}
	if again.ConversionReminderDays[0] != 3 {
		t.Errorf("ContractAggregate.TrialConfig() leaks slice: got ConversionReminderDays[0]=%d, want 3", again.ConversionReminderDays[0])
	}
}

func TestAggregate_TrialConfig_IntakeIsDefensivelyCopied(t *testing.T) {
	clock := fixedClock()
	agg := NewContractAggregate(shared.NewContractID(), clock)
	if err := agg.Create(CreateContractCommand{
		AccountID:    shared.NewAccountID(),
		PriceID:      shared.NewPriceID(),
		ContractType: ContractTypeSubscription,
		BillingCycle: BillingCycleMonthly,
		Price:        shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
	}, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	reminder := []int{3, 1}
	cfg := TrialConfiguration{
		TrialEndDate:           clock.Now().AddDate(0, 0, 14),
		ConversionReminderDays: reminder,
	}
	if err := agg.StartTrial(cfg, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("StartTrial: %v", err)
	}

	// Mutate caller-owned slice after StartTrial returned.
	reminder[0] = 999

	got := agg.TrialConfig()
	if got == nil {
		t.Fatal("TrialConfig must not be nil")
	}
	if got.ConversionReminderDays[0] != 3 {
		t.Errorf("StartTrial does not defend slice at intake: got ConversionReminderDays[0]=%d, want 3", got.ConversionReminderDays[0])
	}
}

func TestAggregate_TrialConfig_NilSafe(t *testing.T) {
	agg := newActiveAggregate(t)
	if agg.TrialConfig() != nil {
		t.Errorf("expected nil TrialConfig, got %v", agg.TrialConfig())
	}
}

// --- SuspensionConfig ---

func TestAggregate_SuspensionConfig_GetterIsDefensivelyCopied(t *testing.T) {
	agg := newActiveAggregate(t)
	resume := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := agg.Suspend(SuspensionConfiguration{
		BillingBehavior: SuspensionBillingSkip,
		ResumeDate:      &resume,
		Reason:          "test",
	}, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	got := agg.SuspensionConfig()
	if got == nil || got.ResumeDate == nil {
		t.Fatal("SuspensionConfig / ResumeDate must not be nil")
	}
	got.Reason = "hacked"
	*got.ResumeDate = time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC)

	again := agg.SuspensionConfig()
	if again == nil {
		t.Fatal("SuspensionConfig must not be nil on second read")
	}
	if again.Reason != "test" {
		t.Errorf("ContractAggregate.SuspensionConfig() leaks struct fields: Reason was mutated to %q", again.Reason)
	}
	if again.ResumeDate == nil || !again.ResumeDate.Equal(resume) {
		t.Errorf("ContractAggregate.SuspensionConfig() leaks ResumeDate pointer: got %v, want %v", again.ResumeDate, resume)
	}
}

func TestAggregate_SuspensionConfig_IntakeIsDefensivelyCopied(t *testing.T) {
	agg := newActiveAggregate(t)
	resume := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := agg.Suspend(SuspensionConfiguration{
		BillingBehavior: SuspensionBillingSkip,
		ResumeDate:      &resume,
	}, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	// Mutate caller-owned time after Suspend returned.
	resume = time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC)

	got := agg.SuspensionConfig()
	if got == nil || got.ResumeDate == nil {
		t.Fatal("SuspensionConfig / ResumeDate must not be nil")
	}
	if !got.ResumeDate.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Suspend does not defend ResumeDate at intake: got %v", got.ResumeDate)
	}
}

func TestAggregate_SuspensionConfig_NilSafe(t *testing.T) {
	agg := newActiveAggregate(t)
	if agg.SuspensionConfig() != nil {
		t.Errorf("expected nil SuspensionConfig, got %v", agg.SuspensionConfig())
	}
}

// --- Snapshot round-trip isolation ---

func TestAggregate_LoadFromSnapshot_IsolatesPointerState(t *testing.T) {
	src := newActiveAggregate(t)
	pm := "pm-snapshot"
	if err := src.ChangePaymentMethod(&pm, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("ChangePaymentMethod: %v", err)
	}
	if err := src.ChangePrice(shared.PriceID("price-new"), ChangePolicyEndOfTerm, nil, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("ChangePrice: %v", err)
	}
	data, err := src.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}

	restored := NewContractAggregate(src.ContractID(), fixedClock())
	if err := restored.LoadFromSnapshot(eventstore.Snapshot{State: data, Version: src.Version()}); err != nil {
		t.Fatalf("LoadFromSnapshot: %v", err)
	}

	// Mutating getters on the restored aggregate must not corrupt its internal
	// state.
	if g := restored.PaymentMethodID(); g != nil {
		*g = "pm-hacked"
	}
	if g := restored.PendingPriceID(); g != nil {
		*g = shared.PriceID("price-hacked")
	}

	if got := restored.PaymentMethodID(); got == nil || *got != "pm-snapshot" {
		t.Errorf("LoadFromSnapshot aliasing: PaymentMethodID got %v, want pm-snapshot", got)
	}
	if got := restored.PendingPriceID(); got == nil || *got != "price-new" {
		t.Errorf("LoadFromSnapshot aliasing: PendingPriceID got %v, want price-new", got)
	}
}
