package integration

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// --- Test helper functions ---

func fixedClock() shared.FixedClock {
	return shared.FixedClock{FixedTime: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
}

func moneyJPY(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func emptyMetadata() eventstore.EventMetadata {
	return eventstore.EventMetadata{UserID: "test-user"}
}

// createActiveContractWithPrice creates a contract aggregate with a matching Price entity,
// applies Create and Activate, saves both to their repositories, and returns both.
func createActiveContractWithPrice(
	t *testing.T,
	ctx context.Context,
	clock shared.Clock,
	contractRepo *inmemory.InMemoryContractRepository,
	priceRepo *inmemory.InMemoryPriceRepository,
	price shared.Money,
) *contract.ContractAggregate {
	t.Helper()

	// Create Price entity
	priceEntity := pricing.NewPrice(
		shared.NewProductID(), price, price.Currency(),
		pricing.BillingCycleMonthly, nil, clock.Now(),
	)
	if err := priceRepo.Save(ctx, priceEntity); err != nil {
		t.Fatalf("failed to save price: %v", err)
	}

	contractID := shared.NewContractID()
	agg := contract.NewContractAggregate(contractID, clock)

	err := agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-integration-billing_flow-1",
		AccountID:      shared.AccountID("acc-001"),
		PriceID:        priceEntity.ID(),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          price,
		BasePrice:      price,
	}, emptyMetadata())
	if err != nil {
		t.Fatalf("failed to create contract: %v", err)
	}

	err = agg.Activate(emptyMetadata())
	if err != nil {
		t.Fatalf("failed to activate contract: %v", err)
	}

	err = contractRepo.Save(ctx, agg)
	if err != nil {
		t.Fatalf("failed to save contract: %v", err)
	}

	return agg
}

// --- Mock plugins ---

// mockCouponPlugin implements DiscountHook with a fixed percentage discount.
type mockCouponPlugin struct {
	rate     *big.Rat // e.g. 0.10 for 10%
	priority int
}

func (p *mockCouponPlugin) Name() string                                        { return "mock-coupon" }
func (p *mockCouponPlugin) Version() string                                     { return "1.0.0" }
func (p *mockCouponPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *mockCouponPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *mockCouponPlugin) Priority() int                                       { return p.priority }

func (p *mockCouponPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
	return ctx.Subtotal().Multiply(p.rate), nil
}

// mockTaxPlugin implements TaxHook with a fixed percentage tax.
type mockTaxPlugin struct {
	rate     *big.Rat // e.g. 0.10 for 10%
	priority int
}

func (p *mockTaxPlugin) Name() string                                        { return "mock-tax" }
func (p *mockTaxPlugin) Version() string                                     { return "1.0.0" }
func (p *mockTaxPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *mockTaxPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *mockTaxPlugin) Priority() int                                       { return p.priority }

func (p *mockTaxPlugin) CalculateTax(ctx *plugin.CalculationContext) (shared.Money, error) {
	return ctx.SubtotalAfterDiscount().Multiply(p.rate), nil
}

// mockHugeDiscountPlugin returns a discount larger than the subtotal.
type mockHugeDiscountPlugin struct{}

func (p *mockHugeDiscountPlugin) Name() string                                        { return "mock-huge-discount" }
func (p *mockHugeDiscountPlugin) Version() string                                     { return "1.0.0" }
func (p *mockHugeDiscountPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *mockHugeDiscountPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *mockHugeDiscountPlugin) Priority() int                                       { return 100 }

func (p *mockHugeDiscountPlugin) CalculateDiscount(_ *plugin.CalculationContext) (shared.Money, error) {
	return moneyJPY(2000), nil
}

// --- Tests ---

func TestSubscriptionBillingFlow(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()
	registry := plugin.NewRegistry()

	svc := service.NewBillingService(
		contractRepo,
		invoiceRepo,
		usageRepo,
		balance.BalanceConfig{},
		priceRepo, productRepo,
		registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
		service.WithBalanceRepo(balanceRepo),
	)

	price := moneyJPY(5000)
	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, price)

	inv, err := svc.GenerateInvoice(ctx, agg.ContractID(), agg.CurrentPeriod())
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}

	// Verify subtotal equals contract price
	if inv.Subtotal().Amount().Cmp(price.Amount()) != 0 {
		t.Errorf("subtotal: got %s, want %s", inv.Subtotal().Amount().RatString(), price.Amount().RatString())
	}

	// No discount/tax plugins, so total == subtotal
	if inv.Total().Amount().Cmp(price.Amount()) != 0 {
		t.Errorf("total: got %s, want %s", inv.Total().Amount().RatString(), price.Amount().RatString())
	}

	// Status should be draft
	if inv.Status() != invoice.InvoiceStatusDraft {
		t.Errorf("status: got %s, want %s", inv.Status(), invoice.InvoiceStatusDraft)
	}

	// amountDue == total (no credit)
	if inv.AmountDue().Amount().Cmp(price.Amount()) != 0 {
		t.Errorf("amountDue: got %s, want %s", inv.AmountDue().Amount().RatString(), price.Amount().RatString())
	}
}

func TestBillingWithDiscountAndTax(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()
	registry := plugin.NewRegistry()

	// 10% discount (priority=100, lower number = higher priority)
	coupon := &mockCouponPlugin{rate: new(big.Rat).SetFrac64(1, 10), priority: 100}
	if err := registry.Register(coupon); err != nil {
		t.Fatalf("failed to register coupon plugin: %v", err)
	}

	// 10% tax (priority=200, runs after discount)
	tax := &mockTaxPlugin{rate: new(big.Rat).SetFrac64(1, 10), priority: 200}
	if err := registry.Register(tax); err != nil {
		t.Fatalf("failed to register tax plugin: %v", err)
	}

	svc := service.NewBillingService(
		contractRepo,
		invoiceRepo,
		usageRepo,
		balance.BalanceConfig{},
		priceRepo, productRepo,
		registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
		service.WithBalanceRepo(balanceRepo),
	)

	price := moneyJPY(10000)
	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, price)

	inv, err := svc.GenerateInvoice(ctx, agg.ContractID(), agg.CurrentPeriod())
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}

	// subtotal = 10000
	assertMoneyEquals(t, "subtotal", inv.Subtotal(), 10000)

	// discount = 10% of 10000 = 1000
	assertMoneyEquals(t, "discount", inv.DiscountAmount(), 1000)

	// afterDiscount = 10000 - 1000 = 9000
	// tax = 10% of 9000 = 900
	assertMoneyEquals(t, "tax", inv.TaxAmount(), 900)

	// total = 9000 + 900 = 9900
	assertMoneyEquals(t, "total", inv.Total(), 9900)
}

func TestDiscountCapGuard(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()
	registry := plugin.NewRegistry()

	// Plugin returns discount=2000, but subtotal is only 1000 -> capped to 1000
	if err := registry.Register(&mockHugeDiscountPlugin{}); err != nil {
		t.Fatalf("failed to register plugin: %v", err)
	}

	// Also register 10% tax to verify tax is calculated on afterDiscount (=0)
	tax := &mockTaxPlugin{rate: new(big.Rat).SetFrac64(1, 10), priority: 200}
	if err := registry.Register(tax); err != nil {
		t.Fatalf("failed to register tax plugin: %v", err)
	}

	svc := service.NewBillingService(
		contractRepo,
		invoiceRepo,
		usageRepo,
		balance.BalanceConfig{},
		priceRepo, productRepo,
		registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
		service.WithBalanceRepo(balanceRepo),
	)

	price := moneyJPY(1000)
	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, price)

	inv, err := svc.GenerateInvoice(ctx, agg.ContractID(), agg.CurrentPeriod())
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}

	// subtotal = 1000
	assertMoneyEquals(t, "subtotal", inv.Subtotal(), 1000)

	// discount capped to subtotal = 1000
	assertMoneyEquals(t, "discount", inv.DiscountAmount(), 1000)

	// afterDiscount = 0, so tax = 0
	assertMoneyEquals(t, "tax", inv.TaxAmount(), 0)

	// total = 0 + 0 = 0
	assertMoneyEquals(t, "total", inv.Total(), 0)
}

func TestBillingWithCreditApplication(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()
	registry := plugin.NewRegistry()

	svc := service.NewBillingService(
		contractRepo,
		invoiceRepo,
		usageRepo,
		balance.BalanceConfig{},
		priceRepo, productRepo,
		registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
		service.WithBalanceRepo(balanceRepo),
	)

	price := moneyJPY(5000)
	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, price)

	// Create a credit entry of 2000 JPY for the same account
	creditEntry, _ := balance.NewBalanceEntry(
		shared.AccountID("acc-001"),
		moneyJPY(2000),
		balance.BalanceReasonManualAdjustment,
		clock.Now(),
	)
	if err := balanceRepo.Save(ctx, creditEntry); err != nil {
		t.Fatalf("failed to save balance entry: %v", err)
	}

	inv, err := svc.GenerateInvoice(ctx, agg.ContractID(), agg.CurrentPeriod())
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}

	// total = 5000 (no discount/tax)
	assertMoneyEquals(t, "total", inv.Total(), 5000)

	// appliedBalance = 2000
	assertMoneyEquals(t, "appliedBalance", inv.AppliedBalance(), 2000)

	// amountDue = 5000 - 2000 = 3000
	assertMoneyEquals(t, "amountDue", inv.AmountDue(), 3000)
}

// assertMoneyEquals is a test helper to compare a Money value with an expected int64 amount.
func assertMoneyEquals(t *testing.T, label string, got shared.Money, wantAmount int64) {
	t.Helper()
	want := new(big.Rat).SetInt64(wantAmount)
	if got.Amount().Cmp(want) != 0 {
		t.Errorf("%s: got %s, want %d", label, got.Amount().RatString(), wantAmount)
	}
}
