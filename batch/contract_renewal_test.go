package batch

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// renewalChangeSpy records OnContractChange invocations for the renewal batch.
type renewalChangeSpy struct {
	mu          sync.Mutex
	changeTypes []plugin.ContractChangeType
	events      []plugin.ContractChangeEvent
}

func (p *renewalChangeSpy) Name() string                                        { return "renewal-change-spy" }
func (p *renewalChangeSpy) Version() string                                     { return "1.0.0" }
func (p *renewalChangeSpy) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *renewalChangeSpy) Shutdown(_ context.Context) error                    { return nil }
func (p *renewalChangeSpy) Priority() int                                       { return 500 }

func (p *renewalChangeSpy) OnContractChange(_ *plugin.Context, event plugin.ContractChangeEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.changeTypes = append(p.changeTypes, event.ChangeType)
	p.events = append(p.events, event)
	return nil
}

// newExpiringContract creates an active contract with autoRenew=false so that a
// real renewal run expires it.
func newExpiringContract(id string) *contract.ContractAggregate {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID(id), clock)
	meta := eventstore.EventMetadata{UserID: "test"}
	cmd := contract.CreateContractCommand{
		IdempotencyKey: "idem-batch-contract_renewal-expire",
		AccountID:      shared.AccountID("a1"),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AutoRenew:      false,
	}
	if err := agg.Create(cmd, meta); err != nil {
		panic("failed to create contract: " + err.Error())
	}
	if err := agg.Activate(meta); err != nil {
		panic("failed to activate contract: " + err.Error())
	}
	return agg
}

// TestContractRenewalProcessor_Expired_FiresExpiredChange verifies that a
// contract reaching natural term end (autoRenew=false) fires an OnContractChange
// with ChangeType=Expired, distinct from a deliberate cancellation (issue #162 B3).
func TestContractRenewalProcessor_Expired_FiresExpiredChange(t *testing.T) {
	agg := newExpiringContract("c-expire")
	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{agg}}
	spy := &renewalChangeSpy{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("register spy: %v", err)
	}

	processor := NewContractRenewalProcessor(repo, nil, registry, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Succeeded != 1 {
		t.Errorf("Succeeded: got %d, want 1", result.Succeeded)
	}
	if agg.Status() != contract.ContractStatusExpired {
		t.Errorf("status: got %s, want expired", agg.Status())
	}
	if len(spy.changeTypes) != 1 || spy.changeTypes[0] != plugin.ContractChangeExpired {
		t.Errorf("OnContractChange type: got %v, want [expired]", spy.changeTypes)
	}
}

// TestContractRenewalProcessor_DryRun_DanglingPendingPrice verifies the dry run
// now validates interval resolution: a contract with a scheduled price change to
// a Price that cannot be loaded FAILS the dry run instead of passing it and then
// blowing up in production (issue #162 B2).
func TestContractRenewalProcessor_DryRun_DanglingPendingPrice(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID("c-dangling"), clock)
	meta := eventstore.EventMetadata{UserID: "test"}
	cmd := contract.CreateContractCommand{
		IdempotencyKey: "idem-batch-contract_renewal-dangling",
		AccountID:      shared.AccountID("a1"),
		PriceID:        shared.PriceID("price-monthly"),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY),
		AutoRenew:      true,
	}
	if err := agg.Create(cmd, meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	// Schedule an end-of-term change to a price that does NOT exist in the repo.
	if err := agg.ChangePrice(shared.PriceID("price-missing"), contract.ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}

	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{agg}}
	priceRepo := &mockPriceRepo{prices: map[shared.PriceID]*pricing.Price{}} // empty — pending price missing
	processor := NewContractRenewalProcessor(repo, priceRepo, nil, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Failed != 1 {
		t.Errorf("Failed: got %d, want 1 (dangling pending price must fail dry run)", result.Failed)
	}
	if result.Succeeded != 0 {
		t.Errorf("Succeeded: got %d, want 0", result.Succeeded)
	}
}

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

func (m *mockRenewalRepo) FindByID(_ context.Context, id shared.ContractID) (*contract.ContractAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, agg := range m.contracts {
		if agg.ContractID() == id {
			return agg, nil
		}
	}
	return nil, nil
}

func (m *mockRenewalRepo) FindByAccountID(_ context.Context, _ shared.AccountID) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockRenewalRepo) FindExpiring(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockRenewalRepo) FindTrialsEndingBefore(_ context.Context, _ time.Time, _ int) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockRenewalRepo) FindByIDAsOf(_ context.Context, _ shared.ContractID, _ time.Time) (*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockRenewalRepo) FindDueForRenewal(_ context.Context, _ time.Time, _ int) ([]*contract.ContractAggregate, error) {
	return m.contracts, nil
}

// newActiveContract creates an active contract with an expired period.
// The contract's clock is set to Feb 1 so the period is [Feb1, Mar1].
func newActiveContract(id string) *contract.ContractAggregate {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID(id), clock)

	cmd := contract.CreateContractCommand{
		IdempotencyKey: "idem-batch-contract_renewal-1",
		AccountID:      shared.AccountID("a1"),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AutoRenew:      true,
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
	// The dry run reports the action the real run would take.
	if len(result.DryRunActions) != 1 ||
		result.DryRunActions[0].ItemID != "c1" ||
		result.DryRunActions[0].Action != RenewalActionRenew {
		t.Errorf("DryRunActions: got %+v, want [{c1 renew}]", result.DryRunActions)
	}
}

// TestContractRenewalProcessor_DryRun_CancelAtPeriodEnd verifies the dry run
// mirrors the real run (issue #242): a cancelAtPeriodEnd contract is processed
// SUCCESSFULLY by a real run (RenewWithInterval resolves it to Cancelled), so
// the dry run must classify it as would-succeed with action "cancel", not as a
// failure.
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
	if result.Succeeded != 1 {
		t.Errorf("Succeeded: got %d, want 1 (real run cancels this contract successfully)", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("Failed: got %d, want 0", result.Failed)
	}
	if len(result.DryRunActions) != 1 ||
		result.DryRunActions[0].ItemID != "c1" ||
		result.DryRunActions[0].Action != RenewalActionCancel {
		t.Errorf("DryRunActions: got %+v, want [{c1 cancel}]", result.DryRunActions)
	}
	// Contract should still be active (dry run — no side effects).
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("status: got %s, want active (dry run should not mutate)", agg.Status())
	}
	if len(repo.saved) != 0 {
		t.Errorf("saved count: got %d, want 0 (dry run)", len(repo.saved))
	}
}

func TestContractRenewalProcessor_DryRun_AutoRenewFalse(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID("c1"), clock)
	cmd := contract.CreateContractCommand{
		IdempotencyKey: "idem-batch-contract_renewal-2",
		AccountID:      shared.AccountID("a1"),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AutoRenew:      false,
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
	// Mirror the real run (issue #242): an autoRenew=false contract is
	// processed successfully by a real run (it expires), so the dry run
	// classifies it as would-succeed with action "expire".
	if result.Succeeded != 1 {
		t.Errorf("Succeeded: got %d, want 1 (real run expires this contract successfully)", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("Failed: got %d, want 0", result.Failed)
	}
	if len(result.DryRunActions) != 1 ||
		result.DryRunActions[0].ItemID != "c1" ||
		result.DryRunActions[0].Action != RenewalActionExpire {
		t.Errorf("DryRunActions: got %+v, want [{c1 expire}]", result.DryRunActions)
	}
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("status: got %s, want active (dry run should not mutate)", agg.Status())
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
		IdempotencyKey: "idem-batch-contract_renewal-3",
		AccountID:      shared.AccountID("a1"),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AutoRenew:      true,
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
	// The unattempted contract is accounted as skipped, keeping
	// Total == Succeeded + Failed + Skipped coherent (issue #242).
	if result.Skipped != 1 {
		t.Errorf("Skipped: got %d, want 1", result.Skipped)
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
		IdempotencyKey: "idem-batch-contract_renewal-4",
		AccountID:      shared.AccountID("a1"),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AutoRenew:      true,
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
		IdempotencyKey: "idem-batch-contract_renewal-5",
		AccountID:      shared.AccountID("a1"),
		PriceID:        shared.PriceID("price-monthly"),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY),
		AutoRenew:      true,
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
	yearlyPrice, yearlyPriceErr := pricing.NewPrice(
		shared.NewProductID(),
		shared.NewMoney(big.NewRat(30000, 1), shared.CurrencyJPY),
		shared.CurrencyJPY,
		pricing.BillingCycleYearly,
		nil,
		clock.Now(),
	)
	if yearlyPriceErr != nil {
		t.Fatalf("failed to create yearly price: %v", yearlyPriceErr)
	}

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

	// interval should be updated to yearly
	if !agg.GetInterval().Equals(pricing.Yearly()) {
		t.Errorf("expected interval yearly, got %s", agg.GetInterval())
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

// TestContractRenewalProcessor_Renewal_PriceChange_PopulatesPriceFields
// verifies that a renewal which applies a pending price change reports the
// price transition on the emitted ContractChangeEvent (issue #242):
// OldPriceID/NewPriceID are populated; MRRChange stays nil because the
// processor does not load price amounts.
func TestContractRenewalProcessor_Renewal_PriceChange_PopulatesPriceFields(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID("c-price-change"), clock)
	meta := eventstore.EventMetadata{UserID: "test"}

	oldPriceID := shared.PriceID("price-monthly")
	cmd := contract.CreateContractCommand{
		IdempotencyKey: "idem-batch-contract_renewal-6",
		AccountID:      shared.AccountID("a1"),
		PriceID:        oldPriceID,
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY),
		AutoRenew:      true,
	}
	if err := agg.Create(cmd, meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	newPriceID := shared.PriceID("price-yearly")
	if err := agg.ChangePrice(newPriceID, contract.ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}

	yearlyPrice, yearlyPriceErr := pricing.NewPrice(
		shared.NewProductID(),
		shared.NewMoney(big.NewRat(30000, 1), shared.CurrencyJPY),
		shared.CurrencyJPY,
		pricing.BillingCycleYearly,
		nil,
		clock.Now(),
	)
	if yearlyPriceErr != nil {
		t.Fatalf("failed to create yearly price: %v", yearlyPriceErr)
	}
	priceRepo := &mockPriceRepo{
		prices: map[shared.PriceID]*pricing.Price{newPriceID: yearlyPrice},
	}

	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{agg}}
	spy := &renewalChangeSpy{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("register spy: %v", err)
	}

	processor := NewContractRenewalProcessor(repo, priceRepo, registry, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Succeeded != 1 {
		t.Fatalf("Succeeded: got %d, want 1", result.Succeeded)
	}

	if len(spy.events) != 1 {
		t.Fatalf("OnContractChange events: got %d, want 1", len(spy.events))
	}
	event := spy.events[0]
	if event.ChangeType != plugin.ContractChangeRenewed {
		t.Errorf("ChangeType: got %s, want renewed", event.ChangeType)
	}
	if event.OldPriceID == nil || *event.OldPriceID != oldPriceID {
		t.Errorf("OldPriceID: got %v, want %s", event.OldPriceID, oldPriceID)
	}
	if event.NewPriceID == nil || *event.NewPriceID != newPriceID {
		t.Errorf("NewPriceID: got %v, want %s", event.NewPriceID, newPriceID)
	}
	if event.MRRChange != nil {
		t.Errorf("MRRChange: got %v, want nil (price amounts are not loaded by the processor)", event.MRRChange)
	}
}

// TestContractRenewalProcessor_PlainRenewal_PriceFieldsNil verifies that a
// renewal without a pending price change leaves OldPriceID/NewPriceID nil on
// the emitted ContractChangeEvent (issue #242).
func TestContractRenewalProcessor_PlainRenewal_PriceFieldsNil(t *testing.T) {
	agg := newActiveContract("c-plain")
	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{agg}}
	spy := &renewalChangeSpy{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("register spy: %v", err)
	}

	processor := NewContractRenewalProcessor(repo, nil, registry, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Succeeded != 1 {
		t.Fatalf("Succeeded: got %d, want 1", result.Succeeded)
	}

	if len(spy.events) != 1 {
		t.Fatalf("OnContractChange events: got %d, want 1", len(spy.events))
	}
	event := spy.events[0]
	if event.ChangeType != plugin.ContractChangeRenewed {
		t.Errorf("ChangeType: got %s, want renewed", event.ChangeType)
	}
	if event.OldPriceID != nil || event.NewPriceID != nil {
		t.Errorf("price fields: got Old=%v New=%v, want both nil on a plain renewal",
			event.OldPriceID, event.NewPriceID)
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
	if !agg.GetInterval().Equals(pricing.Monthly()) {
		t.Errorf("expected interval monthly (fallback), got %s", agg.GetInterval())
	}
}

// --- Zero-interval one_time contracts (issue #218) ---

// newZeroIntervalOneTimeContract creates an ACTIVE one_time contract with no
// billing interval (issue #218). Its currentPeriod stays unset, so a compliant
// FindDueForRenewal never selects it. AutoRenew is set so a force-fed renewal
// would reach the interval logic rather than the expire branch.
func newZeroIntervalOneTimeContract(id string) *contract.ContractAggregate {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID(id), clock)
	meta := eventstore.EventMetadata{UserID: "test"}
	cmd := contract.CreateContractCommand{
		IdempotencyKey: "idem-batch-contract_renewal-onetime-218",
		AccountID:      shared.AccountID("a1"),
		ContractType:   contract.ContractTypeOneTime,
		// Interval intentionally unset (zero) — allowed for one_time (#218).
		Price:     shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY),
		BasePrice: shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY),
		AutoRenew: true,
	}
	if err := agg.Create(cmd, meta); err != nil {
		panic("failed to create contract: " + err.Error())
	}
	if err := agg.Activate(meta); err != nil {
		panic("failed to activate contract: " + err.Error())
	}
	return agg
}

// TestContractRenewalProcessor_ZeroIntervalOneTime_NotSelectedByInMemoryRepo
// verifies the primary line of defense: the in-memory FindDueForRenewal
// excludes a zero-interval one_time contract (its period end is the zero
// time), so the renewal batch never sees it.
func TestContractRenewalProcessor_ZeroIntervalOneTime_NotSelectedByInMemoryRepo(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	store := inmemory.NewInMemoryEventStore(clock)
	repo := inmemory.NewInMemoryContractRepository(store, clock)
	ctx := context.Background()

	agg := newZeroIntervalOneTimeContract("c-onetime-218")
	if err := repo.Save(ctx, agg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)
	result, err := processor.Process(ctx, BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("Total: got %d, want 0 (zero-interval one_time must not be selected)", result.Total)
	}
	if result.Skipped != 0 || result.Failed != 0 {
		t.Errorf("Skipped/Failed: got %d/%d, want 0/0", result.Skipped, result.Failed)
	}
}

// TestContractRenewalProcessor_ZeroIntervalOneTime_SkippedWhenForceFed is the
// defense-in-depth half of issue #218: a custom DB adapter that does NOT
// replicate the zero-period-end guard may still return the contract from
// FindDueForRenewal. The processor must skip it (Warn log, Skipped counter)
// rather than fail it with a confusing renewal error.
func TestContractRenewalProcessor_ZeroIntervalOneTime_SkippedWhenForceFed(t *testing.T) {
	agg := newZeroIntervalOneTimeContract("c-onetime-forcefed")
	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{agg}}

	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)
	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("Total: got %d, want 1", result.Total)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped: got %d, want 1", result.Skipped)
	}
	if result.Succeeded != 0 || result.Failed != 0 {
		t.Errorf("Succeeded/Failed: got %d/%d, want 0/0", result.Succeeded, result.Failed)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors: got %v, want none", result.Errors)
	}
	// The contract must be untouched: still active, no renewal side effects.
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("status: got %s, want active", agg.Status())
	}
	if len(repo.saved) != 0 {
		t.Errorf("expected no Save calls for skipped contract, got %d", len(repo.saved))
	}
}

// TestContractRenewalProcessor_ZeroIntervalOneTime_SkippedConcurrent covers the
// concurrent dispatch path: a force-fed zero-interval contract is skipped while
// a normal renewable contract in the same batch still succeeds.
func TestContractRenewalProcessor_ZeroIntervalOneTime_SkippedConcurrent(t *testing.T) {
	zero := newZeroIntervalOneTimeContract("c-onetime-conc")
	renewable := newActiveContract("c-renewable-conc")
	repo := &mockRenewalRepo{contracts: []*contract.ContractAggregate{zero, renewable}}

	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)
	result, err := processor.Process(context.Background(), BatchOptions{Concurrency: 2, ContinueOnError: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped: got %d, want 1", result.Skipped)
	}
	if result.Succeeded != 1 {
		t.Errorf("Succeeded: got %d, want 1", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("Failed: got %d, want 0", result.Failed)
	}
}

// cancellingRenewalRepo cancels the parent context on the first FindByID call
// (i.e. while the first item is being processed), simulating an external
// caller cancellation mid-run.
type cancellingRenewalRepo struct {
	mockRenewalRepo
	cancel context.CancelFunc
	once   sync.Once
}

func (r *cancellingRenewalRepo) FindByID(ctx context.Context, id shared.ContractID) (*contract.ContractAggregate, error) {
	r.once.Do(r.cancel)
	return r.mockRenewalRepo.FindByID(ctx, id)
}

// TestContractRenewalProcessor_ParentCancellation_Sequential_NotCleanRun is
// the issue #242 review regression: an EXTERNAL context cancellation must not
// be misclassified as a benign partial run. With ContinueOnError=true the
// internal early-stop cancel is never invoked, so a parent-ctx cancellation
// must surface as a non-nil error from Process with the unprocessed remainder
// accounted as Skipped.
func TestContractRenewalProcessor_ParentCancellation_Sequential_NotCleanRun(t *testing.T) {
	agg1 := newActiveContract("c-cancel-1")
	agg2 := newActiveContract("c-cancel-2")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &cancellingRenewalRepo{
		mockRenewalRepo: mockRenewalRepo{contracts: []*contract.ContractAggregate{agg1, agg2}},
		cancel:          cancel,
	}
	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	result, err := processor.Process(ctx, BatchOptions{ContinueOnError: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Process error: got %v, want context.Canceled (cancelled run must not look clean)", err)
	}
	// The first item completes (the mock repo ignores ctx); the remainder is
	// skipped, keeping Total == Succeeded+Failed+Skipped.
	if result.Total != 2 || result.Succeeded != 1 || result.Failed != 0 || result.Skipped != 1 {
		t.Errorf("result: got Total=%d Succeeded=%d Failed=%d Skipped=%d, want Total=2 Succeeded=1 Failed=0 Skipped=1",
			result.Total, result.Succeeded, result.Failed, result.Skipped)
	}
}

// blockingRenewalRepo blocks FindByID until the context is cancelled, so a
// concurrent run's in-flight items observe the external cancellation.
type blockingRenewalRepo struct {
	mockRenewalRepo
}

func (r *blockingRenewalRepo) FindByID(ctx context.Context, _ shared.ContractID) (*contract.ContractAggregate, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestContractRenewalProcessor_ParentCancellation_Concurrent_NotCleanRun
// verifies the concurrent path: with ContinueOnError=true (the internal
// early-stop flag never set), items aborted by an EXTERNAL cancellation are
// recorded as genuine failures with their errors, and Process returns the
// cancellation error — the result is never a clean partial run (issue #242).
func TestContractRenewalProcessor_ParentCancellation_Concurrent_NotCleanRun(t *testing.T) {
	contracts := make([]*contract.ContractAggregate, 4)
	for i := 0; i < 4; i++ {
		contracts[i] = newActiveContract(fmt.Sprintf("c-cc-%d", i))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &blockingRenewalRepo{
		mockRenewalRepo: mockRenewalRepo{contracts: contracts},
	}
	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	timer := time.AfterFunc(20*time.Millisecond, cancel)
	defer timer.Stop()

	result, err := processor.Process(ctx, BatchOptions{ContinueOnError: true, Concurrency: 2})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Process error: got %v, want context.Canceled (cancelled run must not look clean)", err)
	}
	if result.Succeeded != 0 {
		t.Errorf("Succeeded: got %d, want 0", result.Succeeded)
	}
	// In-flight items cancelled externally are genuine failures with recorded
	// errors (NOT silently skipped); items never launched are skipped.
	if result.Failed < 1 {
		t.Errorf("Failed: got %d, want >= 1 (external cancellation must not be masked as Skipped)", result.Failed)
	}
	if len(result.Errors) != result.Failed {
		t.Errorf("Errors count: got %d, want %d (one per failed item)", len(result.Errors), result.Failed)
	}
	if got := result.Succeeded + result.Failed + result.Skipped; got != result.Total {
		t.Errorf("accounting: Succeeded+Failed+Skipped=%d, want Total=%d", got, result.Total)
	}
}
