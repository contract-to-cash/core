// pointer_isolation_test.go verifies that ContractAggregate getters and
// state-transition intakes do NOT leak internal pointer state. This aligns
// contract-domain entities with the domain/shared/money.go precedent and
// extends issue #96 coverage to domain/contract. See issue #109.
package contract

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// newTestClock is defined in aggregate_test.go and reused here to keep
// the package-internal test fixtures aligned (issue #109 review NIT).
func newActiveAggregate(t *testing.T) *ContractAggregate {
	t.Helper()
	clock := newTestClock()
	agg := NewContractAggregate(shared.NewContractID(), clock)
	metadata := eventstore.EventMetadata{UserID: "test"}
	if err := agg.Create(CreateContractCommand{
		IdempotencyKey: "idem-contract-pointer_isolation-1",
		AccountID:      shared.NewAccountID(),
		PriceID:        shared.NewPriceID(),
		ContractType:   ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AutoRenew:      true,
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
	clock := newTestClock()
	agg := NewContractAggregate(shared.NewContractID(), clock)
	if err := agg.Create(CreateContractCommand{
		IdempotencyKey: "idem-contract-pointer_isolation-2",
		AccountID:      shared.NewAccountID(),
		PriceID:        shared.NewPriceID(),
		ContractType:   ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
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
	clock := newTestClock()
	agg := NewContractAggregate(shared.NewContractID(), clock)
	if err := agg.Create(CreateContractCommand{
		IdempotencyKey: "idem-contract-pointer_isolation-3",
		AccountID:      shared.NewAccountID(),
		PriceID:        shared.NewPriceID(),
		ContractType:   ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
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

	restored := NewContractAggregate(src.ContractID(), newTestClock())
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

// TestAggregate_LoadFromSnapshot_IsolatesTrialConfig closes the gap
// flagged in the PR #120 review: the original snapshot round-trip test
// only exercised PaymentMethodID and PendingPriceID, so a regression in
// the LoadFromSnapshot deep-copy of TrialConfig.ConversionReminderDays
// (slice) would only have been caught indirectly via the getter-defense
// test on the live aggregate.
func TestAggregate_LoadFromSnapshot_IsolatesTrialConfig(t *testing.T) {
	clock := newTestClock()
	src := NewContractAggregate(shared.NewContractID(), clock)
	if err := src.Create(CreateContractCommand{
		IdempotencyKey: "idem-contract-pointer_isolation-4",
		AccountID:      shared.NewAccountID(),
		PriceID:        shared.NewPriceID(),
		ContractType:   ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
	}, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := src.StartTrial(TrialConfiguration{
		TrialEndDate:           clock.Now().AddDate(0, 0, 14),
		AutoConvert:            true,
		ConversionReminderDays: []int{7, 3, 1},
	}, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("StartTrial: %v", err)
	}

	data, err := src.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}

	restored := NewContractAggregate(src.ContractID(), newTestClock())
	if err := restored.LoadFromSnapshot(eventstore.Snapshot{State: data, Version: src.Version()}); err != nil {
		t.Fatalf("LoadFromSnapshot: %v", err)
	}

	if tc := restored.TrialConfig(); tc != nil && len(tc.ConversionReminderDays) > 0 {
		tc.ConversionReminderDays[0] = 999
		tc.AutoConvert = false
	}

	again := restored.TrialConfig()
	if again == nil {
		t.Fatal("TrialConfig must survive snapshot round-trip")
	}
	if len(again.ConversionReminderDays) == 0 || again.ConversionReminderDays[0] != 7 {
		t.Errorf("LoadFromSnapshot aliasing: TrialConfig.ConversionReminderDays[0] = %v, want 7", again.ConversionReminderDays)
	}
	if !again.AutoConvert {
		t.Error("LoadFromSnapshot aliasing: TrialConfig.AutoConvert mutated via getter")
	}
}

// TestAggregate_LoadFromSnapshot_IsolatesSuspensionConfig is the
// SuspensionConfig.ResumeDate (*time.Time) counterpart to the TrialConfig
// snapshot test above (PR #120 review MINOR follow-up).
func TestAggregate_LoadFromSnapshot_IsolatesSuspensionConfig(t *testing.T) {
	src := newActiveAggregate(t)
	originalResume := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	if err := src.Suspend(SuspensionConfiguration{
		BillingBehavior: SuspensionBillingDefer,
		ResumeDate:      &originalResume,
		Reason:          "snapshot-isolation",
	}, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	data, err := src.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}

	restored := NewContractAggregate(src.ContractID(), newTestClock())
	if err := restored.LoadFromSnapshot(eventstore.Snapshot{State: data, Version: src.Version()}); err != nil {
		t.Fatalf("LoadFromSnapshot: %v", err)
	}

	if sc := restored.SuspensionConfig(); sc != nil && sc.ResumeDate != nil {
		*sc.ResumeDate = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
		sc.Reason = "hacked"
	}

	again := restored.SuspensionConfig()
	if again == nil {
		t.Fatal("SuspensionConfig must survive snapshot round-trip")
	}
	if again.ResumeDate == nil || !again.ResumeDate.Equal(originalResume) {
		t.Errorf("LoadFromSnapshot aliasing: SuspensionConfig.ResumeDate got %v, want %v", again.ResumeDate, originalResume)
	}
	if again.Reason != "snapshot-isolation" {
		t.Errorf("LoadFromSnapshot aliasing: SuspensionConfig.Reason mutated to %q", again.Reason)
	}
}
