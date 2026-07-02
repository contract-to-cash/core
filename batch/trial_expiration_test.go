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
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// mockTrialRepo is a simple in-memory mock that satisfies contract.Repository.
type mockTrialRepo struct {
	mu        sync.Mutex
	contracts []*contract.ContractAggregate
	saved     []*contract.ContractAggregate
}

func (m *mockTrialRepo) Save(_ context.Context, agg *contract.ContractAggregate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved = append(m.saved, agg)
	return nil
}

func (m *mockTrialRepo) FindByID(_ context.Context, _ shared.ContractID) (*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockTrialRepo) FindByAccountID(_ context.Context, _ shared.AccountID) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockTrialRepo) FindExpiring(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockTrialRepo) FindTrialsEndingSoon(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return m.contracts, nil
}

func (m *mockTrialRepo) FindByIDAsOf(_ context.Context, _ shared.ContractID, _ time.Time) (*contract.ContractAggregate, error) {
	return nil, nil
}

func (m *mockTrialRepo) FindDueForRenewal(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

// trialEndSpyPlugin records OnContractTrialEnd and OnContractChange invocations.
type trialEndSpyPlugin struct {
	mu               sync.Mutex
	trialEndCalls    int
	trialEndConv     []bool
	changeCalls      int
	changeTypes      []plugin.ContractChangeType
	trialEndErr      error // if set, OnContractTrialEnd returns this error
	onContractChange error // if set, OnContractChange returns this error
}

func (p *trialEndSpyPlugin) Name() string                                        { return "trial-end-spy" }
func (p *trialEndSpyPlugin) Version() string                                     { return "1.0.0" }
func (p *trialEndSpyPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *trialEndSpyPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *trialEndSpyPlugin) Priority() int                                       { return 500 }

func (p *trialEndSpyPlugin) OnContractTrialEnd(_ *plugin.Context, _ *contract.ContractAggregate, converted bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.trialEndCalls++
	p.trialEndConv = append(p.trialEndConv, converted)
	return p.trialEndErr
}

func (p *trialEndSpyPlugin) OnContractChange(_ *plugin.Context, event plugin.ContractChangeEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.changeCalls++
	p.changeTypes = append(p.changeTypes, event.ChangeType)
	return p.onContractChange
}

// newTrialingContract creates a trialing contract whose trial ends on Jan 15.
func newTrialingContract(id string, autoConvert bool) *contract.ContractAggregate {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID(id), clock)

	cmd := contract.CreateContractCommand{
		AccountID:    shared.AccountID("a1"),
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
	if err := agg.StartTrial(contract.TrialConfiguration{
		TrialEndDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		AutoConvert:  autoConvert,
	}, meta); err != nil {
		panic("failed to start trial: " + err.Error())
	}
	return agg
}

// trialProcessorClock returns a clock set to Jan 20, after the trial end (Jan 15).
func trialProcessorClock() shared.FixedClock {
	return shared.FixedClock{FixedTime: time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)}
}

func TestTrialExpirationProcessor_AutoConvert_BecomesActive(t *testing.T) {
	agg := newTrialingContract("c1", true)
	repo := &mockTrialRepo{contracts: []*contract.ContractAggregate{agg}}
	spy := &trialEndSpyPlugin{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("failed to register spy plugin: %v", err)
	}

	processor := NewTrialExpirationProcessor(repo, registry, trialProcessorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Errorf("result: got %+v, want Total=1 Succeeded=1 Failed=0", result)
	}
	if len(repo.saved) != 1 {
		t.Errorf("saved count: got %d, want 1", len(repo.saved))
	}
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("status: got %s, want active (AutoConvert=true)", agg.Status())
	}
	if spy.trialEndCalls != 1 {
		t.Errorf("OnContractTrialEnd calls: got %d, want 1", spy.trialEndCalls)
	}
	if len(spy.trialEndConv) != 1 || !spy.trialEndConv[0] {
		t.Errorf("OnContractTrialEnd converted: got %v, want [true]", spy.trialEndConv)
	}
	if spy.changeCalls != 1 {
		t.Errorf("OnContractChange calls: got %d, want 1", spy.changeCalls)
	}
	if len(spy.changeTypes) != 1 || spy.changeTypes[0] != plugin.ContractChangeTrialEnd {
		t.Errorf("OnContractChange type: got %v, want [trial_end]", spy.changeTypes)
	}
}

func TestTrialExpirationProcessor_NoAutoConvert_BecomesCancelled(t *testing.T) {
	agg := newTrialingContract("c1", false)
	repo := &mockTrialRepo{contracts: []*contract.ContractAggregate{agg}}
	spy := &trialEndSpyPlugin{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("failed to register spy plugin: %v", err)
	}

	processor := NewTrialExpirationProcessor(repo, registry, trialProcessorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Succeeded != 1 {
		t.Errorf("Succeeded: got %d, want 1", result.Succeeded)
	}
	if agg.Status() != contract.ContractStatusCancelled {
		t.Errorf("status: got %s, want cancelled (AutoConvert=false)", agg.Status())
	}
	if len(spy.trialEndConv) != 1 || spy.trialEndConv[0] {
		t.Errorf("OnContractTrialEnd converted: got %v, want [false]", spy.trialEndConv)
	}
}

func TestTrialExpirationProcessor_DryRun(t *testing.T) {
	agg := newTrialingContract("c1", true)
	repo := &mockTrialRepo{contracts: []*contract.ContractAggregate{agg}}
	spy := &trialEndSpyPlugin{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("failed to register spy plugin: %v", err)
	}

	processor := NewTrialExpirationProcessor(repo, registry, trialProcessorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Succeeded != 1 {
		t.Errorf("Succeeded: got %d, want 1", result.Succeeded)
	}
	// Dry run: no save, no state change, no hooks.
	if len(repo.saved) != 0 {
		t.Errorf("saved count: got %d, want 0 (dry run)", len(repo.saved))
	}
	if agg.Status() != contract.ContractStatusTrialing {
		t.Errorf("status: got %s, want trialing (dry run should not mutate)", agg.Status())
	}
	if spy.trialEndCalls != 0 || spy.changeCalls != 0 {
		t.Errorf("hooks must not fire in dry run: trialEnd=%d change=%d", spy.trialEndCalls, spy.changeCalls)
	}
}

func TestTrialExpirationProcessor_TrialNotEndedYet_Fails(t *testing.T) {
	agg := newTrialingContract("c1", true)
	repo := &mockTrialRepo{contracts: []*contract.ContractAggregate{agg}}

	// Clock BEFORE the trial end date (Jan 15): guard should reject.
	earlyClock := shared.FixedClock{FixedTime: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)}
	processor := NewTrialExpirationProcessor(repo, nil, earlyClock, nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Failed != 1 {
		t.Errorf("Failed: got %d, want 1", result.Failed)
	}
	if agg.Status() != contract.ContractStatusTrialing {
		t.Errorf("status: got %s, want trialing", agg.Status())
	}
	if len(repo.saved) != 0 {
		t.Errorf("saved count: got %d, want 0", len(repo.saved))
	}
}

func TestTrialExpirationProcessor_NonTrialingContract_Fails(t *testing.T) {
	// A contract that is active (not trialing) must be rejected by the guard.
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID("c-active"), clock)
	meta := eventstore.EventMetadata{UserID: "test"}
	cmd := contract.CreateContractCommand{
		AccountID:    shared.AccountID("a1"),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
	}
	if err := agg.Create(cmd, meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := agg.Activate(meta); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	repo := &mockTrialRepo{contracts: []*contract.ContractAggregate{agg}}
	processor := NewTrialExpirationProcessor(repo, nil, trialProcessorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Failed != 1 {
		t.Errorf("Failed: got %d, want 1", result.Failed)
	}
	var domainErr *shared.DomainError
	if len(result.Errors) != 1 || !errors.As(result.Errors[0], &domainErr) ||
		domainErr.Code != shared.ErrCodeInvalidStateTransition {
		t.Errorf("expected invalid_state_transition error, got: %v", result.Errors)
	}
}

func TestTrialExpirationProcessor_HookErrorIsNonFatal(t *testing.T) {
	agg := newTrialingContract("c1", true)
	repo := &mockTrialRepo{contracts: []*contract.ContractAggregate{agg}}
	spy := &trialEndSpyPlugin{
		trialEndErr:      errors.New("notification backend down"),
		onContractChange: errors.New("metrics backend down"),
	}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("failed to register spy plugin: %v", err)
	}

	processor := NewTrialExpirationProcessor(repo, registry, trialProcessorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Contract is persisted before hooks fire; hook errors must not fail the batch item.
	if result.Succeeded != 1 || result.Failed != 0 {
		t.Errorf("result: got %+v, want Succeeded=1 Failed=0 (hook errors are non-fatal)", result)
	}
	if len(repo.saved) != 1 {
		t.Errorf("saved count: got %d, want 1", len(repo.saved))
	}
}

func TestTrialExpirationProcessor_NoContracts(t *testing.T) {
	repo := &mockTrialRepo{contracts: nil}
	processor := NewTrialExpirationProcessor(repo, nil, trialProcessorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 0 || result.Succeeded != 0 || result.Failed != 0 {
		t.Errorf("result: got %+v, want all zero", result)
	}
}

func TestTrialExpirationProcessor_ContinueOnError(t *testing.T) {
	// First contract: still within its trial (guard fails at Jan 20? No —
	// use a trial ending Feb 1 so the Jan 20 clock rejects it).
	early := newTrialingContractWithEnd("c-fail", true, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	// Second contract: expired trial (should succeed).
	expired := newTrialingContract("c-ok", true)

	repo := &mockTrialRepo{contracts: []*contract.ContractAggregate{early, expired}}
	processor := NewTrialExpirationProcessor(repo, nil, trialProcessorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{ContinueOnError: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 2 || result.Succeeded != 1 || result.Failed != 1 {
		t.Errorf("result: got %+v, want Total=2 Succeeded=1 Failed=1", result)
	}
	if expired.Status() != contract.ContractStatusActive {
		t.Errorf("expired trial status: got %s, want active", expired.Status())
	}
}

func TestTrialExpirationProcessor_Concurrent(t *testing.T) {
	contracts := make([]*contract.ContractAggregate, 5)
	for i := 0; i < 5; i++ {
		contracts[i] = newTrialingContract(fmt.Sprintf("c%d", i), true)
	}

	repo := &mockTrialRepo{contracts: contracts}
	processor := NewTrialExpirationProcessor(repo, nil, trialProcessorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{Concurrency: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 5 || result.Succeeded != 5 || result.Failed != 0 {
		t.Errorf("result: got %+v, want Total=5 Succeeded=5 Failed=0", result)
	}
	if len(repo.saved) != 5 {
		t.Errorf("saved count: got %d, want 5", len(repo.saved))
	}
}

// --- RequirePaymentMethod gate ---

// newTrialingContractRequiringPM creates a trialing contract with
// AutoConvert=true and RequirePaymentMethod=true. If paymentMethodID is
// non-empty it is registered on the contract.
func newTrialingContractRequiringPM(id string, paymentMethodID string) *contract.ContractAggregate {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID(id), clock)

	cmd := contract.CreateContractCommand{
		AccountID:    shared.AccountID("a1"),
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
	if err := agg.StartTrial(contract.TrialConfiguration{
		TrialEndDate:         time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		AutoConvert:          true,
		RequirePaymentMethod: true,
	}, meta); err != nil {
		panic("failed to start trial: " + err.Error())
	}
	if paymentMethodID != "" {
		if err := agg.ChangePaymentMethod(&paymentMethodID, meta); err != nil {
			panic("failed to change payment method: " + err.Error())
		}
	}
	return agg
}

// TestTrialExpirationProcessor_RequirePaymentMethod_BlocksConversion locks the
// design-decisions 2.1 behavior: RequirePaymentMethod=true with no registered
// payment method must NOT auto-convert. The contract stays Trialing, no hooks
// fire, and the batch records the contract as a failure.
func TestTrialExpirationProcessor_RequirePaymentMethod_BlocksConversion(t *testing.T) {
	agg := newTrialingContractRequiringPM("c-no-pm", "")
	repo := &mockTrialRepo{contracts: []*contract.ContractAggregate{agg}}
	spy := &trialEndSpyPlugin{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("failed to register spy plugin: %v", err)
	}

	processor := NewTrialExpirationProcessor(repo, registry, trialProcessorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{ContinueOnError: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Errorf("result: got %+v, want Total=1 Succeeded=0 Failed=1", result)
	}
	var domainErr *shared.DomainError
	if len(result.Errors) != 1 || !errors.As(result.Errors[0], &domainErr) ||
		domainErr.Code != shared.ErrCodeBusinessRule {
		t.Errorf("expected business_rule_violation error, got: %v", result.Errors)
	}
	if agg.Status() != contract.ContractStatusTrialing {
		t.Errorf("status: got %s, want trialing (conversion must be blocked)", agg.Status())
	}
	if len(repo.saved) != 0 {
		t.Errorf("saved count: got %d, want 0", len(repo.saved))
	}
	if spy.trialEndCalls != 0 || spy.changeCalls != 0 {
		t.Errorf("hooks must not fire when conversion is blocked: trialEnd=%d change=%d",
			spy.trialEndCalls, spy.changeCalls)
	}
}

// TestTrialExpirationProcessor_RequirePaymentMethod_DryRunDetects verifies the
// gate is also validated in dry-run mode so operators see the failure without
// side effects.
func TestTrialExpirationProcessor_RequirePaymentMethod_DryRunDetects(t *testing.T) {
	agg := newTrialingContractRequiringPM("c-no-pm", "")
	repo := &mockTrialRepo{contracts: []*contract.ContractAggregate{agg}}

	processor := NewTrialExpirationProcessor(repo, nil, trialProcessorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Failed != 1 {
		t.Errorf("Failed: got %d, want 1 (dry run must detect the missing payment method)", result.Failed)
	}
	if agg.Status() != contract.ContractStatusTrialing {
		t.Errorf("status: got %s, want trialing", agg.Status())
	}
	if len(repo.saved) != 0 {
		t.Errorf("saved count: got %d, want 0 (dry run)", len(repo.saved))
	}
}

// TestTrialExpirationProcessor_RequirePaymentMethod_WithPM_Converts verifies
// that a registered payment method satisfies the gate and conversion proceeds.
func TestTrialExpirationProcessor_RequirePaymentMethod_WithPM_Converts(t *testing.T) {
	agg := newTrialingContractRequiringPM("c-with-pm", "pm-visa-1234")
	repo := &mockTrialRepo{contracts: []*contract.ContractAggregate{agg}}
	spy := &trialEndSpyPlugin{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("failed to register spy plugin: %v", err)
	}

	processor := NewTrialExpirationProcessor(repo, registry, trialProcessorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Succeeded != 1 || result.Failed != 0 {
		t.Errorf("result: got %+v, want Succeeded=1 Failed=0", result)
	}
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("status: got %s, want active", agg.Status())
	}
	if spy.trialEndCalls != 1 {
		t.Errorf("OnContractTrialEnd calls: got %d, want 1", spy.trialEndCalls)
	}
}

// newTrialingContractWithEnd creates a trialing contract with a custom trial end date.
func newTrialingContractWithEnd(id string, autoConvert bool, trialEnd time.Time) *contract.ContractAggregate {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.ContractID(id), clock)

	cmd := contract.CreateContractCommand{
		AccountID:    shared.AccountID("a1"),
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
	if err := agg.StartTrial(contract.TrialConfiguration{
		TrialEndDate: trialEnd,
		AutoConvert:  autoConvert,
	}, meta); err != nil {
		panic("failed to start trial: " + err.Error())
	}
	return agg
}
