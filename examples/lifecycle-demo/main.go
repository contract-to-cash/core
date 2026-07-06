// lifecycle-demo demonstrates the full contract lifecycle:
// Draft -> Trial -> Active -> Suspended -> Active -> Cancelled
// along with credit management (FIFO application to invoices).
//
// Run: go run ./examples/lifecycle-demo/
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
	"github.com/contract-to-cash/core/plugins/tax"
)

func main() {
	ctx := context.Background()
	clock := &advancingClock{current: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}

	// ── Infrastructure ──
	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()

	// Tax plugin only
	registry := plugin.NewRegistry()
	taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})
	must("register tax", registry.Register(taxPlugin))
	must("init plugins", registry.InitializeAll(ctx, map[string]plugin.Config{
		"tax": {"priority": plugin.PriorityLow},
	}))

	metadata := eventstore.EventMetadata{UserID: "admin"}
	contractID := shared.NewContractID()
	accountID := shared.AccountID("acct-lifecycle-001")

	fmt.Println("=== Contract Lifecycle Demo ===")
	fmt.Println()

	transitions := []string{}
	recordTransition := func(status string) {
		transitions = append(transitions, status)
	}

	// ── 1. Create contract (Draft) ──
	priceEntity := pricing.NewPrice(shared.NewProductID(), moneyJPY(3000), shared.CurrencyJPY, pricing.BillingCycleMonthly, nil, clock.Now())
	must("save price", priceRepo.Save(ctx, priceEntity))

	agg := contract.NewContractAggregate(contractID, clock)
	must("create", agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-lifecycle-demo-demo-1",
		AccountID:      accountID,
		PriceID:        priceEntity.ID(),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          moneyJPY(3000),
		BasePrice:      moneyJPY(3000),
		AutoRenew:      true,
	}, metadata))
	must("save", contractRepo.Save(ctx, agg))
	recordTransition("Draft")
	printStep("1. Contract Created (Draft)", "¥3,000/month Pro plan")

	// ── 2. Start 14-day trial ──
	trialEnd := clock.Now().Add(14 * 24 * time.Hour)
	must("start trial", agg.StartTrial(contract.TrialConfiguration{
		TrialEndDate:           trialEnd,
		AutoConvert:            true,
		RequirePaymentMethod:   false,
		ConversionReminderDays: []int{7, 12},
	}, metadata))
	must("save", contractRepo.Save(ctx, agg))
	recordTransition("Trialing")
	printStep("2. Trial Started (14 days)",
		"AutoConvert=true, Reminders at day 7 and 12\n     TrialEnd=%s", trialEnd.Format("2006-01-02"))

	// ── 3. Trial ends, auto-convert to Active ──
	clock.Advance(14 * 24 * time.Hour) // April 15
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("activate", agg.Activate(metadata)) // Trialing -> Active, sets currentPeriod
	must("save", contractRepo.Save(ctx, agg))
	recordTransition("Active")
	printStep("3. Trial Ended -> Active",
		"Auto-converted to paid subscription on %s\n     Billing period: %s",
		clock.Now().Format("2006-01-02"), agg.CurrentPeriod())

	// ── 4. Generate first invoice and pay ──
	clock.Advance(16 * 24 * time.Hour) // May 1
	billingService := service.NewBillingService(
		contractRepo, invoiceRepo, usageRepo,
		balance.BalanceConfig{DowngradePolicy: balance.BalancePolicyLedger, CancellationPolicy: balance.BalancePolicyLedger},
		priceRepo, productRepo, registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
		service.WithBalanceRepo(balanceRepo),
	)
	inv, err := billingService.GenerateInvoice(ctx, contractID, agg.CurrentPeriod())
	if err != nil {
		fatal("generate invoice", err)
	}
	must("finalize", inv.Finalize())
	must("save invoice", invoiceRepo.Save(ctx, inv))

	gateway := &mockPaymentGateway{}
	paymentService := service.NewPaymentService(gateway, paymentRepo, invoiceRepo, contractRepo, eventStore, registry, clock)
	pmt, err := paymentService.ProcessPayment(ctx, inv.ID(), service.ProcessPaymentInput{
		PaymentMethodID: "pm-visa-1234",
		Amount:          inv.AmountDue(),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "pay-001",
	})
	if err != nil {
		fatal("payment", err)
	}
	printStep("4. First Invoice Paid",
		"Invoice ¥%s (tax incl.), Payment Status=%s",
		inv.Total().Amount().RatString(), pmt.Status())

	// ── 5. Suspend contract (customer on vacation) ──
	clock.Advance(15 * 24 * time.Hour) // May 16
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("suspend", agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingSkip,
		Reason:          "customer on vacation",
	}, metadata))
	must("save", contractRepo.Save(ctx, agg))
	recordTransition("Suspended")
	printStep("5. Contract Suspended",
		"Reason: customer on vacation\n     Billing: skip (no charges during suspension)")

	// ── 6. Resume contract ──
	clock.Advance(30 * 24 * time.Hour) // June 15
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("resume", agg.Resume(metadata))
	must("save", contractRepo.Save(ctx, agg))
	recordTransition("Active")
	printStep("6. Contract Resumed", "Back to active on %s", clock.Now().Format("2006-01-02"))

	// ── 7. Issue credits ──
	balance1, balErr1 := balance.NewBalanceEntry(accountID, moneyJPY(1000), balance.BalanceReasonGoodwill, clock.Now())
	must("new balance1", balErr1)
	must("save balance1", balanceRepo.Save(ctx, balance1))

	clock.Advance(time.Hour)
	balance2, balErr2 := balance.NewBalanceEntry(accountID, moneyJPY(500), balance.BalanceReasonProration, clock.Now())
	must("new balance2", balErr2)
	must("save balance2", balanceRepo.Save(ctx, balance2))

	printStep("7. Balance Entries Issued",
		"Balance 1: ¥1,000 (goodwill)\n     Balance 2: ¥500 (proration)\n     Total balance: ¥1,500")

	// ── 8. Generate next invoice - credits applied FIFO ──
	clock.Advance(15 * 24 * time.Hour) // July 1
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("renew", agg.RenewWithInterval(agg.GetInterval(), metadata))
	must("save", contractRepo.Save(ctx, agg))
	inv2, err := billingService.GenerateInvoice(ctx, contractID, agg.CurrentPeriod())
	if err != nil {
		fatal("generate invoice 2", err)
	}
	printStep("8. Second Invoice (Balance Applied FIFO)",
		"Subtotal=¥%s, Tax=¥%s, Total=¥%s\n     Applied Balance=¥%s (FIFO: goodwill first, then proration)\n     Amount Due=¥%s",
		inv2.Subtotal().Amount().RatString(),
		inv2.TaxAmount().Amount().RatString(),
		inv2.Total().Amount().RatString(),
		inv2.AppliedBalance().Amount().RatString(),
		inv2.AmountDue().Amount().RatString())

	// ── 9. Cancel contract ──
	clock.Advance(time.Hour)
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("cancel", agg.Cancel("switching to competitor", metadata))
	must("save", contractRepo.Save(ctx, agg))
	recordTransition("Cancelled")
	printStep("9. Contract Cancelled", "Reason: switching to competitor")

	// ── Status Timeline ──
	fmt.Println("=== Status Timeline ===")
	fmt.Println()
	fmt.Printf("  %s\n", strings.Join(transitions, " -> "))

	// ── Event History ──
	fmt.Println()
	fmt.Println("=== Full Event History ===")
	fmt.Println()
	events, _ := eventStore.Load(ctx, string(contractID))
	for i, e := range events {
		fmt.Printf("  [%d] %-25s at %s\n", i+1, e.Type, e.OccurredAt.Format("2006-01-02"))
	}

	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}

// ── Helpers ──

type advancingClock struct {
	current time.Time
}

func (c *advancingClock) Now() time.Time          { return c.current }
func (c *advancingClock) Advance(d time.Duration) { c.current = c.current.Add(d) }

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

func printStep(title, format string, args ...interface{}) {
	fmt.Printf("  %s\n", title)
	fmt.Printf("     "+format+"\n\n", args...)
}

// ── Mock PaymentGateway ──

type mockPaymentGateway struct{}

func (g *mockPaymentGateway) ID() string { return "mock-gw" }
func (g *mockPaymentGateway) SupportedMethods() []port.PaymentMethodType {
	return []port.PaymentMethodType{port.PaymentMethodTypeCreditCard}
}
func (g *mockPaymentGateway) Charge(_ context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
	return &port.ChargeResponse{
		TransactionID: "txn-" + req.IdempotencyKey,
		Status:        port.TransactionStatusCaptured,
		Amount:        req.Amount,
		CreatedAt:     time.Now(),
	}, nil
}
func (g *mockPaymentGateway) Authorize(_ context.Context, _ *port.AuthorizeRequest) (*port.AuthorizeResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockPaymentGateway) Capture(_ context.Context, _ *port.CaptureRequest) (*port.CaptureResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockPaymentGateway) Void(_ context.Context, _ *port.VoidRequest) (*port.VoidResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockPaymentGateway) Refund(_ context.Context, _ *port.RefundRequest) (*port.RefundResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockPaymentGateway) Cancel(_ context.Context, _ *port.CancelRequest) (*port.CancelResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockPaymentGateway) GetTransaction(_ context.Context, _ string) (*port.Transaction, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockPaymentGateway) RegisterPaymentMethod(_ context.Context, _ *port.RegisterPaymentMethodRequest) (*port.PaymentMethodDetail, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockPaymentGateway) DeletePaymentMethod(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}
func (g *mockPaymentGateway) GetPaymentMethod(_ context.Context, _ string) (*port.PaymentMethodDetail, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockPaymentGateway) ListPaymentMethods(_ context.Context, _ string) ([]*port.PaymentMethodDetail, error) {
	return nil, fmt.Errorf("not implemented")
}
