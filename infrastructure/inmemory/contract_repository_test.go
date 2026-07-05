package inmemory

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

func newTestContractAggregate(t *testing.T, clock shared.Clock, accountID shared.AccountID) *contract.ContractAggregate {
	t.Helper()
	contractID := shared.NewContractID()
	agg := contract.NewContractAggregate(contractID, clock)

	cmd := contract.CreateContractCommand{
		AccountID:    accountID,
		PriceID:      shared.NewPriceID(),
		ContractType: contract.ContractTypeSubscription,
		Interval:     pricing.Monthly(),
		Price:        shared.NewMoney(new(big.Rat).SetInt64(980), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(new(big.Rat).SetInt64(980), shared.CurrencyJPY),
		AutoRenew:    true,
	}
	metadata := eventstore.EventMetadata{UserID: "test-user"}
	if err := agg.Create(cmd, metadata); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	return agg
}

func TestInMemoryContractRepository_SaveAndFindByID(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	store := NewInMemoryEventStore(clock)
	repo := NewInMemoryContractRepository(store, clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	agg := newTestContractAggregate(t, clock, accountID)

	if err := repo.Save(ctx, agg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	found, err := repo.FindByID(ctx, agg.ContractID())
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if found.ContractID() != agg.ContractID() {
		t.Errorf("expected contractID %s, got %s", agg.ContractID(), found.ContractID())
	}
	if found.AccountID() != accountID {
		t.Errorf("expected accountID %s, got %s", accountID, found.AccountID())
	}
	if found.Status() != contract.ContractStatusDraft {
		t.Errorf("expected status draft, got %s", found.Status())
	}
}

func TestInMemoryContractRepository_FindByID_NotFound(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	store := NewInMemoryEventStore(clock)
	repo := NewInMemoryContractRepository(store, clock)
	ctx := context.Background()

	_, err := repo.FindByID(ctx, shared.ContractID("nonexistent"))
	if err == nil {
		t.Error("expected error for non-existent contract")
	}
}

func TestInMemoryContractRepository_FindByAccountID(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	store := NewInMemoryEventStore(clock)
	repo := NewInMemoryContractRepository(store, clock)
	ctx := context.Background()

	accountID1 := shared.NewAccountID()
	accountID2 := shared.NewAccountID()

	agg1 := newTestContractAggregate(t, clock, accountID1)
	agg2 := newTestContractAggregate(t, clock, accountID1)
	agg3 := newTestContractAggregate(t, clock, accountID2)

	for _, agg := range []*contract.ContractAggregate{agg1, agg2, agg3} {
		if err := repo.Save(ctx, agg); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	results, err := repo.FindByAccountID(ctx, accountID1)
	if err != nil {
		t.Fatalf("FindByAccountID failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 contracts for accountID1, got %d", len(results))
	}

	// Non-existent account returns empty.
	empty, err := repo.FindByAccountID(ctx, shared.NewAccountID())
	if err != nil {
		t.Fatalf("FindByAccountID failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 contracts for unknown account, got %d", len(empty))
	}
}

func TestInMemoryContractRepository_FindExpiring(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	store := NewInMemoryEventStore(clock)
	repo := NewInMemoryContractRepository(store, clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	metadata := eventstore.EventMetadata{UserID: "test-user"}

	// Create and activate a contract. Activation sets currentPeriod based on clock.
	agg := newTestContractAggregate(t, clock, accountID)
	if err := agg.Activate(metadata); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	if err := repo.Save(ctx, agg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// The period end should be approximately 2026-02-01 (monthly cycle from Jan 1).
	// Query for contracts expiring before 2026-03-01 should find it.
	before := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	results, err := repo.FindExpiring(ctx, before)
	if err != nil {
		t.Fatalf("FindExpiring failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 expiring contract, got %d", len(results))
	}

	// Query for contracts expiring before 2026-01-15 (before period end) should find none.
	tooEarly := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	none, err := repo.FindExpiring(ctx, tooEarly)
	if err != nil {
		t.Fatalf("FindExpiring failed: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 expiring contracts before period end, got %d", len(none))
	}
}

func TestInMemoryContractRepository_FindTrialsEndingSoon(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	store := NewInMemoryEventStore(clock)
	repo := NewInMemoryContractRepository(store, clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	metadata := eventstore.EventMetadata{UserID: "test-user"}

	agg := newTestContractAggregate(t, clock, accountID)
	trialConfig := contract.TrialConfiguration{
		TrialEndDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		AutoConvert:  true,
	}
	if err := agg.StartTrial(trialConfig, metadata); err != nil {
		t.Fatalf("StartTrial failed: %v", err)
	}
	if err := repo.Save(ctx, agg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Trial ends on Jan 15. Query before Jan 20 should find it.
	before := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	results, err := repo.FindTrialsEndingSoon(ctx, before)
	if err != nil {
		t.Fatalf("FindTrialsEndingSoon failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 trial ending soon, got %d", len(results))
	}

	// Query before Jan 10 should find none (trial ends after Jan 10).
	beforeEarly := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	none, err := repo.FindTrialsEndingSoon(ctx, beforeEarly)
	if err != nil {
		t.Fatalf("FindTrialsEndingSoon failed: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 trials ending before Jan 10, got %d", len(none))
	}
}

func TestInMemoryContractRepository_FindDueForRenewal(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	store := NewInMemoryEventStore(clock)
	repo := NewInMemoryContractRepository(store, clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	metadata := eventstore.EventMetadata{UserID: "test-user"}

	agg := newTestContractAggregate(t, clock, accountID)
	if err := agg.Activate(metadata); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	if err := repo.Save(ctx, agg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Monthly contract activated on Jan 1 has period ending around Feb 1.
	// Querying asOf Feb 1 should find it (period.End <= asOf).
	asOf := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	results, err := repo.FindDueForRenewal(ctx, asOf)
	if err != nil {
		t.Fatalf("FindDueForRenewal failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 contract due for renewal, got %d", len(results))
	}

	// Querying before period end should find none.
	tooEarly := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	none, err := repo.FindDueForRenewal(ctx, tooEarly)
	if err != nil {
		t.Fatalf("FindDueForRenewal failed: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 contracts due for renewal before period end, got %d", len(none))
	}
}

func TestInMemoryContractRepository_FindByIDAsOf(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	store := NewInMemoryEventStore(clock)
	repo := NewInMemoryContractRepository(store, clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	metadata := eventstore.EventMetadata{UserID: "test-user"}

	agg := newTestContractAggregate(t, clock, accountID)
	if err := repo.Save(ctx, agg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// At this point, the contract is in draft status.
	// Advance clock and activate.
	clock2 := shared.FixedClock{FixedTime: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)}
	agg2 := contract.NewContractAggregate(agg.ContractID(), clock2)

	// Reload from events up to now (all events).
	events, err := store.Load(ctx, string(agg.ContractID()))
	if err != nil {
		t.Fatalf("Load events failed: %v", err)
	}
	if err := agg2.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}

	// Activate and save.
	if err := agg2.Activate(metadata); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	// Need to save via store directly to preserve events with correct version.
	newEvents := agg2.UncommittedEvents()
	if err := store.Append(ctx, string(agg.ContractID()), newEvents, len(events)); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// FindByIDAsOf at Jan 5 (before activation) should return draft status.
	asOfBeforeActivation := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	historical, err := repo.FindByIDAsOf(ctx, agg.ContractID(), asOfBeforeActivation)
	if err != nil {
		t.Fatalf("FindByIDAsOf failed: %v", err)
	}
	if historical.Status() != contract.ContractStatusDraft {
		t.Errorf("expected draft status as of Jan 5, got %s", historical.Status())
	}

	// FindByIDAsOf at Jan 15 (after activation) should return active status.
	asOfAfterActivation := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	active, err := repo.FindByIDAsOf(ctx, agg.ContractID(), asOfAfterActivation)
	if err != nil {
		t.Fatalf("FindByIDAsOf failed: %v", err)
	}
	if active.Status() != contract.ContractStatusActive {
		t.Errorf("expected active status as of Jan 15, got %s", active.Status())
	}

	// FindByIDAsOf for non-existent contract.
	_, err = repo.FindByIDAsOf(ctx, shared.ContractID("nonexistent"), asOfAfterActivation)
	if err == nil {
		t.Error("expected error for non-existent contract")
	}
}
