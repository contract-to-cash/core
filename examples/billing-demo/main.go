// billing-demo demonstrates the full contract-to-cash flow:
// Contract creation -> Activation -> Invoice generation -> Payment processing.
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/credit"
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
	creditRepo := inmemory.NewInMemoryCreditRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()

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
	fmt.Println("=== Contract-to-Cash Demo ===")
	fmt.Println()

	contractID := shared.NewContractID()
	accountID := shared.AccountID("acct-demo-001")
	planID := shared.PlanID("plan-standard")
	price := moneyJPY(3000)
	metadata := eventstore.EventMetadata{UserID: "demo-user"}

	agg := contract.NewContractAggregate(contractID, clock)
	must("create contract", agg.Create(contract.CreateContractCommand{
		AccountID:    accountID,
		PlanID:       planID,
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        price,
		BasePrice:    price,
	}, metadata))
	printStep("1. Contract created", "ID=%s, Status=%s, Price=¥%s/month",
		contractID, agg.Status(), agg.Price().Amount().RatString())

	// ── 4. Activate the contract ──
	must("activate contract", agg.Activate(metadata))
	must("save contract", contractRepo.Save(ctx, agg))
	printStep("2. Contract activated", "Status=%s", agg.Status())

	// ── 5. Generate an invoice via BillingService ──
	billingPeriod, _ := shared.NewDateRange(
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	)
	billingService := service.NewBillingService(
		contractRepo, invoiceRepo, usageRepo, creditRepo,
		credit.CreditConfig{
			DowngradePolicy:    credit.CreditPolicyLedger,
			CancellationPolicy: credit.CreditPolicyLedger,
		},
		nil, nil, // priceRepo/productRepo: not needed for subscription type
		registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
	)

	inv, err := billingService.GenerateInvoice(ctx, contractID, billingPeriod)
	if err != nil {
		fatal("generate invoice", err)
	}
	printStep("3. Invoice generated",
		"ID=%s\n     Status=%s\n     Subtotal=¥%s, Tax(10%%)=¥%s, Total=¥%s\n     DueDate=%s",
		inv.ID(), inv.Status(),
		inv.Subtotal().Amount().RatString(),
		inv.TaxAmount().Amount().RatString(),
		inv.Total().Amount().RatString(),
		inv.DueDate().Format("2006-01-02"))

	// ── 6. Finalize the invoice ──
	must("finalize invoice", inv.Finalize())
	must("save finalized invoice", invoiceRepo.Save(ctx, inv))
	printStep("4. Invoice finalized", "Status=%s, AmountDue=¥%s",
		inv.Status(), inv.AmountDue().Amount().RatString())

	// ── 7. Process payment via PaymentService ──
	gateway := &mockPaymentGateway{}
	paymentService := service.NewPaymentService(
		gateway, paymentRepo, invoiceRepo, contractRepo, nil, eventStore, registry, clock,
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

	// ── 8. Verify final invoice status ──
	finalInv, _ := invoiceRepo.FindByID(ctx, inv.ID())
	printStep("6. Invoice updated",
		"Status=%s, PaidAmount=¥%s, Balance=¥%s",
		finalInv.Status(),
		finalInv.PaidAmount().Amount().RatString(),
		finalInv.Balance().Amount().RatString())

	// ── 9. Show event history ──
	fmt.Println()
	fmt.Println("--- Event History ---")
	events, _ := eventStore.Load(ctx, string(contractID))
	for i, e := range events {
		fmt.Printf("  [%d] %s (v%d) at %s\n",
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
