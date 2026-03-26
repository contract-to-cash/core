package integration

import (
	"context"
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/credit"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// --- Additional mock plugins for plugin integration tests ---

// mockDiscountPluginA is a discount plugin with configurable name, rate, and priority.
type mockDiscountPluginA struct {
	name     string
	rate     *big.Rat
	priority int
	called   bool
}

func (p *mockDiscountPluginA) Name() string                                        { return p.name }
func (p *mockDiscountPluginA) Version() string                                     { return "1.0.0" }
func (p *mockDiscountPluginA) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *mockDiscountPluginA) Shutdown(_ context.Context) error                    { return nil }
func (p *mockDiscountPluginA) Priority() int                                       { return p.priority }

func (p *mockDiscountPluginA) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
	p.called = true
	return ctx.Subtotal().Multiply(p.rate), nil
}

// mockMultiHookPlugin implements both DiscountHook and InvoiceLifecycleHook.
type mockMultiHookPlugin struct {
	discountRate       *big.Rat
	beforeCalcCalled   bool
	afterCalcCalled    bool
	discountCalcCalled bool
}

func (p *mockMultiHookPlugin) Name() string                                        { return "multi-hook" }
func (p *mockMultiHookPlugin) Version() string                                     { return "1.0.0" }
func (p *mockMultiHookPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *mockMultiHookPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *mockMultiHookPlugin) Priority() int                                       { return 50 }

func (p *mockMultiHookPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
	p.discountCalcCalled = true
	return ctx.Subtotal().Multiply(p.discountRate), nil
}

func (p *mockMultiHookPlugin) BeforeCalculation(_ *plugin.CalculationContext) error {
	p.beforeCalcCalled = true
	return nil
}

func (p *mockMultiHookPlugin) AfterCalculation(_ *plugin.CalculationContext, _ *invoice.Invoice) error {
	p.afterCalcCalled = true
	return nil
}

// --- Tests ---

func TestMultipleDiscountPlugins(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	creditRepo := inmemory.NewInMemoryCreditRepository(clock)
	registry := plugin.NewRegistry()

	// Plugin A: 5% discount, priority=100 (higher priority, runs first)
	pluginA := &mockDiscountPluginA{
		name:     "discount-5pct",
		rate:     new(big.Rat).SetFrac64(5, 100),
		priority: 100,
	}
	if err := registry.Register(pluginA); err != nil {
		t.Fatalf("failed to register plugin A: %v", err)
	}

	// Plugin B: 10% discount, priority=200 (lower priority, runs second)
	pluginB := &mockDiscountPluginA{
		name:     "discount-10pct",
		rate:     new(big.Rat).SetFrac64(10, 100),
		priority: 200,
	}
	if err := registry.Register(pluginB); err != nil {
		t.Fatalf("failed to register plugin B: %v", err)
	}

	svc := service.NewBillingService(
		contractRepo,
		invoiceRepo,
		usageRepo,
		creditRepo,
		credit.CreditConfig{},
		nil,
		registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
	)

	price := moneyJPY(10000)
	agg := createActiveContract(t, ctx, clock, contractRepo, price)

	inv, err := svc.GenerateInvoice(ctx, agg.ContractID(), billingPeriod())
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}

	// Both plugins should have been called
	if !pluginA.called {
		t.Error("plugin A was not called")
	}
	if !pluginB.called {
		t.Error("plugin B was not called")
	}

	// Total discount = 5% + 10% = 15% of 10000 = 1500
	assertMoneyEquals(t, "discount", inv.DiscountAmount(), 1500)

	// afterDiscount = 10000 - 1500 = 8500, no tax
	assertMoneyEquals(t, "total", inv.Total(), 8500)

	// Verify priority order: both discount hooks sorted by priority
	hooks := registry.GetDiscountHooks()
	if len(hooks) != 2 {
		t.Fatalf("expected 2 discount hooks, got %d", len(hooks))
	}
	if hooks[0].Priority() > hooks[1].Priority() {
		t.Error("discount hooks are not sorted by priority (ascending)")
	}
}

func TestPluginImplementsMultipleHooks(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	creditRepo := inmemory.NewInMemoryCreditRepository(clock)
	registry := plugin.NewRegistry()

	// Register a single plugin that implements both DiscountHook and InvoiceLifecycleHook
	multiPlugin := &mockMultiHookPlugin{
		discountRate: new(big.Rat).SetFrac64(20, 100), // 20%
	}
	if err := registry.Register(multiPlugin); err != nil {
		t.Fatalf("failed to register multi-hook plugin: %v", err)
	}

	svc := service.NewBillingService(
		contractRepo,
		invoiceRepo,
		usageRepo,
		creditRepo,
		credit.CreditConfig{},
		nil,
		registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
	)

	price := moneyJPY(5000)
	agg := createActiveContract(t, ctx, clock, contractRepo, price)

	inv, err := svc.GenerateInvoice(ctx, agg.ContractID(), billingPeriod())
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}

	// Verify all hooks were called
	if !multiPlugin.beforeCalcCalled {
		t.Error("BeforeCalculation was not called")
	}
	if !multiPlugin.discountCalcCalled {
		t.Error("CalculateDiscount was not called")
	}
	if !multiPlugin.afterCalcCalled {
		t.Error("AfterCalculation was not called")
	}

	// Verify the plugin is registered under both hook types
	discountHooks := registry.GetDiscountHooks()
	lifecycleHooks := registry.GetInvoiceLifecycleHooks()
	if len(discountHooks) != 1 {
		t.Errorf("expected 1 discount hook, got %d", len(discountHooks))
	}
	if len(lifecycleHooks) != 1 {
		t.Errorf("expected 1 lifecycle hook, got %d", len(lifecycleHooks))
	}

	// discount = 20% of 5000 = 1000
	assertMoneyEquals(t, "discount", inv.DiscountAmount(), 1000)

	// total = 5000 - 1000 = 4000
	assertMoneyEquals(t, "total", inv.Total(), 4000)
}

func TestPluginPriorityOrder(t *testing.T) {
	registry := plugin.NewRegistry()

	// Register in reverse priority order
	p1 := &mockDiscountPluginA{name: "high-priority", rate: new(big.Rat).SetFrac64(1, 100), priority: 300}
	p2 := &mockDiscountPluginA{name: "low-priority", rate: new(big.Rat).SetFrac64(1, 100), priority: 100}
	p3 := &mockDiscountPluginA{name: "mid-priority", rate: new(big.Rat).SetFrac64(1, 100), priority: 200}

	_ = registry.Register(p1)
	_ = registry.Register(p2)
	_ = registry.Register(p3)

	hooks := registry.GetDiscountHooks()
	if len(hooks) != 3 {
		t.Fatalf("expected 3 hooks, got %d", len(hooks))
	}

	// Should be sorted: 100, 200, 300
	priorities := make([]int, len(hooks))
	for i, h := range hooks {
		priorities[i] = h.Priority()
	}
	for i := 1; i < len(priorities); i++ {
		if priorities[i-1] > priorities[i] {
			t.Errorf("hooks not sorted by priority: %v", priorities)
			break
		}
	}
}

func TestDuplicatePluginRegistration(t *testing.T) {
	registry := plugin.NewRegistry()

	p := &mockDiscountPluginA{name: "unique-plugin", rate: new(big.Rat).SetFrac64(1, 10), priority: 100}
	if err := registry.Register(p); err != nil {
		t.Fatalf("first registration should succeed: %v", err)
	}

	p2 := &mockDiscountPluginA{name: "unique-plugin", rate: new(big.Rat).SetFrac64(2, 10), priority: 200}
	if err := registry.Register(p2); err == nil {
		t.Error("expected error when registering plugin with duplicate name")
	}
}
