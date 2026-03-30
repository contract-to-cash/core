// billing-demo demonstrates the recommended contract-to-cash flow
// with payment-gated provisioning:
//
//	Draft → Invoice → Activate → Suspend (awaiting payment) → Pay → Resume (service starts)
//
// This flow ensures that services are only provisioned after payment clears.
// The Suspended state serves as a unified "service not active" state for both
// initial payment pending and non-payment suspension scenarios.
//
// NOTE: This is the recommended flow, but not the only option.
// You can skip the Suspend/Resume steps for simpler use cases
// (e.g., Draft → Activate → Invoice → Pay).
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
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
	clock := shared.FixedClock{FixedTime: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}

	// ── 1. Infrastructure setup ──
	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()

	// ── 2. Plugin registry (tax: 10%) ──
	registry := plugin.NewRegistry()
	taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})
	if err := registry.Register(taxPlugin); err != nil {
		fatal("register tax plugin", err)
	}
	configs := map[string]plugin.Config{
		"tax": {"priority": plugin.PriorityLow},
	}
	if err := registry.InitializeAll(ctx, configs); err != nil {
		fatal("initialize plugins", err)
	}
	defer registry.ShutdownAll(ctx)

	// ── 3. Create a subscription contract (¥3,000/month) ──
	fmt.Println("=== Contract-to-Cash Demo (Payment-Gated Provisioning) ===")
	fmt.Println()

	contractID := shared.NewContractID()
	accountID := shared.AccountID("acct-demo-001")
	planID := shared.PlanID("plan-standard")
	price := moneyJPY(3000)
	metadata := eventstore.EventMetadata{UserID: "demo-user"}

	// Create a Price entity for this subscription
	priceEntity := pricing.NewPrice(shared.NewProductID(), price, shared.CurrencyJPY, pricing.BillingCycleMonthly, nil, clock.Now())
	must("save price", priceRepo.Save(ctx, priceEntity))

	agg := contract.NewContractAggregate(contractID, clock)
	must("create contract", agg.Create(contract.CreateContractCommand{
		AccountID:    accountID,
		PlanID:       planID,
		PriceID:      priceEntity.ID(),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        price,
		BasePrice:    price,
	}, metadata))
	must("save contract", contractRepo.Save(ctx, agg))
	printStep("1. Contract created (Draft)", "ID=%s, Status=%s, Price=¥%s/month",
		contractID, agg.Status(), agg.Price().Amount().RatString())

	// ── 4. Generate draft invoice for user confirmation ──
	billingService := service.NewBillingService(
		contractRepo, invoiceRepo, usageRepo,
		balance.BalanceConfig{
			DowngradePolicy:    balance.BalancePolicyLedger,
			CancellationPolicy: balance.BalancePolicyLedger,
		},
		priceRepo, productRepo,
		registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
		service.WithBalanceRepo(balanceRepo),
	)

	inv, err := billingService.GenerateInvoice(ctx, contractID, agg.CurrentPeriod())
	if err != nil {
		fatal("generate invoice", err)
	}
	printStep("2. Draft invoice generated",
		"ID=%s, Status=%s\n     Subtotal=¥%s, Tax(10%%)=¥%s, Total=¥%s",
		inv.ID(), inv.Status(),
		inv.Subtotal().Amount().RatString(),
		inv.TaxAmount().Amount().RatString(),
		inv.Total().Amount().RatString())

	// ── 5. User reviews and confirms → Activate contract + Finalize invoice ──
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("activate contract", agg.Activate(metadata))
	must("save contract", contractRepo.Save(ctx, agg))

	must("finalize invoice", inv.Finalize())
	must("save invoice", invoiceRepo.Save(ctx, inv))
	printStep("3. User confirmed → Activate + Finalize",
		"Contract=%s, Invoice=%s, AmountDue=¥%s",
		agg.Status(), inv.Status(), inv.AmountDue().Amount().RatString())

	// ── 6. Immediately suspend (awaiting payment) ──
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("suspend", agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingSkip,
		Reason:          "awaiting_initial_payment",
	}, metadata))
	must("save contract", contractRepo.Save(ctx, agg))
	printStep("4. Contract suspended (awaiting payment)",
		"Status=%s, Reason=%s",
		agg.Status(), "awaiting_initial_payment")

	// ── 7. Process payment ──
	gateway := &mockPaymentGateway{}
	paymentService := service.NewPaymentService(
		gateway, paymentRepo, invoiceRepo, contractRepo, eventStore, registry, clock,
	)

	payment, err := paymentService.ProcessPayment(ctx, inv.ID(), service.ProcessPaymentInput{
		PaymentMethodID: "pm-visa-1234",
		Amount:          inv.AmountDue(),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "demo-pay-001",
	})
	if err != nil {
		fatal("process payment", err)
	}
	printStep("5. Payment processed",
		"ID=%s, Status=%s, Amount=¥%s\n     GatewayTxn=%s",
		payment.ID(), payment.Status(), payment.Amount().Amount().RatString(),
		payment.GatewayTransactionID())

	// ── 8. Payment confirmed → Resume contract (service starts) ──
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("resume", agg.Resume(metadata))
	must("save contract", contractRepo.Save(ctx, agg))
	printStep("6. Payment confirmed → Contract resumed",
		"Status=%s — service is now active!", agg.Status())

	// ── 9. Verify final state ──
	finalInv, _ := invoiceRepo.FindByID(ctx, inv.ID())
	printStep("7. Final state",
		"Invoice: Status=%s, PaidAmount=¥%s, Balance=¥%s",
		finalInv.Status(),
		finalInv.PaidAmount().Amount().RatString(),
		finalInv.Balance().Amount().RatString())

	// ── 10. Show event history ──
	fmt.Println("--- Event History ---")
	events, _ := eventStore.Load(ctx, string(contractID))
	for i, e := range events {
		fmt.Printf("  [%d] %-25s (v%d) at %s\n",
			i+1, e.Type, e.Version, e.OccurredAt.Format("2006-01-02T15:04:05Z"))
	}

	fmt.Println()
	fmt.Println("=== Demo Complete ===")
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

func printStep(title, format string, args ...interface{}) {
	fmt.Printf("  %s\n", title)
	fmt.Printf("     "+format+"\n\n", args...)
}

// ── Mock PaymentGateway ──

type mockPaymentGateway struct{}

func (g *mockPaymentGateway) ID() string {
	return "mock-gateway"
}

func (g *mockPaymentGateway) SupportedMethods() []port.PaymentMethodType {
	return []port.PaymentMethodType{port.PaymentMethodTypeCreditCard}
}

func (g *mockPaymentGateway) Charge(_ context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
	return &port.ChargeResponse{
		TransactionID: "txn-mock-" + req.IdempotencyKey,
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
