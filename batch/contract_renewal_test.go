package batch

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// mockPriceRepo is a simple mock for pricing.PriceRepository.
type mockPriceRepo struct {
	prices map[shared.PriceID]*pricing.Price
}

func (m *mockPriceRepo) FindByID(_ context.Context, id shared.PriceID) (*pricing.Price, error) {
	p, ok := m.prices[id]
	if !ok {
		return nil, fmt.Errorf("price %s not found", id)
	}
	return p, nil
}

func (m *mockPriceRepo) FindByProductID(_ context.Context, _ shared.ProductID) ([]*pricing.Price, error) {
	return nil, nil
}

func (m *mockPriceRepo) FindActiveByProductID(_ context.Context, _ shared.ProductID) ([]*pricing.Price, error) {
	return nil, nil
}

func (m *mockPriceRepo) Save(_ context.Context, _ *pricing.Price) error {
	return nil
}

// mockRenewalRepo is a simple in-memory mock that satisfies contract.Repository.
type mockRenewalRepo struct {
	mu        sync.Mutex
	contracts []*contract.ContractAggregate
	saved     []*contract.ContractAggregate
}

func (m *mockRenewalRepo) Save(_ context.Context, agg *contract.ContractAggregate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved = append(m.saved, agg)
	return nil
}

func (m *mockRenewalRepo) FindByID(_ context.Context, _ shared.ContractID) (*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockRenewalRepo) FindByAccountID(_ context.Context, _ shared.AccountID) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockRenewalRepo) FindActiveByPlanID(_ context.Context, _ shared.PlanID) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockRenewalRepo) FindExpiring(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockRenewalRepo) FindTrialsEndingSoon(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockRenewalRepo) FindByIDAsOf(_ context.Context, _ shared.ContractID, _ time.Time) (*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockRenewalRepo) FindDueForRenewal(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return m.contracts, nil
}

// newActiveContract creates an active contract with an expired period.
// The contract's clock is set to Feb 1 so the period is [Feb1, Mar1].
func newActiveContract(id string) *contract.ContractAggregate {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID(id), clock)

	cmd := contract.CreateContractCommand{
		AccountID:    shared.AccountID("a1"),
		PlanID:       shared.PlanID("p1"),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AutoRenew:    true,
	}
	meta := eventstore.EventMetadata{UserID: "test"}
	if err := agg.Create(cmd, meta); err != nil {
		panic("failed to create contract: " + err.Error())
	}
	if err := agg.Activate(meta); err != nil {
		panic("failed to activate contract: " + err.Error())
	}
	return agg
}

// processorClock returns a clock set to Mar 2, which is after the period end (Mar 1).
func processorClock() shared.FixedClock {
	return shared.FixedClock{FixedTime: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)}
}

func TestContractRenewalProcessor_HappyPath(t *testing.T) {
	agg := newActiveContract("c1")
	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{agg}}

	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 1 {
		t.Errorf("Total: got %d, want 1", result.Total)
	}
	if result.Succeeded != 1 {
		t.Errorf("Succeeded: got %d, want 1", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("Failed: got %d, want 0", result.Failed)
	}
	if len(repo.saved) != 1 {
		t.Errorf("saved count: got %d, want 1", len(repo.saved))
	}
	// After renewal, the contract should still be active.
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("status: got %s, want active", agg.Status())
	}
}

func TestContractRenewalProcessor_DryRun(t *testing.T) {
	agg := newActiveContract("c1")
	oldPeriod := agg.CurrentPeriod()
	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{agg}}

	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 1 {
		t.Errorf("Total: got %d, want 1", result.Total)
	}
	if result.Succeeded != 1 {
		t.Errorf("Succeeded: got %d, want 1", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("Failed: got %d, want 0", result.Failed)
	}
	// Dry run should NOT save.
	if len(repo.saved) != 0 {
		t.Errorf("saved count: got %d, want 0 (dry run)", len(repo.saved))
	}
	// Period should not have changed.
	if agg.CurrentPeriod() != oldPeriod {
		t.Errorf("period changed during dry run: got %v, want %v", agg.CurrentPeriod(), oldPeriod)
	}
}

func TestContractRenewalProcessor_DryRun_CancelAtPeriodEnd(t *testing.T) {
	agg := newActiveContract("c1")
	meta := eventstore.EventMetadata{UserID: "test"}
	if err := agg.ScheduleCancellation("customer request", meta); err != nil {
		t.Fatalf("ScheduleCancellation failed: %v", err)
	}
	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{agg}}

	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Failed != 1 {
		t.Errorf("Failed: got %d, want 1", result.Failed)
	}
	if result.Succeeded != 0 {
		t.Errorf("Succeeded: got %d, want 0", result.Succeeded)
	}
	// Contract should still be active (dry run — no side effects).
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("status: got %s, want active (dry run should not mutate)", agg.Status())
	}
}

func TestContractRenewalProcessor_DryRun_AutoRenewFalse(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID("c1"), clock)
	cmd := contract.CreateContractCommand{
		AccountID:    shared.AccountID("a1"),
		PlanID:       shared.PlanID("p1"),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AutoRenew:    false,
	}
	meta := eventstore.EventMetadata{UserID: "test"}
	if err := agg.Create(cmd, meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{agg}}
	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Failed != 1 {
		t.Errorf("Failed: got %d, want 1", result.Failed)
	}
	if result.Succeeded != 0 {
		t.Errorf("Succeeded: got %d, want 0", result.Succeeded)
	}
}

func TestContractRenewalProcessor_NoContracts(t *testing.T) {
	repo := &mockRenewalRepo{contracts: nil}

	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 0 {
		t.Errorf("Total: got %d, want 0", result.Total)
	}
	if result.Succeeded != 0 {
		t.Errorf("Succeeded: got %d, want 0", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("Failed: got %d, want 0", result.Failed)
	}
}

func TestContractRenewalProcessor_StopOnError(t *testing.T) {
	// First contract: cancelled, so renewal will fail.
	cancelledClock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	cancelledAgg := contract.NewContractAggregate(shared.ContractID("c-fail"), cancelledClock)
	meta := eventstore.EventMetadata{UserID: "test"}
	cmd := contract.CreateContractCommand{
		AccountID:    shared.AccountID("a1"),
		PlanID:       shared.PlanID("p1"),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AutoRenew:    true,
	}
	if err := cancelledAgg.Create(cmd, meta); err != nil {
		t.Fatalf("failed to create cancelled contract: %v", err)
	}
	if err := cancelledAgg.Activate(meta); err != nil {
		t.Fatalf("failed to activate cancelled contract: %v", err)
	}
	if err := cancelledAgg.Cancel("test cancellation", meta); err != nil {
		t.Fatalf("failed to cancel contract: %v", err)
	}

	// Second contract: active (should succeed but won't be reached).
	successAgg := newActiveContract("c-ok")

	repo := &mockRenewalRepo{
		contracts: []*contract.ContractAggregate{cancelledAgg, successAgg},
	}

	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	// ContinueOnError=false (default): should stop after first failure.
	result, err := processor.Process(context.Background(), BatchOptions{ContinueOnError: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Failed != 1 {
		t.Errorf("Failed: got %d, want 1", result.Failed)
	}
	// Second contract should NOT have been processed.
	if result.Succeeded != 0 {
		t.Errorf("Succeeded: got %d, want 0 (should stop after first error)", result.Succeeded)
	}
	if len(repo.saved) != 0 {
		t.Errorf("saved count: got %d, want 0", len(repo.saved))
	}
}

func TestContractRenewalProcessor_Concurrent(t *testing.T) {
	// Create multiple active contracts.
	contracts := make([]*contract.ContractAggregate, 5)
	for i := 0; i < 5; i++ {
		contracts[i] = newActiveContract(fmt.Sprintf("c%d", i))
	}

	repo := &mockRenewalRepo{contracts: contracts}
	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{Concurrency: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 5 {
		t.Errorf("Total: got %d, want 5", result.Total)
	}
	if result.Succeeded != 5 {
		t.Errorf("Succeeded: got %d, want 5", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("Failed: got %d, want 0", result.Failed)
	}
	if len(repo.saved) != 5 {
		t.Errorf("saved count: got %d, want 5", len(repo.saved))
	}
}

func TestContractRenewalProcessor_ContinueOnError(t *testing.T) {
	// First contract: cancelled (not active), so renewal will fail.
	cancelledClock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	cancelledAgg := contract.NewContractAggregate(shared.ContractID("c-fail"), cancelledClock)
	meta := eventstore.EventMetadata{UserID: "test"}
	cmd := contract.CreateContractCommand{
		AccountID:    shared.AccountID("a1"),
		PlanID:       shared.PlanID("p1"),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AutoRenew:    true,
	}
	if err := cancelledAgg.Create(cmd, meta); err != nil {
		t.Fatalf("failed to create cancelled contract: %v", err)
	}
	if err := cancelledAgg.Activate(meta); err != nil {
		t.Fatalf("failed to activate cancelled contract: %v", err)
	}
	if err := cancelledAgg.Cancel("test cancellation", meta); err != nil {
		t.Fatalf("failed to cancel contract: %v", err)
	}

	// Second contract: active and due for renewal (should succeed).
	successAgg := newActiveContract("c-ok")

	repo := &mockRenewalRepo{
		contracts: []*contract.ContractAggregate{cancelledAgg, successAgg},
	}

	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{ContinueOnError: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 2 {
		t.Errorf("Total: got %d, want 2", result.Total)
	}
	if result.Succeeded != 1 {
		t.Errorf("Succeeded: got %d, want 1", result.Succeeded)
	}
	if result.Failed != 1 {
		t.Errorf("Failed: got %d, want 1", result.Failed)
	}
	if len(result.Errors) != 1 {
		t.Errorf("Errors count: got %d, want 1", len(result.Errors))
	}
}

func TestContractRenewalProcessor_BillingCycleChange(t *testing.T) {
	// Create a monthly contract with a pending yearly Price
	clock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID("c-cycle"), clock)
	meta := eventstore.EventMetadata{UserID: "test"}

	cmd := contract.CreateContractCommand{
		AccountID:    shared.AccountID("a1"),
		PlanID:       shared.PlanID("p1"),
		PriceID:      shared.PriceID("price-monthly"),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY),
		AutoRenew:    true,
	}
	if err := agg.Create(cmd, meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	// Schedule a change to yearly Price
	yearlyPriceID := shared.PriceID("price-yearly")
	if err := agg.ChangePrice(yearlyPriceID, contract.ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}

	// Create a yearly Price entity in mock repo
	yearlyPrice := pricing.NewPrice(
		shared.NewProductID(),
		shared.NewMoney(big.NewRat(30000, 1), shared.CurrencyJPY),
		shared.CurrencyJPY,
		pricing.BillingCycleYearly,
		nil,
		clock.Now(),
	)

	priceRepo := &mockPriceRepo{
		prices: map[shared.PriceID]*pricing.Price{
			yearlyPriceID: yearlyPrice,
		},
	}

	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{agg}}
	processor := NewContractRenewalProcessor(repo, priceRepo, nil, processorClock(), nil, nil)

	oldPeriod := agg.CurrentPeriod()
	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Succeeded != 1 {
		t.Errorf("Succeeded: got %d, want 1", result.Succeeded)
	}

	// billingCycle should be updated to yearly
	if agg.GetBillingCycle() != contract.BillingCycleYearly {
		t.Errorf("expected billingCycle yearly, got %s", agg.GetBillingCycle())
	}
	// Period should advance by 1 year
	expectedEnd := oldPeriod.End().AddDate(1, 0, 0)
	if agg.CurrentPeriod().End() != expectedEnd {
		t.Errorf("expected period end %v, got %v", expectedEnd, agg.CurrentPeriod().End())
	}
	// PriceID should be promoted
	if agg.PriceID() != yearlyPriceID {
		t.Errorf("expected priceID %s, got %s", yearlyPriceID, agg.PriceID())
	}
}

func TestContractRenewalProcessor_NilPriceRepo_FallsBack(t *testing.T) {
	// Without PriceRepository, processor should fall back to contract's billingCycle
	agg := newActiveContract("c-fallback")
	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{agg}}

	// nil priceRepo
	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Succeeded != 1 {
		t.Errorf("Succeeded: got %d, want 1", result.Succeeded)
	}
	if agg.GetBillingCycle() != contract.BillingCycleMonthly {
		t.Errorf("expected billingCycle monthly (fallback), got %s", agg.GetBillingCycle())
	}
}
