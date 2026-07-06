package integration

import (
	"context"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
)

func TestContractFullLifecycle(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)

	contractID := shared.NewContractID()
	agg := contract.NewContractAggregate(contractID, clock)

	// Step 1: Create → draft
	err := agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-integration-contract_lifecycle-1",
		AccountID:      shared.AccountID("acc-lifecycle"),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          moneyJPY(3000),
		BasePrice:      moneyJPY(3000),
	}, emptyMetadata())
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	assertStatus(t, agg, contract.ContractStatusDraft)

	// Step 2: Activate → active
	err = agg.Activate(emptyMetadata())
	if err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	assertStatus(t, agg, contract.ContractStatusActive)

	// Step 3: Suspend → suspended
	err = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingSkip,
		Reason:          "non-payment",
	}, emptyMetadata())
	if err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}
	assertStatus(t, agg, contract.ContractStatusSuspended)

	// Step 4: Resume → active
	err = agg.Resume(emptyMetadata())
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	assertStatus(t, agg, contract.ContractStatusActive)

	// Step 5: Cancel → cancelled
	err = agg.Cancel("customer request", emptyMetadata())
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	assertStatus(t, agg, contract.ContractStatusCancelled)

	// Persist and verify
	err = contractRepo.Save(ctx, agg)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := contractRepo.FindByID(ctx, contractID)
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	assertStatus(t, loaded, contract.ContractStatusCancelled)
}

func TestTrialLifecycle(t *testing.T) {
	clock := fixedClock()

	contractID := shared.NewContractID()
	agg := contract.NewContractAggregate(contractID, clock)

	// Create → draft
	err := agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-integration-contract_lifecycle-2",
		AccountID:      shared.AccountID("acc-trial"),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          moneyJPY(1000),
		BasePrice:      moneyJPY(1000),
	}, emptyMetadata())
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	assertStatus(t, agg, contract.ContractStatusDraft)

	// Start trial → trialing
	trialEnd := clock.Now().Add(14 * 24 * time.Hour)
	err = agg.StartTrial(contract.TrialConfiguration{
		TrialEndDate:         trialEnd,
		AutoConvert:          true,
		RequirePaymentMethod: false,
	}, emptyMetadata())
	if err != nil {
		t.Fatalf("StartTrial failed: %v", err)
	}
	assertStatus(t, agg, contract.ContractStatusTrialing)

	// End trial with conversion → active
	err = agg.EndTrial(true, emptyMetadata())
	if err != nil {
		t.Fatalf("EndTrial failed: %v", err)
	}
	assertStatus(t, agg, contract.ContractStatusActive)
}

func TestTrialLifecycleNotConverted(t *testing.T) {
	clock := fixedClock()

	contractID := shared.NewContractID()
	agg := contract.NewContractAggregate(contractID, clock)

	err := agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-integration-contract_lifecycle-3",
		AccountID:      shared.AccountID("acc-trial-no"),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          moneyJPY(1000),
		BasePrice:      moneyJPY(1000),
	}, emptyMetadata())
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	trialEnd := clock.Now().Add(14 * 24 * time.Hour)
	err = agg.StartTrial(contract.TrialConfiguration{
		TrialEndDate: trialEnd,
		AutoConvert:  false,
	}, emptyMetadata())
	if err != nil {
		t.Fatalf("StartTrial failed: %v", err)
	}

	// End trial without conversion → cancelled
	err = agg.EndTrial(false, emptyMetadata())
	if err != nil {
		t.Fatalf("EndTrial failed: %v", err)
	}
	assertStatus(t, agg, contract.ContractStatusCancelled)
}

func TestEventReplayReconstructsState(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)

	contractID := shared.NewContractID()
	agg := contract.NewContractAggregate(contractID, clock)

	// Build up history: create → activate → suspend
	err := agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-integration-contract_lifecycle-4",
		AccountID:      shared.AccountID("acc-replay"),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          moneyJPY(7500),
		BasePrice:      moneyJPY(7500),
	}, emptyMetadata())
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	err = agg.Activate(emptyMetadata())
	if err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	err = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingDefer,
		Reason:          "testing replay",
	}, emptyMetadata())
	if err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}

	// Save to persist events
	err = contractRepo.Save(ctx, agg)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Reload events from event store and replay into a fresh aggregate
	events, err := eventStore.Load(ctx, string(contractID))
	if err != nil {
		t.Fatalf("Load events failed: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	replayed := contract.NewContractAggregate(contractID, clock)
	err = replayed.LoadFromHistory(events)
	if err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	// Verify replayed state matches
	assertStatus(t, replayed, contract.ContractStatusSuspended)
	if replayed.AccountID() != shared.AccountID("acc-replay") {
		t.Errorf("accountID: got %s, want acc-replay", replayed.AccountID())
	}
	assertMoneyEquals(t, "price", replayed.Price(), 7500)
	if replayed.Version() != 3 {
		t.Errorf("version: got %d, want 3", replayed.Version())
	}
}

// --- Invalid state transition tests ---

func TestInvalidStateTransitions(t *testing.T) {
	clock := fixedClock()

	t.Run("cannot activate cancelled contract", func(t *testing.T) {
		agg := contract.NewContractAggregate(shared.NewContractID(), clock)
		_ = agg.Create(contract.CreateContractCommand{
			IdempotencyKey: "idem-integration-contract_lifecycle-5",
			AccountID:      shared.AccountID("acc-x"),
			ContractType:   contract.ContractTypeSubscription,
			Interval:       pricing.Monthly(),
			Price:          moneyJPY(1000),
			BasePrice:      moneyJPY(1000),
		}, emptyMetadata())
		_ = agg.Activate(emptyMetadata())
		_ = agg.Cancel("test", emptyMetadata())

		err := agg.Activate(emptyMetadata())
		if err == nil {
			t.Error("expected error activating cancelled contract")
		}
	})

	t.Run("cannot suspend draft contract", func(t *testing.T) {
		agg := contract.NewContractAggregate(shared.NewContractID(), clock)
		_ = agg.Create(contract.CreateContractCommand{
			IdempotencyKey: "idem-integration-contract_lifecycle-6",
			AccountID:      shared.AccountID("acc-x"),
			ContractType:   contract.ContractTypeSubscription,
			Interval:       pricing.Monthly(),
			Price:          moneyJPY(1000),
			BasePrice:      moneyJPY(1000),
		}, emptyMetadata())

		err := agg.Suspend(contract.SuspensionConfiguration{
			BillingBehavior: contract.SuspensionBillingSkip,
			Reason:          "test",
		}, emptyMetadata())
		if err == nil {
			t.Error("expected error suspending draft contract")
		}
	})

	t.Run("cannot resume active contract", func(t *testing.T) {
		agg := contract.NewContractAggregate(shared.NewContractID(), clock)
		_ = agg.Create(contract.CreateContractCommand{
			IdempotencyKey: "idem-integration-contract_lifecycle-7",
			AccountID:      shared.AccountID("acc-x"),
			ContractType:   contract.ContractTypeSubscription,
			Interval:       pricing.Monthly(),
			Price:          moneyJPY(1000),
			BasePrice:      moneyJPY(1000),
		}, emptyMetadata())
		_ = agg.Activate(emptyMetadata())

		err := agg.Resume(emptyMetadata())
		if err == nil {
			t.Error("expected error resuming active contract")
		}
	})
}

// assertStatus verifies the contract aggregate status.
func assertStatus(t *testing.T, agg *contract.ContractAggregate, want contract.ContractStatus) {
	t.Helper()
	if agg.Status() != want {
		t.Errorf("status: got %s, want %s", agg.Status(), want)
	}
}

// Ensure eventstore import is used (events loaded in TestEventReplayReconstructsState).
var _ eventstore.Event
