// hosting-integration-demo shows how Contract-to-Cash integrates with
// an external service (hosting/server provisioning) via the plugin hook system.
//
// Scenario: A rental server business where:
//   - Contract activated + payment completed -> provision a server
//   - Contract suspended (payment overdue)   -> stop the server
//   - Contract resumed                       -> restart the server
//   - Contract cancelled                     -> terminate the server
//
// The "ServerProvisioningPlugin" implements multiple hook interfaces to
// react to contract lifecycle events and coordinate with an external system.
// This pattern works for any service: cloud instances, SaaS seats, domain
// registration, CDN, etc.
//
// Run: go run ./examples/hosting-integration-demo/
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/credit"
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

	fmt.Println("=== Hosting Integration Demo ===")
	fmt.Println("Scenario: Rental server business with automated provisioning")
	fmt.Println()

	// ── 1. Infrastructure ──
	es := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(es, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()
	creditRepo := inmemory.NewInMemoryCreditRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()

	// ── 2. Register plugins ──
	registry := plugin.NewRegistry()

	// Core: tax calculation
	must("register tax", registry.Register(tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})))

	// Integration: server provisioning (the star of this demo)
	serverMgr := newServerManager()
	provPlugin := newServerProvisioningPlugin(serverMgr)
	must("register provisioning", registry.Register(provPlugin))

	must("init plugins", registry.InitializeAll(ctx, map[string]plugin.Config{
		"tax":                 {"priority": plugin.PriorityLow},
		"server-provisioning": {"priority": plugin.PriorityNormal},
	}))
	defer registry.ShutdownAll(ctx)

	metadata := eventstore.EventMetadata{UserID: "customer-tanaka"}
	contractID := shared.NewContractID()
	accountID := shared.AccountID("acct-tanaka-001")

	// ── 3. Customer signs up for a hosting plan ──
	printSection("Phase 1: Customer Sign-up")

	priceEntity := pricing.NewPrice(shared.NewProductID(), moneyJPY(5000), shared.CurrencyJPY, pricing.BillingCycleMonthly, nil)
	must("save price", priceRepo.Save(ctx, priceEntity))

	agg := contract.NewContractAggregate(contractID, clock)
	must("create", agg.Create(contract.CreateContractCommand{
		AccountID:    accountID,
		PlanID:       shared.PlanID("plan-vps-standard"),
		PriceID:      priceEntity.ID(),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        moneyJPY(5000),
		BasePrice:    moneyJPY(5000),
		AutoRenew:    true,
	}, metadata))

	// Fire OnContractCreate hooks
	for _, h := range registry.GetOnContractCreateHooks() {
		must("hook:create", h.OnContractCreate(plugin.NewContext(ctx), agg))
	}
	must("save", contractRepo.Save(ctx, agg))

	// ── 4. Activate contract ──
	must("activate", agg.Activate(metadata))
	for _, h := range registry.GetOnContractActivateHooks() {
		must("hook:activate", h.OnContractActivate(plugin.NewContext(ctx), agg))
	}
	must("save", contractRepo.Save(ctx, agg))

	// ── 5. First invoice + payment -> server provisioned ──
	printSection("Phase 2: First Payment & Server Provisioning")

	billingService := service.NewBillingService(
		contractRepo, invoiceRepo, usageRepo, creditRepo,
		credit.CreditConfig{}, priceRepo, productRepo, registry,
		service.BillingConfig{DaysUntilDue: 30}, clock,
	)
	inv, err := billingService.GenerateInvoice(ctx, contractID, agg.CurrentPeriod())
	if err != nil {
		fatal("generate invoice", err)
	}
	must("finalize", inv.Finalize())
	must("save invoice", invoiceRepo.Save(ctx, inv))

	fmt.Printf("  Invoice: ¥%s (subtotal ¥%s + tax ¥%s)\n",
		inv.Total().Amount().RatString(),
		inv.Subtotal().Amount().RatString(),
		inv.TaxAmount().Amount().RatString())

	gateway := &mockPaymentGateway{clock: clock}
	paymentService := service.NewPaymentService(gateway, paymentRepo, invoiceRepo, contractRepo, nil, es, registry, clock)
	pmt, err := paymentService.ProcessPayment(ctx, inv.ID(), service.ProcessPaymentInput{
		PaymentMethodID: "pm-visa-tanaka",
		Amount:          inv.AmountDue(),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "pay-001",
	})
	if err != nil {
		fatal("payment", err)
	}
	fmt.Printf("  Payment: ¥%s -> %s\n\n", pmt.Amount().Amount().RatString(), pmt.Status())

	// AfterCharge hooks fire the provisioning
	for _, h := range registry.GetAfterChargeHooks() {
		must("hook:after-charge", h.AfterCharge(plugin.NewPaymentContext(ctx, pmt, inv)))
	}

	serverMgr.PrintStatus()

	// ── 6. Month 2: renewal payment fails -> suspend ──
	printSection("Phase 3: Payment Failed -> Server Suspended")

	clock.Advance(30 * 24 * time.Hour) // May 1
	gateway.failNext = true            // simulate payment failure

	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("renew", agg.Renew(metadata))
	must("save", contractRepo.Save(ctx, agg))

	inv2, _ := billingService.GenerateInvoice(ctx, contractID, agg.CurrentPeriod())
	must("finalize", inv2.Finalize())
	must("save invoice", invoiceRepo.Save(ctx, inv2))

	_, err = paymentService.ProcessPayment(ctx, inv2.ID(), service.ProcessPaymentInput{
		PaymentMethodID: "pm-visa-tanaka",
		Amount:          inv2.AmountDue(),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "pay-002",
	})
	fmt.Printf("  Payment failed: %v\n\n", err)

	// Payment failed -> suspend the contract
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("suspend", agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingSkip,
		Reason:          "payment overdue",
	}, metadata))
	for _, h := range registry.GetOnContractSuspendHooks() {
		must("hook:suspend", h.OnContractSuspend(plugin.NewContext(ctx), agg))
	}
	must("save", contractRepo.Save(ctx, agg))

	serverMgr.PrintStatus()

	// ── 7. Customer updates payment method and pays -> resume ──
	printSection("Phase 4: Payment Retry -> Server Resumed")

	clock.Advance(3 * 24 * time.Hour) // May 4
	gateway.failNext = false          // payment method updated

	pmt2, err := paymentService.ProcessPayment(ctx, inv2.ID(), service.ProcessPaymentInput{
		PaymentMethodID: "pm-visa-tanaka-new",
		Amount:          inv2.AmountDue(),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "pay-002-retry",
	})
	if err != nil {
		fatal("retry payment", err)
	}
	fmt.Printf("  Payment retry: ¥%s -> %s\n\n", pmt2.Amount().Amount().RatString(), pmt2.Status())

	// Payment succeeded -> resume the contract
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("resume", agg.Resume(metadata))
	for _, h := range registry.GetOnContractResumeHooks() {
		must("hook:resume", h.OnContractResume(plugin.NewContext(ctx), agg))
	}
	must("save", contractRepo.Save(ctx, agg))

	for _, h := range registry.GetAfterChargeHooks() {
		must("hook:after-charge", h.AfterCharge(plugin.NewPaymentContext(ctx, pmt2, inv2)))
	}

	serverMgr.PrintStatus()

	// ── 8. Customer cancels -> server terminated ──
	printSection("Phase 5: Cancellation -> Server Terminated")

	clock.Advance(20 * 24 * time.Hour)
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("cancel", agg.Cancel("switching to competitor", metadata))
	for _, h := range registry.GetOnContractCancelHooks() {
		must("hook:cancel", h.OnContractCancel(plugin.NewContext(ctx), agg))
	}
	must("save", contractRepo.Save(ctx, agg))

	serverMgr.PrintStatus()

	// ── Summary ──
	printSection("Summary: Event-Driven Integration Flow")
	fmt.Println("  Contract Event          Plugin Hook                Server Action")
	fmt.Println("  " + strings.Repeat("-", 70))
	fmt.Println("  Contract Created     -> OnContractCreate        -> (prepare resources)")
	fmt.Println("  Contract Activated   -> OnContractActivate      -> (mark ready)")
	fmt.Println("  Payment Completed    -> AfterCharge             -> Provision server")
	fmt.Println("  Payment Failed       -> OnPaymentFailed         -> (alert)")
	fmt.Println("  Contract Suspended   -> OnContractSuspend       -> Stop server")
	fmt.Println("  Contract Resumed     -> OnContractResume        -> Restart server")
	fmt.Println("  Payment Completed    -> AfterCharge             -> (confirm active)")
	fmt.Println("  Contract Cancelled   -> OnContractCancel        -> Terminate server")
	fmt.Println()
	fmt.Println("  The plugin system makes this integration:")
	fmt.Println("    - Declarative: just implement the hook interfaces")
	fmt.Println("    - Composable: multiple plugins can react to the same event")
	fmt.Println("    - Decoupled: billing core knows nothing about servers")
	fmt.Println()

	// ── Server Operation Log ──
	printSection("Server Operation Log (Full Audit Trail)")
	for _, entry := range serverMgr.log {
		fmt.Printf("  [%s] %s\n", entry.at.Format("2006-01-02"), entry.message)
	}

	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}

// ═══════════════════════════════════════════════════════════════════
// ServerManager - simulates an external server management system
// (In production: API calls to cloud provider, Kubernetes, etc.)
// ═══════════════════════════════════════════════════════════════════

type serverState string

const (
	serverStateNone         serverState = "none"
	serverStatePending      serverState = "pending"
	serverStateProvisioning serverState = "provisioning"
	serverStateRunning      serverState = "running"
	serverStateStopped      serverState = "stopped"
	serverStateTerminated   serverState = "terminated"
)

type logEntry struct {
	at      time.Time
	message string
}

type serverManager struct {
	mu      sync.Mutex
	servers map[string]serverState
	log     []logEntry
}

func newServerManager() *serverManager {
	return &serverManager{
		servers: make(map[string]serverState),
	}
}

func (m *serverManager) Provision(contractID string, planID string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[contractID] = serverStateRunning
	msg := fmt.Sprintf("SERVER PROVISIONED: contract=%s plan=%s -> allocating CPU/RAM/disk, installing OS, configuring network",
		contractID[:12]+"...", planID)
	m.log = append(m.log, logEntry{at: at, message: msg})
	fmt.Printf("  >> [ServerManager] %s\n", msg)
}

func (m *serverManager) Stop(contractID string, reason string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[contractID] = serverStateStopped
	msg := fmt.Sprintf("SERVER STOPPED: contract=%s reason=%q -> VM suspended, data preserved",
		contractID[:12]+"...", reason)
	m.log = append(m.log, logEntry{at: at, message: msg})
	fmt.Printf("  >> [ServerManager] %s\n", msg)
}

func (m *serverManager) Restart(contractID string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[contractID] = serverStateRunning
	msg := fmt.Sprintf("SERVER RESTARTED: contract=%s -> VM resumed, services starting",
		contractID[:12]+"...")
	m.log = append(m.log, logEntry{at: at, message: msg})
	fmt.Printf("  >> [ServerManager] %s\n", msg)
}

func (m *serverManager) Terminate(contractID string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[contractID] = serverStateTerminated
	msg := fmt.Sprintf("SERVER TERMINATED: contract=%s -> VM deleted, backup created, IP released",
		contractID[:12]+"...")
	m.log = append(m.log, logEntry{at: at, message: msg})
	fmt.Printf("  >> [ServerManager] %s\n", msg)
}

func (m *serverManager) GetState(contractID string) serverState {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servers[contractID]
	if !ok {
		return serverStateNone
	}
	return s
}

func (m *serverManager) PrintStatus() {
	m.mu.Lock()
	defer m.mu.Unlock()
	fmt.Println()
	for id, state := range m.servers {
		icon := map[serverState]string{
			serverStateRunning:    "🟢",
			serverStateStopped:    "🔴",
			serverStateTerminated: "⚫",
			serverStatePending:    "🟡",
		}[state]
		fmt.Printf("  %s Server [%s...]: %s\n", icon, id[:12], state)
	}
	fmt.Println()
}

// ═══════════════════════════════════════════════════════════════════
// ServerProvisioningPlugin - bridges billing events to server management
// Implements: OnContractCreate, OnContractActivate, OnContractSuspend,
//             OnContractResume, OnContractCancel, AfterCharge, OnPaymentFailed
// ═══════════════════════════════════════════════════════════════════

type serverProvisioningPlugin struct {
	mgr      *serverManager
	priority int
	// In production, you'd look up the contract from the invoice via a repository.
	// For this demo, we track the active contract ID directly.
	activeContractID shared.ContractID
}

func newServerProvisioningPlugin(mgr *serverManager) *serverProvisioningPlugin {
	return &serverProvisioningPlugin{mgr: mgr}
}

// Plugin interface
func (p *serverProvisioningPlugin) Name() string    { return "server-provisioning" }
func (p *serverProvisioningPlugin) Version() string { return "1.0.0" }
func (p *serverProvisioningPlugin) Priority() int   { return p.priority }
func (p *serverProvisioningPlugin) Initialize(_ context.Context, config plugin.Config) error {
	if v, ok := config["priority"]; ok {
		if n, ok := v.(int); ok {
			p.priority = n
		}
	}
	return nil
}
func (p *serverProvisioningPlugin) Shutdown(_ context.Context) error { return nil }

// OnContractCreateHook - prepare (but don't provision yet)
func (p *serverProvisioningPlugin) OnContractCreate(_ *plugin.Context, c *contract.ContractAggregate) error {
	p.activeContractID = c.ContractID()
	fmt.Printf("  >> [Provisioning] Contract created: %s (plan: %s) - awaiting payment\n",
		c.ContractID(), c.PlanID())
	return nil
}

// OnContractActivateHook - mark as ready for provisioning
func (p *serverProvisioningPlugin) OnContractActivate(_ *plugin.Context, c *contract.ContractAggregate) error {
	fmt.Printf("  >> [Provisioning] Contract activated: %s - ready for provisioning after payment\n",
		c.ContractID())
	return nil
}

// AfterChargeHook - payment succeeded -> provision or confirm server
func (p *serverProvisioningPlugin) AfterCharge(ctx *plugin.PaymentContext) error {
	// PaymentContext provides type-safe access to invoice and contract
	cid := string(p.activeContractID)
	state := p.mgr.GetState(cid)
	if state == serverStateNone || state == serverStatePending {
		p.mgr.Provision(cid, "vps-standard", time.Now())
	}
	return nil
}

// OnContractSuspendHook - stop the server
func (p *serverProvisioningPlugin) OnContractSuspend(_ *plugin.Context, c *contract.ContractAggregate) error {
	reason := "unknown"
	if c.SuspensionConfig() != nil {
		reason = c.SuspensionConfig().Reason
	}
	p.mgr.Stop(string(c.ContractID()), reason, c.UpdatedAt())
	return nil
}

// OnContractResumeHook - restart the server
func (p *serverProvisioningPlugin) OnContractResume(_ *plugin.Context, c *contract.ContractAggregate) error {
	p.mgr.Restart(string(c.ContractID()), c.UpdatedAt())
	return nil
}

// OnContractCancelHook - terminate the server
func (p *serverProvisioningPlugin) OnContractCancel(_ *plugin.Context, c *contract.ContractAggregate) error {
	p.mgr.Terminate(string(c.ContractID()), c.UpdatedAt())
	return nil
}

// ── Mock PaymentGateway ──

type mockPaymentGateway struct {
	clock    *advancingClock
	failNext bool
}

func (g *mockPaymentGateway) ID() string { return "mock-gw" }
func (g *mockPaymentGateway) SupportedMethods() []port.PaymentMethodType {
	return []port.PaymentMethodType{port.PaymentMethodTypeCreditCard}
}
func (g *mockPaymentGateway) Charge(_ context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
	if g.failNext {
		g.failNext = false
		return nil, fmt.Errorf("card declined: insufficient funds")
	}
	return &port.ChargeResponse{
		TransactionID: "txn-" + req.IdempotencyKey,
		Status:        port.TransactionStatusCaptured,
		Amount:        req.Amount,
		CreatedAt:     g.clock.Now(),
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

// ── Helpers ──

type advancingClock struct{ current time.Time }

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

func printSection(title string) {
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  %s\n", title)
	fmt.Println(strings.Repeat("─", 60))
}
