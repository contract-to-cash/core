package service

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/credit"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/domain/usage"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// --- Mock implementations ---

type mockContractRepo struct {
	agg *contract.ContractAggregate
	err error
}

func (m *mockContractRepo) Save(_ context.Context, _ *contract.ContractAggregate) error { return nil }
func (m *mockContractRepo) FindByID(_ context.Context, _ shared.ContractID) (*contract.ContractAggregate, error) {
	return m.agg, m.err
}
func (m *mockContractRepo) FindByAccountID(_ context.Context, _ shared.AccountID) ([]*contract.ContractAggregate, error) {
	return nil, nil
}
func (m *mockContractRepo) FindActiveByPlanID(_ context.Context, _ shared.PlanID) ([]*contract.ContractAggregate, error) {
	return nil, nil
}
func (m *mockContractRepo) FindExpiring(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return nil, nil
}
func (m *mockContractRepo) FindTrialsEndingSoon(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return nil, nil
}
func (m *mockContractRepo) FindByIDAsOf(_ context.Context, _ shared.ContractID, _ time.Time) (*contract.ContractAggregate, error) {
	return nil, nil
}

type mockInvoiceRepo struct {
	saved              *invoice.Invoice
	existingByContract []*invoice.Invoice // returned by FindByContractID
	existingByStatus   []*invoice.Invoice // returned by FindByContractAndStatus
	existingByPeriod   []*invoice.Invoice // returned by FindByContractAndPeriod
}

func (m *mockInvoiceRepo) Save(_ context.Context, inv *invoice.Invoice) error {
	m.saved = inv
	return nil
}
func (m *mockInvoiceRepo) FindByID(_ context.Context, _ shared.InvoiceID) (*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepo) FindByContractID(_ context.Context, _ shared.ContractID) ([]*invoice.Invoice, error) {
	return m.existingByContract, nil
}
func (m *mockInvoiceRepo) FindByAccountID(_ context.Context, _ shared.AccountID) ([]*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepo) FindOverdue(_ context.Context) ([]*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepo) FindByStatus(_ context.Context, _ invoice.InvoiceStatus) ([]*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepo) FindByIDAsOf(_ context.Context, _ shared.InvoiceID, _ time.Time) (*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepo) FindByContractAndStatus(_ context.Context, _ shared.ContractID, _ invoice.InvoiceStatus) ([]*invoice.Invoice, error) {
	return m.existingByStatus, nil
}
func (m *mockInvoiceRepo) FindByContractAndPeriod(_ context.Context, _ shared.ContractID, _ shared.DateRange) ([]*invoice.Invoice, error) {
	return m.existingByPeriod, nil
}
func (m *mockInvoiceRepo) FindUnpaidByContract(_ context.Context, _ shared.ContractID) ([]*invoice.Invoice, error) {
	return nil, nil
}

type mockUsageRepo struct{}

func (m *mockUsageRepo) Record(_ context.Context, _ *usage.UsageRecord) error { return nil }
func (m *mockUsageRepo) GetSummary(_ context.Context, _ shared.ContractID, _ string, _ shared.DateRange) (*usage.UsageSummary, error) {
	return &usage.UsageSummary{TotalUsage: 100}, nil
}
func (m *mockUsageRepo) GetRecords(_ context.Context, _ shared.ContractID, _ string, _, _ time.Time) ([]*usage.UsageRecord, error) {
	return nil, nil
}

type mockCreditRepo struct {
	credits []*credit.CreditEntry
}

func (m *mockCreditRepo) Save(_ context.Context, _ *credit.CreditEntry) error { return nil }
func (m *mockCreditRepo) FindByID(_ context.Context, _ shared.CreditEntryID) (*credit.CreditEntry, error) {
	return nil, nil
}
func (m *mockCreditRepo) FindAvailable(_ context.Context, _ shared.AccountID, _ shared.Currency) ([]*credit.CreditEntry, error) {
	return m.credits, nil
}
func (m *mockCreditRepo) GetBalance(_ context.Context, _ shared.AccountID, _ shared.Currency) (shared.Money, error) {
	return shared.Zero(shared.CurrencyJPY), nil
}
func (m *mockCreditRepo) SaveApplication(_ context.Context, _ *credit.CreditApplication) error {
	return nil
}
func (m *mockCreditRepo) FindApplicationsByInvoice(_ context.Context, _ shared.InvoiceID) ([]*credit.CreditApplication, error) {
	return nil, nil
}
func (m *mockCreditRepo) SaveRefund(_ context.Context, _ *credit.CreditRefund) error { return nil }

type mockPlanRepo struct {
	plan *pricing.Plan
}

func (m *mockPlanRepo) FindByID(_ context.Context, _ shared.PlanID) (*pricing.Plan, error) {
	return m.plan, nil
}

// --- Helpers ---

func newTestClock() shared.FixedClock {
	return shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
}

func newTestContractAggregate(clock shared.Clock, contractType contract.ContractType, price shared.Money) *contract.ContractAggregate {
	cid := shared.NewContractID()
	agg := contract.NewContractAggregate(cid, clock)
	_ = agg.Create(contract.CreateContractCommand{
		AccountID:    shared.NewAccountID(),
		PlanID:       shared.NewPlanID(),
		ContractType: contractType,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        price,
		BasePrice:    price,
	}, eventstore.EventMetadata{UserID: "test"})
	_ = agg.Activate(eventstore.EventMetadata{UserID: "test"})
	return agg
}

func newBillingPeriod() shared.DateRange {
	r, _ := shared.NewDateRange(
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	)
	return r
}

func jpy(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

// --- Tests ---

func TestGenerateInvoice_SubscriptionBasic(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, price)
	invRepo := &mockInvoiceRepo{}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		nil, // no credit repo
		credit.CreditConfig{},
		&mockPlanRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice, got nil")
	}
	if inv.Subtotal().Amount().Cmp(price.Amount()) != 0 {
		t.Errorf("expected subtotal %v, got %v", price.Amount(), inv.Subtotal().Amount())
	}
	if inv.Status() != invoice.InvoiceStatusDraft {
		t.Errorf("expected status draft, got %s", inv.Status())
	}
	if invRepo.saved == nil {
		t.Error("expected invoice to be saved")
	}
}

func TestGenerateInvoice_DiscountCap(t *testing.T) {
	clock := newTestClock()
	price := jpy(5000)
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, price)
	invRepo := &mockInvoiceRepo{}

	// Register a discount hook that returns more than the subtotal
	registry := plugin.NewRegistry()
	_ = registry.Register(&overDiscountPlugin{discount: jpy(9999)})

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		nil,
		credit.CreditConfig{},
		&mockPlanRepo{},
		registry,
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Discount should be capped to subtotal (5000), so discountAmount = 5000
	if inv.DiscountAmount().Amount().Cmp(price.Amount()) != 0 {
		t.Errorf("expected discount capped to %v, got %v", price.Amount(), inv.DiscountAmount().Amount())
	}
}

func TestGenerateInvoice_CreditNilSafe(t *testing.T) {
	clock := newTestClock()
	price := jpy(3000)
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, price)

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		nil, // creditRepo is nil
		credit.CreditConfig{},
		&mockPlanRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Applied credit should be zero
	if !inv.AppliedCredit().IsZero() {
		t.Errorf("expected zero applied credit, got %v", inv.AppliedCredit().Amount())
	}
}

func TestGenerateInvoice_WithCredits(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, price)

	creditEntry := credit.NewCreditEntry(agg.AccountID(), jpy(3000), credit.CreditReasonGoodwill, clock.Now())
	creditRepo := &mockCreditRepo{credits: []*credit.CreditEntry{creditEntry}}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		creditRepo,
		credit.CreditConfig{},
		&mockPlanRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Applied credit should be 3000
	expectedCredit := new(big.Rat).SetInt64(3000)
	if inv.AppliedCredit().Amount().Cmp(expectedCredit) != 0 {
		t.Errorf("expected applied credit 3000, got %v", inv.AppliedCredit().Amount())
	}

	// Amount due should be 10000 - 3000 = 7000
	expectedDue := new(big.Rat).SetInt64(7000)
	if inv.AmountDue().Amount().Cmp(expectedDue) != 0 {
		t.Errorf("expected amount due 7000, got %v", inv.AmountDue().Amount())
	}
}

// --- Status guard tests ---

func newDraftContractAggregate(clock shared.Clock, price shared.Money) *contract.ContractAggregate {
	cid := shared.NewContractID()
	agg := contract.NewContractAggregate(cid, clock)
	_ = agg.Create(contract.CreateContractCommand{
		AccountID:    shared.NewAccountID(),
		PlanID:       shared.NewPlanID(),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        price,
		BasePrice:    price,
	}, eventstore.EventMetadata{UserID: "test"})
	return agg
}

func newBillingSvc(agg *contract.ContractAggregate, invRepo *mockInvoiceRepo, clock shared.Clock) *BillingService {
	if invRepo == nil {
		invRepo = &mockInvoiceRepo{}
	}
	return NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		nil,
		credit.CreditConfig{},
		&mockPlanRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)
}

func TestGenerateInvoice_StatusGuard_DraftAllowed(t *testing.T) {
	clock := newTestClock()
	agg := newDraftContractAggregate(clock, jpy(1000))
	svc := newBillingSvc(agg, nil, clock)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err != nil {
		t.Fatalf("draft contract should allow invoice generation: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice")
	}
}

func TestGenerateInvoice_StatusGuard_CancelledBlocked(t *testing.T) {
	clock := newTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	_ = agg.Cancel("test", eventstore.EventMetadata{UserID: "test"})
	svc := newBillingSvc(agg, nil, clock)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err == nil {
		t.Fatal("cancelled contract should block invoice generation")
	}
}

func TestGenerateInvoice_StatusGuard_SuspendedSkipBlocked(t *testing.T) {
	clock := newTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	_ = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingSkip,
		Reason:          "payment pending",
	}, eventstore.EventMetadata{UserID: "test"})
	svc := newBillingSvc(agg, nil, clock)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err == nil {
		t.Fatal("suspended (skip) contract should block invoice generation")
	}
}

func TestGenerateInvoice_StatusGuard_SuspendedDeferBlocked(t *testing.T) {
	clock := newTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	_ = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingDefer,
		Reason:          "payment pending",
	}, eventstore.EventMetadata{UserID: "test"})
	svc := newBillingSvc(agg, nil, clock)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err == nil {
		t.Fatal("suspended (defer) contract should block invoice generation")
	}
}

func TestGenerateInvoice_StatusGuard_SuspendedContinueAllowed(t *testing.T) {
	clock := newTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	_ = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingContinue,
		Reason:          "admin hold",
	}, eventstore.EventMetadata{UserID: "test"})
	svc := newBillingSvc(agg, nil, clock)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err != nil {
		t.Fatalf("suspended (continue) should allow invoice generation: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice")
	}
}

// --- Duplicate invoice prevention tests ---

func TestGenerateInvoice_DuplicateDraftBlocked(t *testing.T) {
	clock := newTestClock()
	agg := newDraftContractAggregate(clock, jpy(1000))

	existingDraft := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
	)
	invRepo := &mockInvoiceRepo{existingByStatus: []*invoice.Invoice{existingDraft}}
	svc := newBillingSvc(agg, invRepo, clock)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err == nil {
		t.Fatal("should block duplicate draft invoice")
	}
}

func TestGenerateInvoice_DuplicatePeriodBlocked(t *testing.T) {
	clock := newTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))

	existingInv := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithBillingPeriod(newBillingPeriod()),
	)
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{existingInv}}
	svc := newBillingSvc(agg, invRepo, clock)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err == nil {
		t.Fatal("should block duplicate invoice for same billing period")
	}
}

func TestGenerateInvoice_VoidedAllowsRegeneration(t *testing.T) {
	clock := newTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))

	voidedInv := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(newBillingPeriod()),
	)
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedInv}}
	svc := newBillingSvc(agg, invRepo, clock)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err != nil {
		t.Fatalf("voided invoice should allow regeneration: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice")
	}
}

func TestGenerateInvoice_OneTimeDuplicateBlocked(t *testing.T) {
	clock := newTestClock()
	cid := shared.NewContractID()
	agg := contract.NewContractAggregate(cid, clock)
	_ = agg.Create(contract.CreateContractCommand{
		AccountID:    shared.NewAccountID(),
		PlanID:       shared.NewPlanID(),
		ContractType: contract.ContractTypeOneTime,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        jpy(5000),
		BasePrice:    jpy(5000),
	}, eventstore.EventMetadata{UserID: "test"})
	_ = agg.Activate(eventstore.EventMetadata{UserID: "test"})

	existingInv := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(5000), jpy(0), jpy(0),
	)
	invRepo := &mockInvoiceRepo{existingByContract: []*invoice.Invoice{existingInv}}
	svc := newBillingSvc(agg, invRepo, clock)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err == nil {
		t.Fatal("should block duplicate invoice for one-time contract")
	}
}

// --- Mock discount plugin ---

type overDiscountPlugin struct {
	discount shared.Money
}

func (p *overDiscountPlugin) Name() string                                        { return "over_discount" }
func (p *overDiscountPlugin) Version() string                                     { return "1.0.0" }
func (p *overDiscountPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *overDiscountPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *overDiscountPlugin) Priority() int                                       { return 100 }
func (p *overDiscountPlugin) CalculateDiscount(_ *plugin.CalculationContext) (shared.Money, error) {
	return p.discount, nil
}
