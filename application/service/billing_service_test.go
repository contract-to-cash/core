package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/product"
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
func (m *mockContractRepo) FindExpiring(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return nil, nil
}
func (m *mockContractRepo) FindTrialsEndingBefore(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return nil, nil
}
func (m *mockContractRepo) FindByIDAsOf(_ context.Context, _ shared.ContractID, _ time.Time) (*contract.ContractAggregate, error) {
	return nil, nil
}
func (m *mockContractRepo) FindDueForRenewal(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

type mockInvoiceRepo struct {
	saved              *invoice.Invoice
	byID               *invoice.Invoice   // returned by FindByID
	existingByContract []*invoice.Invoice // returned by FindByContractID
	existingByStatus   []*invoice.Invoice // returned by FindByContractAndStatus
	existingByPeriod   []*invoice.Invoice // returned by FindByContractAndPeriod
}

func (m *mockInvoiceRepo) Save(_ context.Context, inv *invoice.Invoice) error {
	m.saved = inv
	return nil
}
func (m *mockInvoiceRepo) FindByID(_ context.Context, _ shared.InvoiceID) (*invoice.Invoice, error) {
	return m.byID, nil
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
func (m *mockUsageRepo) GetSummary(_ context.Context, _ shared.ContractID, _ shared.MetricName, _ shared.DateRange) (*usage.UsageSummary, error) {
	return &usage.UsageSummary{TotalUsage: 100}, nil
}
func (m *mockUsageRepo) GetRecords(_ context.Context, _ shared.ContractID, _ shared.MetricName, _, _ time.Time) ([]*usage.UsageRecord, error) {
	return nil, nil
}

type mockBalanceRepo struct {
	credits []*balance.BalanceEntry
}

func (m *mockBalanceRepo) Save(_ context.Context, _ *balance.BalanceEntry) error { return nil }
func (m *mockBalanceRepo) FindByID(_ context.Context, _ shared.BalanceEntryID) (*balance.BalanceEntry, error) {
	return nil, nil
}
func (m *mockBalanceRepo) FindAvailable(_ context.Context, _ shared.AccountID, _ shared.Currency) ([]*balance.BalanceEntry, error) {
	return m.credits, nil
}
func (m *mockBalanceRepo) GetBalance(_ context.Context, _ shared.AccountID, _ shared.Currency) (shared.Money, error) {
	return shared.Zero(shared.CurrencyJPY), nil
}
func (m *mockBalanceRepo) SaveApplication(_ context.Context, _ *balance.BalanceApplication) error {
	return nil
}
func (m *mockBalanceRepo) FindApplicationsByInvoice(_ context.Context, _ shared.InvoiceID) ([]*balance.BalanceApplication, error) {
	return nil, nil
}
func (m *mockBalanceRepo) SaveRefund(_ context.Context, _ *balance.BalanceRefund) error { return nil }
func (m *mockBalanceRepo) FindRefundsByInvoice(_ context.Context, _ shared.InvoiceID) ([]*balance.BalanceRefund, error) {
	return nil, nil
}
func (m *mockBalanceRepo) FindExpired(_ context.Context, _ time.Time) ([]*balance.BalanceEntry, error) {
	return nil, nil
}
func (m *mockBalanceRepo) FindByAccountID(_ context.Context, _ shared.AccountID, _ shared.Currency) ([]*balance.BalanceEntry, error) {
	return nil, nil
}

type mockPriceRepo struct {
	price *pricing.Price
	err   error
}

func (m *mockPriceRepo) FindByID(_ context.Context, _ shared.PriceID) (*pricing.Price, error) {
	return m.price, m.err
}
func (m *mockPriceRepo) FindByProductID(_ context.Context, _ shared.ProductID) ([]*pricing.Price, error) {
	return nil, nil
}
func (m *mockPriceRepo) FindActiveByProductID(_ context.Context, _ shared.ProductID) ([]*pricing.Price, error) {
	return nil, nil
}
func (m *mockPriceRepo) Save(_ context.Context, _ *pricing.Price) error { return nil }

type mockProductRepo struct {
	product *product.Product
	err     error
}

func (m *mockProductRepo) FindByID(_ context.Context, _ shared.ProductID) (*product.Product, error) {
	return m.product, m.err
}
func (m *mockProductRepo) Save(_ context.Context, _ *product.Product) error { return nil }

// --- Helpers ---

func newTestClock() shared.FixedClock {
	return shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
}

func newTestPrice(productID shared.ProductID, amount shared.Money, pricingModel pricing.PricingModel) *pricing.Price {
	return pricing.NewPrice(productID, amount, amount.Currency(), pricing.BillingCycleMonthly, pricingModel, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
}

func newTestContractAggregate(clock shared.Clock, contractType contract.ContractType, price shared.Money) *contract.ContractAggregate {
	return newTestContractAggregateWithPriceID(clock, contractType, price, "")
}

func newTestContractAggregateWithPriceID(clock shared.Clock, contractType contract.ContractType, price shared.Money, priceID shared.PriceID) *contract.ContractAggregate {
	cid := shared.NewContractID()
	agg := contract.NewContractAggregate(cid, clock)
	_ = agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-service-billing_service-1",
		AccountID:      shared.NewAccountID(),
		PriceID:        priceID,
		ContractType:   contractType,
		Interval:       pricing.Monthly(),
		Price:          price,
		BasePrice:      price,
	}, eventstore.EventMetadata{UserID: "test"})
	_ = agg.Activate(eventstore.EventMetadata{UserID: "test"})
	return agg
}

// currentPeriodOf returns the billing period that matches the aggregate's current period.
func currentPeriodOf(agg *contract.ContractAggregate) shared.DateRange {
	return agg.CurrentPeriod()
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

// newActiveAggWithPrice creates an active aggregate with a matching Price entity and returns both.
func newActiveAggWithPrice(clock shared.Clock, contractType contract.ContractType, amount shared.Money) (*contract.ContractAggregate, *pricing.Price) {
	priceEntity := newTestPrice(shared.NewProductID(), amount, nil)
	agg := newTestContractAggregateWithPriceID(clock, contractType, amount, priceEntity.ID())
	return agg, priceEntity
}

// priceRepoFor returns a mock price repo that returns the given price.
func priceRepoFor(p *pricing.Price) *mockPriceRepo {
	return &mockPriceRepo{price: p}
}

// --- Tests ---

// TestGenerateInvoice_AllowPartialPaymentThreaded guards review #1: BillingConfig
// AllowPartialPayment must be propagated onto the generated invoice so consumers
// have an opt-in path for partial payments through the normal flow.
func TestGenerateInvoice_AllowPartialPaymentThreaded(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))

	// Default config: partial NOT allowed -> invoice flag false.
	svcDefault := NewBillingService(
		&mockContractRepo{agg: agg}, &mockInvoiceRepo{}, &mockUsageRepo{},
		balance.BalanceConfig{}, priceRepoFor(priceEntity), &mockProductRepo{},
		plugin.NewRegistry(), BillingConfig{DaysUntilDue: 30}, clock,
	)
	invDefault, err := svcDefault.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invDefault.AllowPartialPay() {
		t.Error("expected AllowPartialPay false by default")
	}

	// Opt-in config: invoice flag true.
	svcOptIn := NewBillingService(
		&mockContractRepo{agg: agg}, &mockInvoiceRepo{}, &mockUsageRepo{},
		balance.BalanceConfig{}, priceRepoFor(priceEntity), &mockProductRepo{},
		plugin.NewRegistry(), BillingConfig{DaysUntilDue: 30, AllowPartialPayment: true}, clock,
	)
	invOptIn, err := svcOptIn.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !invOptIn.AllowPartialPay() {
		t.Error("expected AllowPartialPay true when BillingConfig.AllowPartialPayment is set")
	}
}

func TestGenerateInvoice_SubscriptionBasic(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)
	invRepo := &mockInvoiceRepo{}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
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

func TestGenerateInvoice_DueDate_ZeroValueConfigFallsBackTo30Days(t *testing.T) {
	// Regression for issue #154: a zero-value BillingConfig{} must not produce an
	// immediately-due invoice. DaysUntilDue==0 falls back to 30 days, anchored on
	// the issue date (clock.Now()), not the billing period end.
	clock := newTestClock() // FixedTime: 2026-01-15
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{}, // zero value: no DaysUntilDue set
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	now := clock.Now()
	wantDue := now.AddDate(0, 0, 30)
	if !inv.DueDate().Equal(wantDue) {
		t.Errorf("due date = %v, want %v (issue date %v + 30d)", inv.DueDate(), wantDue, now)
	}
	// Must not be immediately due (issue date == due date).
	if inv.DueDate().Equal(now) {
		t.Error("zero-value config produced an immediately-due invoice")
	}
}

func TestGenerateInvoice_DueDate_ExplicitDaysUntilDueRespected(t *testing.T) {
	clock := newTestClock() // FixedTime: 2026-01-15
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 7},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantDue := clock.Now().AddDate(0, 0, 7)
	if !inv.DueDate().Equal(wantDue) {
		t.Errorf("due date = %v, want %v", inv.DueDate(), wantDue)
	}
}

func TestGenerateInvoice_DiscountCap(t *testing.T) {
	clock := newTestClock()
	price := jpy(5000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)
	invRepo := &mockInvoiceRepo{}

	// Register a discount hook that returns more than the subtotal
	registry := plugin.NewRegistry()
	_ = registry.Register(&overDiscountPlugin{discount: jpy(9999)})

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		registry,
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
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
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Applied credit should be zero
	if !inv.AppliedBalance().IsZero() {
		t.Errorf("expected zero applied balance, got %v", inv.AppliedBalance().Amount())
	}
}

func TestGenerateInvoice_WithCredits(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	creditEntry, _ := balance.NewBalanceEntry(agg.AccountID(), jpy(3000), balance.BalanceReasonGoodwill, clock.Now())
	balanceRepo := &mockBalanceRepo{credits: []*balance.BalanceEntry{creditEntry}}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
		WithBalanceRepo(balanceRepo),
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Applied credit should be 3000
	expectedCredit := new(big.Rat).SetInt64(3000)
	if inv.AppliedBalance().Amount().Cmp(expectedCredit) != 0 {
		t.Errorf("expected applied balance 3000, got %v", inv.AppliedBalance().Amount())
	}

	// Amount due should be 10000 - 3000 = 7000
	expectedDue := new(big.Rat).SetInt64(7000)
	if inv.AmountDue().Amount().Cmp(expectedDue) != 0 {
		t.Errorf("expected amount due 7000, got %v", inv.AmountDue().Amount())
	}
}

// --- Payment method inheritance tests ---

func TestGenerateInvoice_InheritsContractPaymentMethod(t *testing.T) {
	clock := newTestClock()
	price := jpy(5000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	// Set payment method on contract
	pmID := "pm-contract-inherited"
	_ = agg.ChangePaymentMethod(&pmID, eventstore.EventMetadata{UserID: "test"})

	invRepo := &mockInvoiceRepo{}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if inv.PaymentMethodID() == nil || *inv.PaymentMethodID() != "pm-contract-inherited" {
		t.Errorf("expected invoice to inherit contract payment method pm-contract-inherited, got %v", inv.PaymentMethodID())
	}
}

func TestGenerateInvoice_NoPaymentMethodWhenContractHasNone(t *testing.T) {
	clock := newTestClock()
	price := jpy(5000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)
	invRepo := &mockInvoiceRepo{}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if inv.PaymentMethodID() != nil {
		t.Errorf("expected nil payment method on invoice, got %v", inv.PaymentMethodID())
	}
}

// --- Status guard tests ---

func newDraftContractAggregateWithPrice(clock shared.Clock, price shared.Money) (*contract.ContractAggregate, *pricing.Price) {
	priceEntity := newTestPrice(shared.NewProductID(), price, nil)
	cid := shared.NewContractID()
	agg := contract.NewContractAggregate(cid, clock)
	_ = agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-service-billing_service-2",
		AccountID:      shared.NewAccountID(),
		PriceID:        priceEntity.ID(),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          price,
		BasePrice:      price,
	}, eventstore.EventMetadata{UserID: "test"})
	return agg, priceEntity
}

func newBillingSvcWithPrice(agg *contract.ContractAggregate, invRepo *mockInvoiceRepo, priceEntity *pricing.Price, clock shared.Clock) *BillingService {
	if invRepo == nil {
		invRepo = &mockInvoiceRepo{}
	}
	return NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)
}

func TestGenerateInvoice_StatusGuard_DraftAllowed(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newDraftContractAggregateWithPrice(clock, jpy(1000))
	svc := newBillingSvcWithPrice(agg, nil, priceEntity, clock)

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
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	_ = agg.Cancel("test", eventstore.EventMetadata{UserID: "test"})
	svc := newBillingSvcWithPrice(agg, nil, priceEntity, clock)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err == nil {
		t.Fatal("cancelled contract should block invoice generation")
	}
}

func TestGenerateInvoice_StatusGuard_SuspendedSkipBlocked(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	_ = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingSkip,
		Reason:          "payment pending",
	}, eventstore.EventMetadata{UserID: "test"})
	svc := newBillingSvcWithPrice(agg, nil, priceEntity, clock)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err == nil {
		t.Fatal("suspended (skip) contract should block invoice generation")
	}
}

func TestGenerateInvoice_StatusGuard_SuspendedDeferBlocked(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	_ = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingDefer,
		Reason:          "payment pending",
	}, eventstore.EventMetadata{UserID: "test"})
	svc := newBillingSvcWithPrice(agg, nil, priceEntity, clock)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err == nil {
		t.Fatal("suspended (defer) contract should block invoice generation")
	}
}

func TestGenerateInvoice_StatusGuard_SuspendedContinueAllowed(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	_ = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingContinue,
		Reason:          "admin hold",
	}, eventstore.EventMetadata{UserID: "test"})
	svc := newBillingSvcWithPrice(agg, nil, priceEntity, clock)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
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
	agg, priceEntity := newDraftContractAggregateWithPrice(clock, jpy(1000))

	existingDraft, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByStatus: []*invoice.Invoice{existingDraft}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	_, err = svc.GenerateInvoice(context.Background(), agg.ContractID(), newBillingPeriod())
	if err == nil {
		t.Fatal("should block duplicate draft invoice")
	}
}

func TestGenerateInvoice_DuplicatePeriodBlocked(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	existingInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{existingInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	_, err = svc.GenerateInvoice(context.Background(), agg.ContractID(), period)
	if err == nil {
		t.Fatal("should block duplicate invoice for same billing period")
	}
}

func TestGenerateInvoice_VoidedAllowsRegeneration(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), period)
	if err != nil {
		t.Fatalf("voided invoice should allow regeneration: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice")
	}
}

func TestGenerateInvoice_OneTimeDuplicateBlocked(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeOneTime, jpy(5000))

	existingInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(5000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByContract: []*invoice.Invoice{existingInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	_, err = svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err == nil {
		t.Fatal("should block duplicate invoice for one-time contract")
	}
}

// --- Price-aware billing tests ---

func TestCalculateSubtotal_BillingPeriodMismatch(t *testing.T) {
	clock := newTestClock()
	priceEntity := newTestPrice(shared.NewProductID(), jpy(10000), nil)
	agg := newTestContractAggregateWithPriceID(clock, contract.ContractTypeSubscription, jpy(10000), priceEntity.ID())

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		&mockPriceRepo{price: priceEntity},
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	// Use a billing period that does NOT match the aggregate's current period
	mismatchedPeriod := newBillingPeriod() // [2026-01-01, 2026-02-01) != currentPeriod [2026-01-15, 2026-02-15)
	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), mismatchedPeriod)
	if err == nil {
		t.Fatal("expected error for billing period mismatch")
	}
}

func TestCalculateSubtotal_SubscriptionUsesPriceEntity(t *testing.T) {
	clock := newTestClock()
	priceAmount := jpy(15000)
	priceEntity := newTestPrice(shared.NewProductID(), priceAmount, nil)
	// Contract has price=10000 but Price entity has 15000
	agg := newTestContractAggregateWithPriceID(clock, contract.ContractTypeSubscription, jpy(10000), priceEntity.ID())

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		&mockPriceRepo{price: priceEntity},
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should use Price entity amount (15000), not aggregate Price (10000)
	if inv.Subtotal().Amount().Cmp(priceAmount.Amount()) != 0 {
		t.Errorf("expected subtotal %v (from Price entity), got %v", priceAmount.Amount(), inv.Subtotal().Amount())
	}
}

func TestCalculateSubtotal_OneTimeUsesPriceEntity(t *testing.T) {
	clock := newTestClock()
	priceAmount := jpy(50000)
	priceEntity := newTestPrice(shared.NewProductID(), priceAmount, nil)
	agg := newTestContractAggregateWithPriceID(clock, contract.ContractTypeOneTime, jpy(10000), priceEntity.ID())

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		&mockPriceRepo{price: priceEntity},
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.Subtotal().Amount().Cmp(priceAmount.Amount()) != 0 {
		t.Errorf("expected subtotal %v (from Price entity), got %v", priceAmount.Amount(), inv.Subtotal().Amount())
	}
}

func TestCalculateSubtotal_UsageBased_ViaProductAndPrice(t *testing.T) {
	clock := newTestClock()

	// Create product with usage metrics
	prod := product.NewProduct("API Access", "API usage product", clock.Now())
	prod.AddUsageMetric(product.UsageMetric{Name: "api_calls", IncludedQuantity: 50})

	// Create price with usage pricing model (10 JPY per unit)
	usagePricing := pricing.UsagePrice{UnitPrice: jpy(10)}
	baseAmount := jpy(1000)
	priceEntity := newTestPrice(prod.ID(), baseAmount, usagePricing)

	agg := newTestContractAggregateWithPriceID(clock, contract.ContractTypeUsageBased, jpy(1000), priceEntity.ID())

	// Usage repo returns 150 total usage for api_calls
	usageRepo := &mockUsageRepoWithMetrics{
		summaries: map[shared.MetricName]*usage.UsageSummary{
			"api_calls": {TotalUsage: 150},
		},
	}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		usageRepo,
		balance.BalanceConfig{},
		&mockPriceRepo{price: priceEntity},
		&mockProductRepo{product: prod},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// billableUsage = 150 - 50 (included) = 100
	// usageCharge = 100 * 10 = 1000
	// total = baseAmount(1000) + usageCharge(1000) = 2000
	expectedTotal := new(big.Rat).SetInt64(2000)
	if inv.Subtotal().Amount().Cmp(expectedTotal) != 0 {
		t.Errorf("expected subtotal 2000, got %v", inv.Subtotal().Amount())
	}
}

func TestCalculateSubtotal_UsageBased_IncludedQuantityCoversAll(t *testing.T) {
	clock := newTestClock()

	prod := product.NewProduct("API Access", "API usage product", clock.Now())
	prod.AddUsageMetric(product.UsageMetric{Name: "api_calls", IncludedQuantity: 200})

	usagePricing := pricing.UsagePrice{UnitPrice: jpy(10)}
	baseAmount := jpy(1000)
	priceEntity := newTestPrice(prod.ID(), baseAmount, usagePricing)

	agg := newTestContractAggregateWithPriceID(clock, contract.ContractTypeUsageBased, jpy(1000), priceEntity.ID())

	usageRepo := &mockUsageRepoWithMetrics{
		summaries: map[shared.MetricName]*usage.UsageSummary{
			"api_calls": {TotalUsage: 100}, // below included quantity
		},
	}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		usageRepo,
		balance.BalanceConfig{},
		&mockPriceRepo{price: priceEntity},
		&mockProductRepo{product: prod},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// billableUsage = 100 - 200 = -100 → clamped to 0
	// usageCharge = 0
	// total = baseAmount(1000)
	expectedTotal := new(big.Rat).SetInt64(1000)
	if inv.Subtotal().Amount().Cmp(expectedTotal) != 0 {
		t.Errorf("expected subtotal 1000 (base only), got %v", inv.Subtotal().Amount())
	}
}

func TestGenerateInvoice_LineItemHasPriceID(t *testing.T) {
	clock := newTestClock()
	priceAmount := jpy(10000)
	priceEntity := newTestPrice(shared.NewProductID(), priceAmount, nil)
	agg := newTestContractAggregateWithPriceID(clock, contract.ContractTypeSubscription, priceAmount, priceEntity.ID())
	invRepo := &mockInvoiceRepo{}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := inv.LineItems()
	if len(items) == 0 {
		t.Fatal("expected at least one line item")
	}

	if items[0].PriceID() != priceEntity.ID() {
		t.Errorf("expected line item priceID %s, got %s", priceEntity.ID(), items[0].PriceID())
	}

	if items[0].Amount().Amount().Cmp(priceAmount.Amount()) != 0 {
		t.Errorf("expected line item amount %v, got %v", priceAmount.Amount(), items[0].Amount().Amount())
	}
}

// mockUsageRepoWithMetrics returns specific summaries per metric name.
type mockUsageRepoWithMetrics struct {
	summaries map[shared.MetricName]*usage.UsageSummary
}

func (m *mockUsageRepoWithMetrics) Record(_ context.Context, _ *usage.UsageRecord) error { return nil }
func (m *mockUsageRepoWithMetrics) GetSummary(_ context.Context, _ shared.ContractID, metric shared.MetricName, _ shared.DateRange) (*usage.UsageSummary, error) {
	if s, ok := m.summaries[metric]; ok {
		return s, nil
	}
	return &usage.UsageSummary{TotalUsage: 0}, nil
}
func (m *mockUsageRepoWithMetrics) GetRecords(_ context.Context, _ shared.ContractID, _ shared.MetricName, _, _ time.Time) ([]*usage.UsageRecord, error) {
	return nil, nil
}

// --- Error path tests ---

func TestCalculateSubtotal_EmptyPriceID(t *testing.T) {
	clock := newTestClock()
	// Contract with empty PriceID
	agg := newTestContractAggregateWithPriceID(clock, contract.ContractTypeSubscription, jpy(10000), "")

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		&mockPriceRepo{},
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err == nil {
		t.Fatal("expected error for empty priceID")
	}
}

func TestCalculateSubtotal_PriceRepoError(t *testing.T) {
	clock := newTestClock()
	agg := newTestContractAggregateWithPriceID(clock, contract.ContractTypeSubscription, jpy(10000), shared.PriceID("price-999"))

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		&mockPriceRepo{err: fmt.Errorf("price not found")},
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err == nil {
		t.Fatal("expected error when priceRepo fails")
	}
}

func TestCalculateUsageCharge_ProductRepoError(t *testing.T) {
	clock := newTestClock()
	usagePricing := pricing.UsagePrice{UnitPrice: jpy(10)}
	priceEntity := newTestPrice(shared.NewProductID(), jpy(1000), usagePricing)
	agg := newTestContractAggregateWithPriceID(clock, contract.ContractTypeUsageBased, jpy(1000), priceEntity.ID())

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{err: fmt.Errorf("product not found")},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err == nil {
		t.Fatal("expected error when productRepo fails")
	}
}

// --- Proration invoice tests ---

func TestGenerateProrationInvoice_Basic(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)
	invRepo := &mockInvoiceRepo{}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	proration := contract.PlanChangeProration{
		CreditAmount:     jpy(3000), // unused old price
		ChargeAmount:     jpy(5000), // new price remainder
		AdjustmentAmount: jpy(2000), // net = 5000 - 3000
		EffectiveDate:    clock.Now(),
	}

	inv, err := svc.GenerateProrationInvoice(context.Background(), agg.ContractID(), proration)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice, got nil")
	}

	// Subtotal should be the adjustment amount (2000)
	expectedSubtotal := new(big.Rat).SetInt64(2000)
	if inv.Subtotal().Amount().Cmp(expectedSubtotal) != 0 {
		t.Errorf("expected subtotal 2000, got %v", inv.Subtotal().Amount())
	}

	// Should be draft status
	if inv.Status() != invoice.InvoiceStatusDraft {
		t.Errorf("expected status draft, got %s", inv.Status())
	}

	// Should have 2 line items (credit + charge)
	if len(inv.LineItems()) != 2 {
		t.Errorf("expected 2 line items, got %d", len(inv.LineItems()))
	}

	// Metadata should mark it as proration
	if inv.Metadata()["invoice_type"] != "proration" {
		t.Errorf("expected metadata invoice_type=proration, got %v", inv.Metadata()["invoice_type"])
	}

	// Should be saved
	if invRepo.saved == nil {
		t.Error("expected invoice to be saved")
	}
}

func TestGenerateProrationInvoice_WithDiscountHook(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	registry := plugin.NewRegistry()
	_ = registry.Register(&overDiscountPlugin{discount: jpy(500)})

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		registry,
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	proration := contract.PlanChangeProration{
		CreditAmount:     jpy(3000),
		ChargeAmount:     jpy(5000),
		AdjustmentAmount: jpy(2000),
		EffectiveDate:    clock.Now(),
	}

	inv, err := svc.GenerateProrationInvoice(context.Background(), agg.ContractID(), proration)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Discount (500) should be applied to the proration subtotal (2000)
	expectedDiscount := new(big.Rat).SetInt64(500)
	if inv.DiscountAmount().Amount().Cmp(expectedDiscount) != 0 {
		t.Errorf("expected discount 500, got %v", inv.DiscountAmount().Amount())
	}
}

func TestGenerateProrationInvoice_WithTaxHook(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	registry := plugin.NewRegistry()
	_ = registry.Register(&tenPercentTaxPlugin{})

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		registry,
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	proration := contract.PlanChangeProration{
		CreditAmount:     jpy(3000),
		ChargeAmount:     jpy(5000),
		AdjustmentAmount: jpy(2000),
		EffectiveDate:    clock.Now(),
	}

	inv, err := svc.GenerateProrationInvoice(context.Background(), agg.ContractID(), proration)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Tax = 10% of 2000 = 200
	expectedTax := new(big.Rat).SetInt64(200)
	if inv.TaxAmount().Amount().Cmp(expectedTax) != 0 {
		t.Errorf("expected tax 200, got %v", inv.TaxAmount().Amount())
	}

	// Total = 2000 + 200 = 2200
	expectedTotal := new(big.Rat).SetInt64(2200)
	if inv.Total().Amount().Cmp(expectedTotal) != 0 {
		t.Errorf("expected total 2200, got %v", inv.Total().Amount())
	}
}

func TestGenerateProrationInvoice_WithCredits(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	creditEntry, _ := balance.NewBalanceEntry(agg.AccountID(), jpy(800), balance.BalanceReasonGoodwill, clock.Now())
	balanceRepo := &mockBalanceRepo{credits: []*balance.BalanceEntry{creditEntry}}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
		WithBalanceRepo(balanceRepo),
	)

	proration := contract.PlanChangeProration{
		CreditAmount:     jpy(3000),
		ChargeAmount:     jpy(5000),
		AdjustmentAmount: jpy(2000),
		EffectiveDate:    clock.Now(),
	}

	inv, err := svc.GenerateProrationInvoice(context.Background(), agg.ContractID(), proration)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Applied credit should be 800
	expectedCredit := new(big.Rat).SetInt64(800)
	if inv.AppliedBalance().Amount().Cmp(expectedCredit) != 0 {
		t.Errorf("expected applied balance 800, got %v", inv.AppliedBalance().Amount())
	}

	// Amount due = 2000 - 800 = 1200
	expectedDue := new(big.Rat).SetInt64(1200)
	if inv.AmountDue().Amount().Cmp(expectedDue) != 0 {
		t.Errorf("expected amount due 1200, got %v", inv.AmountDue().Amount())
	}
}

func TestGenerateProrationInvoice_CancelledContractBlocked(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)
	_ = agg.Cancel("test", eventstore.EventMetadata{UserID: "test"})

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	proration := contract.PlanChangeProration{
		CreditAmount:     jpy(3000),
		ChargeAmount:     jpy(5000),
		AdjustmentAmount: jpy(2000),
		EffectiveDate:    clock.Now(),
	}

	_, err := svc.GenerateProrationInvoice(context.Background(), agg.ContractID(), proration)
	if err == nil {
		t.Fatal("cancelled contract should block proration invoice generation")
	}
}

func TestGenerateProrationInvoice_ZeroCreditAmount(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	// New subscription — no credit, only charge
	proration := contract.PlanChangeProration{
		CreditAmount:     jpy(0),
		ChargeAmount:     jpy(5000),
		AdjustmentAmount: jpy(5000),
		EffectiveDate:    clock.Now(),
	}

	inv, err := svc.GenerateProrationInvoice(context.Background(), agg.ContractID(), proration)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have only 1 line item (charge only, no credit)
	if len(inv.LineItems()) != 1 {
		t.Errorf("expected 1 line item (charge only), got %d", len(inv.LineItems()))
	}

	expectedSubtotal := new(big.Rat).SetInt64(5000)
	if inv.Subtotal().Amount().Cmp(expectedSubtotal) != 0 {
		t.Errorf("expected subtotal 5000, got %v", inv.Subtotal().Amount())
	}
}

func TestGenerateProrationInvoice_NegativeAdjustmentBlocked(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	// Downgrade: credit > charge → negative adjustment
	proration := contract.PlanChangeProration{
		CreditAmount:     jpy(5000),
		ChargeAmount:     jpy(3000),
		AdjustmentAmount: jpy(-2000),
		EffectiveDate:    clock.Now(),
	}

	_, err := svc.GenerateProrationInvoice(context.Background(), agg.ContractID(), proration)
	if err == nil {
		t.Fatal("negative adjustment (downgrade) should be rejected")
	}
}

func TestGenerateProrationInvoice_ZeroAdjustmentBlocked(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	// Same-price change → zero adjustment
	proration := contract.PlanChangeProration{
		CreditAmount:     jpy(3000),
		ChargeAmount:     jpy(3000),
		AdjustmentAmount: jpy(0),
		EffectiveDate:    clock.Now(),
	}

	_, err := svc.GenerateProrationInvoice(context.Background(), agg.ContractID(), proration)
	if err == nil {
		t.Fatal("zero adjustment should be rejected")
	}
}

func TestGenerateProrationInvoice_InheritsPaymentMethod(t *testing.T) {
	clock := newTestClock()
	price := jpy(10000)
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, price)

	pmID := "pm-inherited"
	_ = agg.ChangePaymentMethod(&pmID, eventstore.EventMetadata{UserID: "test"})

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	proration := contract.PlanChangeProration{
		CreditAmount:     jpy(3000),
		ChargeAmount:     jpy(5000),
		AdjustmentAmount: jpy(2000),
		EffectiveDate:    clock.Now(),
	}

	inv, err := svc.GenerateProrationInvoice(context.Background(), agg.ContractID(), proration)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if inv.PaymentMethodID() == nil || *inv.PaymentMethodID() != "pm-inherited" {
		t.Errorf("expected payment method pm-inherited, got %v", inv.PaymentMethodID())
	}
}

// --- RegenerateInvoice tests ---

func TestRegenerateInvoice_DeferSuspended_VoidedExists_Success(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	_ = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingDefer,
		Reason:          "payment pending",
	}, eventstore.EventMetadata{UserID: "test"})

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	inv, err := svc.RegenerateInvoice(context.Background(), agg.ContractID(), period)
	if err != nil {
		t.Fatalf("defer suspended + voided should allow regeneration: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice")
	}
	if inv.Status() != invoice.InvoiceStatusDraft {
		t.Errorf("expected draft status, got %s", inv.Status())
	}
	// Verify revision chain: regenerated invoice links back to the voided one
	if inv.RevisionOf() == nil {
		t.Fatal("expected RevisionOf to be set")
	}
	if *inv.RevisionOf() != voidedInv.ID() {
		t.Errorf("expected RevisionOf %s, got %s", voidedInv.ID(), *inv.RevisionOf())
	}
	if inv.OriginalInvoiceID() == nil {
		t.Fatal("expected OriginalInvoiceID to be set")
	}
	// Verify metadata
	if inv.Metadata()["invoice_type"] != "regeneration" {
		t.Errorf("expected invoice_type=regeneration, got %s", inv.Metadata()["invoice_type"])
	}
}

func TestRegenerateInvoice_DeferSuspended_NoVoided_Error(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	_ = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingDefer,
		Reason:          "payment pending",
	}, eventstore.EventMetadata{UserID: "test"})

	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	_, err := svc.RegenerateInvoice(context.Background(), agg.ContractID(), period)
	if err == nil {
		t.Fatal("defer suspended + no voided should block regeneration")
	}
	assertDomainError(t, err, shared.ErrCodeBusinessRule)
}

func TestRegenerateInvoice_Active_VoidedExists_Success(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	inv, err := svc.RegenerateInvoice(context.Background(), agg.ContractID(), period)
	if err != nil {
		t.Fatalf("active + voided should allow regeneration: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice")
	}
	if inv.Status() != invoice.InvoiceStatusDraft {
		t.Errorf("expected draft status, got %s", inv.Status())
	}
}

func TestRegenerateInvoice_Active_NoVoided_Error(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	_, err := svc.RegenerateInvoice(context.Background(), agg.ContractID(), period)
	if err == nil {
		t.Fatal("active + no voided should block regeneration")
	}
	assertDomainError(t, err, shared.ErrCodeBusinessRule)
}

func TestRegenerateInvoice_SkipSuspended_Error(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	_ = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingSkip,
		Reason:          "admin hold",
	}, eventstore.EventMetadata{UserID: "test"})

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	_, err = svc.RegenerateInvoice(context.Background(), agg.ContractID(), period)
	if err == nil {
		t.Fatal("skip suspended should always block regeneration")
	}
	assertDomainError(t, err, shared.ErrCodeBusinessRule)
}

func TestRegenerateInvoice_ContinueSuspended_VoidedExists_Success(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	_ = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingContinue,
		Reason:          "admin hold",
	}, eventstore.EventMetadata{UserID: "test"})

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	inv, err := svc.RegenerateInvoice(context.Background(), agg.ContractID(), period)
	if err != nil {
		t.Fatalf("continue suspended + voided should allow regeneration: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice")
	}
}

func TestRegenerateInvoice_CancelledContract_Error(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	_ = agg.Cancel("user requested", eventstore.EventMetadata{UserID: "test"})

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	_, err = svc.RegenerateInvoice(context.Background(), agg.ContractID(), period)
	if err == nil {
		t.Fatal("cancelled contract should block regeneration")
	}
	assertDomainError(t, err, shared.ErrCodeBusinessRule)
}

func TestRegenerateInvoice_VoidedAndNonVoidedExist_DuplicateBlocked(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	draftInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusDraft),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedInv, draftInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	_, err = svc.RegenerateInvoice(context.Background(), agg.ContractID(), period)
	if err == nil {
		t.Fatal("should block when non-voided invoice exists alongside voided")
	}
	assertDomainError(t, err, shared.ErrCodeConflict)
}

func TestRegenerateInvoice_PipelineHooksApplied(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	_ = agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingDefer,
		Reason:          "payment pending",
	}, eventstore.EventMetadata{UserID: "test"})

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedInv}}

	registry := plugin.NewRegistry()
	_ = registry.Register(&overDiscountPlugin{discount: jpy(2000)})
	_ = registry.Register(&tenPercentTaxPlugin{})

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		registry,
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := svc.RegenerateInvoice(context.Background(), agg.ContractID(), period)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// subtotal=10000, discount=2000, afterDiscount=8000, tax=800, total=8800
	expectedDiscount := new(big.Rat).SetInt64(2000)
	if inv.DiscountAmount().Amount().Cmp(expectedDiscount) != 0 {
		t.Errorf("expected discount %v, got %v", expectedDiscount, inv.DiscountAmount().Amount())
	}
	expectedTax := new(big.Rat).SetInt64(800)
	if inv.TaxAmount().Amount().Cmp(expectedTax) != 0 {
		t.Errorf("expected tax %v, got %v", expectedTax, inv.TaxAmount().Amount())
	}
	expectedTotal := new(big.Rat).SetInt64(8800)
	if inv.Total().Amount().Cmp(expectedTotal) != 0 {
		t.Errorf("expected total %v, got %v", expectedTotal, inv.Total().Amount())
	}
}

// --- Test helpers ---

func assertDomainError(t *testing.T, err error, expectedCode shared.ErrorCode) {
	t.Helper()
	var domainErr *shared.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if domainErr.Code != expectedCode {
		t.Errorf("expected error code %s, got %s (message: %s)", expectedCode, domainErr.Code, domainErr.Message)
	}
}

func TestRegenerateInvoice_RevisionChainDepth2_PreservesOriginalID(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	// Simulate a second void-and-recreate cycle:
	// original (voided) -> first regeneration (voided) -> second regeneration (under test)
	originalID := shared.NewInvoiceID()
	firstRegenID := shared.NewInvoiceID()

	// The first voided invoice (the original)
	voidedOriginal, err := invoice.NewInvoice(
		originalID, agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	// The second voided invoice (first regeneration, now also voided)
	voidedRegen, err := invoice.NewInvoice(
		firstRegenID, agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(period),
		invoice.WithRevisionOf(originalID),
		invoice.WithOriginalInvoiceID(originalID),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedOriginal, voidedRegen}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	inv, err := svc.RegenerateInvoice(context.Background(), agg.ContractID(), period)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// RevisionOf should point to the latest voided invoice (voidedRegen), not the original
	if inv.RevisionOf() == nil {
		t.Fatal("expected RevisionOf to be set")
	}
	if *inv.RevisionOf() != firstRegenID {
		t.Errorf("expected RevisionOf %s (latest voided), got %s", firstRegenID, *inv.RevisionOf())
	}

	// OriginalInvoiceID should be preserved from the voided invoice's chain root
	if inv.OriginalInvoiceID() == nil {
		t.Fatal("expected OriginalInvoiceID to be set")
	}
	if *inv.OriginalInvoiceID() != originalID {
		t.Errorf("expected OriginalInvoiceID %s (chain root), got %s", originalID, *inv.OriginalInvoiceID())
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

// --- Mock tax plugin ---

type tenPercentTaxPlugin struct{}

func (p *tenPercentTaxPlugin) Name() string                                        { return "tax_10pct" }
func (p *tenPercentTaxPlugin) Version() string                                     { return "1.0.0" }
func (p *tenPercentTaxPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *tenPercentTaxPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *tenPercentTaxPlugin) Priority() int                                       { return 100 }
func (p *tenPercentTaxPlugin) CalculateTax(ctx *plugin.CalculationContext) (shared.Money, error) {
	afterDiscount := ctx.SubtotalAfterDiscount()
	rate := new(big.Rat).SetFrac64(10, 100)
	return afterDiscount.Multiply(rate), nil
}

// TestGenerateInvoice_DuplicateCheck_UsesTxScopedRepo verifies that when
// GenerateInvoice runs inside an active transaction (as CreditNoteService.
// ReissueInvoice invokes it), the duplicate-invoice check reads through the
// transaction-scoped repository — not the service's field repository. On a real
// DB the void written earlier in the same transaction is uncommitted and
// invisible to a separate-connection field-repo read, which would wrongly raise
// a conflict and break void-and-recreate. Here the field repo reports a
// conflicting invoice while the tx-scoped repo is clean; generation must succeed.
func TestGenerateInvoice_DuplicateCheck_UsesTxScopedRepo(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	conflicting := newPaidInvoice(agg.AccountID(), agg.ContractID())
	fieldInvRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{conflicting}}

	cleanInvRepo := &mockInvoiceRepo{}
	mgr := tx.NewNoopTxManager(tx.Repos{
		Contracts: &mockContractRepo{agg: agg},
		Invoices:  cleanInvRepo,
	})

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		fieldInvRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
		WithBillingTxManager(mgr),
	)

	err := tx.Run(context.Background(), mgr, func(txCtx context.Context, _ tx.Repos) error {
		_, genErr := svc.GenerateInvoice(txCtx, agg.ContractID(), period)
		return genErr
	})
	if err != nil {
		t.Fatalf("duplicate check should read tx-scoped repo and not conflict: %v", err)
	}
	if cleanInvRepo.saved == nil {
		t.Error("expected the generated invoice to be saved through the tx-scoped repo")
	}
}

// TestRegenerateInvoice_UsesTxScopedReads verifies that RegenerateInvoice, like
// GenerateInvoice, reads the contract and existing invoices through the
// transaction-scoped repos when invoked inside an active transaction. The voided
// invoice that distinguishes regeneration from net-new creation may have been
// written earlier in the same transaction; a field-repo read on a real DB would
// miss it and wrongly reject the regeneration.
func TestRegenerateInvoice_UsesTxScopedReads(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating voided invoice: %v", err)
	}

	// Field repo is empty (pre-void committed state); only the tx-scoped repo
	// sees the voided invoice written in this transaction.
	fieldInvRepo := &mockInvoiceRepo{}
	txInvRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedInv}}
	mgr := tx.NewNoopTxManager(tx.Repos{
		Contracts: &mockContractRepo{agg: agg},
		Invoices:  txInvRepo,
	})

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		fieldInvRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
		WithBillingTxManager(mgr),
	)

	err = tx.Run(context.Background(), mgr, func(txCtx context.Context, _ tx.Repos) error {
		inv, genErr := svc.RegenerateInvoice(txCtx, agg.ContractID(), period)
		if genErr != nil {
			return genErr
		}
		if inv == nil || inv.RevisionOf() == nil || *inv.RevisionOf() != voidedInv.ID() {
			t.Errorf("expected replacement linked to the tx-scoped voided invoice")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("regeneration should read tx-scoped repos and find the voided invoice: %v", err)
	}
}

// --- FinalizeInvoice / OnInvoiceIssued hook ---

// onInvoiceIssuedSpyPlugin records OnInvoiceIssued invocations.
type onInvoiceIssuedSpyPlugin struct {
	called   bool
	calls    int
	received *invoice.Invoice
	err      error // if set, OnInvoiceIssued returns this error
}

func (p *onInvoiceIssuedSpyPlugin) Name() string                                        { return "invoice-issued-spy" }
func (p *onInvoiceIssuedSpyPlugin) Version() string                                     { return "1.0.0" }
func (p *onInvoiceIssuedSpyPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *onInvoiceIssuedSpyPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *onInvoiceIssuedSpyPlugin) Priority() int                                       { return 500 }
func (p *onInvoiceIssuedSpyPlugin) OnInvoiceIssued(_ *plugin.Context, inv *invoice.Invoice) error {
	p.called = true
	p.calls++
	p.received = inv
	return p.err
}

func newDraftInvoiceForFinalize(t *testing.T) *invoice.Invoice {
	t.Helper()
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		jpy(10000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusDraft),
	)
	if err != nil {
		t.Fatalf("failed to create draft invoice: %v", err)
	}
	return inv
}

func newFinalizeTestService(invRepo *mockInvoiceRepo, registry *plugin.Registry) *BillingService {
	return NewBillingService(
		&mockContractRepo{}, invRepo, &mockUsageRepo{},
		balance.BalanceConfig{}, &mockPriceRepo{}, &mockProductRepo{},
		registry, BillingConfig{DaysUntilDue: 30}, newTestClock(),
	)
}

func TestFinalizeInvoice_FiresOnInvoiceIssuedHook(t *testing.T) {
	inv := newDraftInvoiceForFinalize(t)
	invRepo := &mockInvoiceRepo{byID: inv}
	spy := &onInvoiceIssuedSpyPlugin{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("failed to register spy plugin: %v", err)
	}

	svc := newFinalizeTestService(invRepo, registry)

	got, err := svc.FinalizeInvoice(context.Background(), inv.ID())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status() != invoice.InvoiceStatusFinalized {
		t.Errorf("status: got %s, want finalized", got.Status())
	}
	if invRepo.saved == nil {
		t.Error("expected finalized invoice to be saved")
	}
	if !spy.called {
		t.Fatal("expected OnInvoiceIssued hook to be called")
	}
	if spy.received == nil || spy.received.ID() != inv.ID() {
		t.Error("OnInvoiceIssued hook received wrong invoice")
	}
	// Hook must fire AFTER finalization: the invoice it sees is finalized.
	if spy.received.Status() != invoice.InvoiceStatusFinalized {
		t.Errorf("hook saw invoice status %s, want finalized", spy.received.Status())
	}
}

func TestFinalizeInvoice_HookErrorIsNonFatal(t *testing.T) {
	inv := newDraftInvoiceForFinalize(t)
	invRepo := &mockInvoiceRepo{byID: inv}
	spy := &onInvoiceIssuedSpyPlugin{err: errors.New("metrics backend down")}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("failed to register spy plugin: %v", err)
	}

	svc := newFinalizeTestService(invRepo, registry)

	got, err := svc.FinalizeInvoice(context.Background(), inv.ID())
	if err != nil {
		t.Fatalf("hook error must be non-fatal, got: %v", err)
	}
	if got.Status() != invoice.InvoiceStatusFinalized {
		t.Errorf("status: got %s, want finalized", got.Status())
	}
	if invRepo.saved == nil {
		t.Error("expected finalized invoice to be saved despite hook error")
	}
}

func TestFinalizeInvoice_NonDraftRejected(t *testing.T) {
	inv := newDraftInvoiceForFinalize(t)
	if err := inv.Finalize(); err != nil {
		t.Fatalf("setup finalize failed: %v", err)
	}
	invRepo := &mockInvoiceRepo{byID: inv}
	spy := &onInvoiceIssuedSpyPlugin{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("failed to register spy plugin: %v", err)
	}

	svc := newFinalizeTestService(invRepo, registry)

	_, err := svc.FinalizeInvoice(context.Background(), inv.ID())
	if err == nil {
		t.Fatal("expected error finalizing a non-draft invoice")
	}
	var domainErr *shared.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != shared.ErrCodeInvalidStateTransition {
		t.Errorf("expected invalid_state_transition domain error, got: %v", err)
	}
	if invRepo.saved != nil {
		t.Error("non-draft invoice must not be saved")
	}
	if spy.called {
		t.Error("OnInvoiceIssued must not fire when finalization is rejected")
	}
}

// TestFinalizeInvoice_SecondCallRejected locks the in-tx check-then-act:
// the load and the draft→finalized transition happen inside the transaction,
// so a repeat call re-reads the already-finalized invoice and is rejected —
// OnInvoiceIssued fires exactly once, never twice.
func TestFinalizeInvoice_SecondCallRejected(t *testing.T) {
	inv := newDraftInvoiceForFinalize(t)
	invRepo := &mockInvoiceRepo{byID: inv}
	spy := &onInvoiceIssuedSpyPlugin{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("failed to register spy plugin: %v", err)
	}

	svc := newFinalizeTestService(invRepo, registry)

	if _, err := svc.FinalizeInvoice(context.Background(), inv.ID()); err != nil {
		t.Fatalf("first finalize failed: %v", err)
	}

	_, err := svc.FinalizeInvoice(context.Background(), inv.ID())
	if err == nil {
		t.Fatal("second finalize must be rejected")
	}
	var domainErr *shared.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != shared.ErrCodeInvalidStateTransition {
		t.Errorf("expected invalid_state_transition domain error, got: %v", err)
	}
	if spy.calls != 1 {
		t.Errorf("OnInvoiceIssued calls: got %d, want exactly 1", spy.calls)
	}
}

func TestFinalizeInvoice_NotFound(t *testing.T) {
	svc := newFinalizeTestService(&mockInvoiceRepo{}, plugin.NewRegistry())

	_, err := svc.FinalizeInvoice(context.Background(), shared.NewInvoiceID())
	if err == nil {
		t.Fatal("expected not-found error")
	}
	var domainErr *shared.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != shared.ErrCodeNotFound {
		t.Errorf("expected not_found domain error, got: %v", err)
	}
}
