// plugin-pipeline-demo demonstrates how multiple plugins compose into a billing
// calculation pipeline with priority-based execution order.
//
// It registers 4 plugins:
//   - AuditLogPlugin (InvoiceLifecycleHook, priority=0): logs calculation flow
//   - CouponPlugin (DiscountHook, priority=500): applies a 10% coupon
//   - LoyaltyDiscountPlugin (DiscountHook, priority=500): gives 5% loyalty discount
//   - TaxPlugin (TaxHook, priority=900): calculates 10% Japanese consumption tax
//
// Run: go run ./examples/plugin-pipeline-demo/
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
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
	"github.com/contract-to-cash/core/plugins/coupon"
	"github.com/contract-to-cash/core/plugins/tax"
)

func main() {
	ctx := context.Background()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}

	fmt.Println("=== Plugin Pipeline Demo ===")
	fmt.Println()

	// ── 1. Setup infrastructure ──
	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()

	// ── 2. Register 4 plugins ──
	registry := plugin.NewRegistry()

	// Plugin 1: AuditLog (lifecycle hooks, highest priority)
	auditPlugin := &auditLogPlugin{}
	must("register audit", registry.Register(auditPlugin))

	// Plugin 2: Coupon (10% discount)
	couponRepo := newInMemoryCouponRepo(clock)
	couponPlugin := coupon.NewCouponPlugin(couponRepo, clock)
	must("register coupon", registry.Register(couponPlugin))

	// Plugin 3: Loyalty discount (5%)
	loyaltyPlugin := &loyaltyDiscountPlugin{}
	must("register loyalty", registry.Register(loyaltyPlugin))

	// Plugin 4: Tax (10% Japanese consumption tax)
	taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})
	must("register tax", registry.Register(taxPlugin))

	configs := map[string]plugin.Config{
		"audit-log":        {"priority": plugin.PriorityHighest},
		"coupon":           {"priority": plugin.PriorityNormal, "maxCouponsPerInvoice": 1, "allowStacking": true},
		"loyalty-discount": {"priority": plugin.PriorityNormal},
		"tax":              {"priority": plugin.PriorityLow},
	}
	must("init plugins", registry.InitializeAll(ctx, configs))
	defer registry.ShutdownAll(ctx)

	fmt.Println("  Registered Plugins:")
	fmt.Println("    [Priority 0]   audit-log        (InvoiceLifecycleHook)")
	fmt.Println("    [Priority 500] coupon            (DiscountHook)")
	fmt.Println("    [Priority 500] loyalty-discount  (DiscountHook)")
	fmt.Println("    [Priority 900] tax               (TaxHook)")
	fmt.Println()

	// ── 3. Create a ¥10,000/month contract ──
	priceEntity := pricing.NewPrice(shared.NewProductID(), moneyJPY(10000), shared.CurrencyJPY, pricing.BillingCycleMonthly, nil, clock.Now())
	must("save price", priceRepo.Save(ctx, priceEntity))

	contractID := shared.NewContractID()
	agg := contract.NewContractAggregate(contractID, clock)
	must("create", agg.Create(contract.CreateContractCommand{
		AccountID:    shared.AccountID("acct-vip-001"),
		PlanID:       shared.PlanID("plan-enterprise"),
		PriceID:      priceEntity.ID(),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        moneyJPY(10000),
		BasePrice:    moneyJPY(10000),
	}, eventstore.EventMetadata{UserID: "admin"}))
	must("activate", agg.Activate(eventstore.EventMetadata{UserID: "admin"}))
	must("save", contractRepo.Save(ctx, agg))

	fmt.Println("  Contract: ¥10,000/month (Enterprise plan)")
	fmt.Println()

	// ── 4. Generate invoice -- watch the pipeline ──
	fmt.Println("=== Invoice Generation Pipeline ===")
	fmt.Println()

	billingService := service.NewBillingService(
		contractRepo, invoiceRepo, usageRepo,
		balance.BalanceConfig{},
		priceRepo, productRepo, registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
		service.WithBalanceRepo(balanceRepo),
	)

	inv, err := billingService.GenerateInvoice(ctx, contractID, agg.CurrentPeriod())
	if err != nil {
		fatal("generate invoice", err)
	}

	// ── 5. Show the calculation breakdown ──
	fmt.Println()
	fmt.Println("=== Calculation Breakdown ===")
	fmt.Println()
	fmt.Printf("  Subtotal:              ¥%s\n", inv.Subtotal().Amount().RatString())
	fmt.Printf("  Discount (coupon+loyalty): -¥%s\n", inv.DiscountAmount().Amount().RatString())

	afterDiscount, _ := inv.Subtotal().Subtract(inv.DiscountAmount())
	fmt.Printf("  After Discounts:       ¥%s\n", afterDiscount.Amount().RatString())
	fmt.Printf("  Tax (10%%):             +¥%s\n", inv.TaxAmount().Amount().RatString())
	fmt.Printf("  ─────────────────────────────\n")
	fmt.Printf("  Total:                 ¥%s\n", inv.Total().Amount().RatString())
	fmt.Printf("  Status:                %s\n", inv.Status())

	fmt.Println()
	fmt.Println("  Pipeline: Subtotal(¥10,000)")
	fmt.Printf("         -> Coupon(-10%%=¥1,000) -> Loyalty(-5%%=¥500) -> AfterDiscount(¥8,500)\n")
	fmt.Printf("         -> Tax(+10%%=¥850) -> Total(¥9,350)\n")

	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}

// ── Custom Plugins ──

// auditLogPlugin logs the invoice calculation lifecycle.
type auditLogPlugin struct {
	priority int
}

func (p *auditLogPlugin) Name() string    { return "audit-log" }
func (p *auditLogPlugin) Version() string { return "1.0.0" }
func (p *auditLogPlugin) Priority() int   { return p.priority }
func (p *auditLogPlugin) Initialize(_ context.Context, config plugin.Config) error {
	if v, ok := config["priority"]; ok {
		if n, ok := v.(int); ok {
			p.priority = n
		}
	}
	return nil
}
func (p *auditLogPlugin) Shutdown(_ context.Context) error { return nil }

func (p *auditLogPlugin) BeforeCalculation(ctx *plugin.CalculationContext) error {
	fmt.Printf("  >> [AuditLog] BeforeCalculation: ContractID=%s\n", ctx.ContractID())
	return nil
}

func (p *auditLogPlugin) AfterCalculation(ctx *plugin.CalculationContext, inv *invoice.Invoice) error {
	fmt.Printf("  >> [AuditLog] AfterCalculation: Total=¥%s, Discounts=%d applied\n",
		inv.Total().Amount().RatString(), len(ctx.AppliedDiscounts()))
	for _, d := range ctx.AppliedDiscounts() {
		fmt.Printf("     - %s (%s): -¥%s\n", d.PluginName, d.Code, d.Amount.Amount().RatString())
	}
	return nil
}

// loyaltyDiscountPlugin gives a 5% discount to loyal customers.
type loyaltyDiscountPlugin struct {
	priority int
}

func (p *loyaltyDiscountPlugin) Name() string    { return "loyalty-discount" }
func (p *loyaltyDiscountPlugin) Version() string { return "1.0.0" }
func (p *loyaltyDiscountPlugin) Priority() int   { return p.priority }
func (p *loyaltyDiscountPlugin) Initialize(_ context.Context, config plugin.Config) error {
	if v, ok := config["priority"]; ok {
		if n, ok := v.(int); ok {
			p.priority = n
		}
	}
	return nil
}
func (p *loyaltyDiscountPlugin) Shutdown(_ context.Context) error { return nil }

func (p *loyaltyDiscountPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
	rate := new(big.Rat).SetFrac64(5, 100) // 5%
	discount := ctx.Subtotal().Multiply(rate)
	fmt.Printf("  >> [Loyalty] 5%% discount on ¥%s = -¥%s\n",
		ctx.Subtotal().Amount().RatString(), discount.Amount().RatString())
	ctx.RecordDiscount(plugin.AppliedDiscount{
		PluginName: "loyalty-discount",
		Code:       "LOYALTY5",
		Amount:     discount,
	})
	return discount, nil
}

// ── In-Memory Coupon Repository ──

type inMemoryCouponRepo struct {
	coupons []*coupon.Coupon
}

func newInMemoryCouponRepo(clock shared.Clock) *inMemoryCouponRepo {
	now := clock.Now()
	limit := 100
	c := coupon.NewCoupon(
		coupon.CouponID("coupon-001"),
		"SAVE10",
		coupon.CouponTypePercentage,
		new(big.Rat).SetFrac64(10, 100), // 10%
		shared.CurrencyJPY,
		nil, nil, // no min/max
		now.Add(-24*time.Hour), now.Add(365*24*time.Hour), // valid for 1 year
		&limit, 0, // usage limit 100, used 0
		nil,
	)
	return &inMemoryCouponRepo{coupons: []*coupon.Coupon{c}}
}

func (r *inMemoryCouponRepo) FindByCode(_ context.Context, code string) (*coupon.Coupon, error) {
	for _, c := range r.coupons {
		if c.Code() == code {
			return c, nil
		}
	}
	return nil, fmt.Errorf("coupon not found: %s", code)
}

func (r *inMemoryCouponRepo) FindApplicable(_ context.Context, _ coupon.CouponQuery) ([]*coupon.Coupon, error) {
	return r.coupons, nil
}

func (r *inMemoryCouponRepo) Save(_ context.Context, _ *coupon.Coupon) error { return nil }

func (r *inMemoryCouponRepo) RecordUsage(_ context.Context, _ coupon.CouponID, _ shared.ContractID) error {
	fmt.Println("  >> [Coupon] Usage recorded for coupon SAVE10")
	return nil
}

func (r *inMemoryCouponRepo) FindUsageByAccount(_ context.Context, _ coupon.CouponID, _ shared.AccountID) (int, error) {
	return 0, nil
}

func (r *inMemoryCouponRepo) SaveRedemption(_ context.Context, _ *coupon.Redemption) error {
	fmt.Println("  >> [Coupon] Redemption recorded")
	return nil
}

func (r *inMemoryCouponRepo) FindRedemptions(_ context.Context, _ coupon.CouponID, _ *shared.AccountID) ([]*coupon.Redemption, error) {
	return nil, nil
}

// ── Helpers ──

func moneyJPY(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func must(action string, err error) {
	if err != nil {
		fatal(action, err)
	}
}

func fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "ERROR [%s]: %v\n", action, err)
	os.Exit(1)
}
