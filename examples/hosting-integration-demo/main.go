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

	fmt.Println("=== Hosting Integration Demo ===")
	fmt.Println("Scenario: Rental server business with automated provisioning")
	fmt.Println()

	// ── 1. Infrastructure ──
	es := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(es, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()

	// ── 2. Register plugins ──
	registry := plugin.NewRegistry()

	// Core: tax calculation
	must("register tax", registry.Register(tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})))

	// Integration: server provisioning (the star of this demo)
	serverMgr := newServerManager()
	provPlugin := newServerProvisioningPlugin(serverMgr, clock)
	must("register provisioning", registry.Register(provPlugin))

	must("init plugins", registry.InitializeAll(ctx, map[string]plugin.Config{
		"tax":                 {"priority": plugin.PriorityLow},
		"server-provisioning": {"priority": plugin.PriorityNormal},
	}))
	defer func() {
		if err := registry.ShutdownAll(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "shutdown plugins: %v\n", err)
		}
	}()

	metadata := eventstore.EventMetadata{UserID: "customer-tanaka"}
	contractID := shared.NewContractID()
	accountID := shared.AccountID("acct-tanaka-001")

	// ── 3. Customer signs up for a hosting plan ──
	printSection("Phase 1: Customer Sign-up")

	priceEntity, priceErr := pricing.NewPrice(shared.NewProductID(), moneyJPY(5000), shared.CurrencyJPY, pricing.BillingCycleMonthly, nil, clock.Now())
	must("create price", priceErr)
	must("save price", priceRepo.Save(ctx, priceEntity))

	agg := contract.NewContractAggregate(contractID, clock)
	must("create", agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-hosting-integration-demo-demo-1",
		AccountID:      accountID,
		PriceID:        priceEntity.ID(),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          moneyJPY(5000),
		BasePrice:      moneyJPY(5000),
		AutoRenew:      true,
	}, metadata))

	// Integrator-fired lifecycle hooks follow "transition -> SAVE -> fire"
	// (docs/internals/plugin-system.md §5.3): firing before the save would
	// notify plugins about a state that may never persist. Each invocation is
	// wrapped in plugin.SafeInvoke so a panicking plugin cannot crash the
	// integration flow, and failures are non-fatal — logged via
	// plugin.LogNonFatalHookError and processing continues (§5.4).
	must("save", contractRepo.Save(ctx, agg))
	for _, h := range registry.GetOnContractCreateHooks() {
		fireNonFatal("OnContractCreateHook.OnContractCreate", h.Name(), func() error {
			return h.OnContractCreate(plugin.NewContext(ctx), agg)
		})
	}

	// ── 4. Activate contract ──
	must("activate", agg.Activate(metadata))
	must("save", contractRepo.Save(ctx, agg))
	for _, h := range registry.GetOnContractActivateHooks() {
		fireNonFatal("OnContractActivateHook.OnContractActivate", h.Name(), func() error {
			return h.OnContractActivate(plugin.NewContext(ctx), agg)
		})
	}

	// ── 5. First invoice + payment -> server provisioned ──
	printSection("Phase 2: First Payment & Server Provisioning")

	// WithoutTransactions: this demo intentionally runs on in-memory
	// repositories without a TxManager (issue #187) — the explicit opt-in
	// suppresses the "running without a transaction manager" warning.
	billingService := service.NewBillingService(
		contractRepo, invoiceRepo, usageRepo,
		balance.BalanceConfig{}, priceRepo, productRepo, registry,
		service.BillingConfig{DaysUntilDue: 30}, clock,
		service.WithBalanceRepo(balanceRepo),
		service.WithoutTransactions(),
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
	paymentService := service.NewPaymentService(gateway, paymentRepo, invoiceRepo, contractRepo, es, registry, clock,
		service.WithoutPaymentTransactions())

	// Payment hooks are CORE-fired (§5.3): ProcessPayment itself fires
	// BeforeCharge before the gateway call and AfterCharge on success, so the
	// provisioning below happens inside this call — no manual hook loop needed.
	fmt.Println("  Processing payment (PaymentService fires AfterCharge automatically)...")
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

	serverMgr.PrintStatus()

	// ── 6. Customer schedules cancellation, then changes their mind ──
	// ScheduleCancellation requires an Active contract; UnscheduleCancellation
	// requires a pending scheduled cancellation. Like the other lifecycle hooks,
	// the integrator fires OnContractCancelScheduled/OnContractCancelUnscheduled.
	printSection("Phase 3: Scheduled Cancellation -> Change of Mind")

	clock.Advance(10 * 24 * time.Hour) // April 11
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("schedule-cancel", agg.ScheduleCancellation("budget review", metadata))
	must("save", contractRepo.Save(ctx, agg))
	for _, h := range registry.GetOnContractCancelScheduledHooks() {
		fireNonFatal("OnContractCancelScheduledHook.OnContractCancelScheduled", h.Name(), func() error {
			return h.OnContractCancelScheduled(plugin.NewContext(ctx), agg)
		})
	}

	clock.Advance(2 * 24 * time.Hour) // April 13: customer decides to stay
	must("unschedule-cancel", agg.UnscheduleCancellation(metadata))
	must("save", contractRepo.Save(ctx, agg))
	for _, h := range registry.GetOnContractCancelUnscheduledHooks() {
		fireNonFatal("OnContractCancelUnscheduledHook.OnContractCancelUnscheduled", h.Name(), func() error {
			return h.OnContractCancelUnscheduled(plugin.NewContext(ctx), agg)
		})
	}

	serverMgr.PrintStatus()

	// ── 7. Month 2: renewal payment fails -> suspend ──
	printSection("Phase 4: Payment Failed -> Server Suspended")

	clock.Advance(18 * 24 * time.Hour) // May 1
	gateway.failNext = true            // simulate payment failure

	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("renew", agg.RenewWithInterval(agg.GetInterval(), metadata))
	must("save", contractRepo.Save(ctx, agg))

	inv2, _ := billingService.GenerateInvoice(ctx, contractID, agg.CurrentPeriod())
	must("finalize", inv2.Finalize())
	must("save invoice", invoiceRepo.Save(ctx, inv2))

	// On gateway failure ProcessPayment fires OnPaymentFailedHook (core-fired,
	// non-fatal) — the plugin's alert below is printed from inside this call.
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
	must("save", contractRepo.Save(ctx, agg))
	for _, h := range registry.GetOnContractSuspendHooks() {
		fireNonFatal("OnContractSuspendHook.OnContractSuspend", h.Name(), func() error {
			return h.OnContractSuspend(plugin.NewContext(ctx), agg)
		})
	}

	serverMgr.PrintStatus()

	// ── 8. Customer updates payment method and pays -> resume ──
	printSection("Phase 5: Payment Retry -> Server Resumed")

	clock.Advance(3 * 24 * time.Hour) // May 4
	gateway.failNext = false          // payment method updated

	// ProcessPayment again fires AfterCharge automatically. The server is
	// already provisioned (just stopped), so the plugin takes no provisioning
	// action here — the restart is driven by the Resume hook below.
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
	must("save", contractRepo.Save(ctx, agg))
	for _, h := range registry.GetOnContractResumeHooks() {
		fireNonFatal("OnContractResumeHook.OnContractResume", h.Name(), func() error {
			return h.OnContractResume(plugin.NewContext(ctx), agg)
		})
	}

	serverMgr.PrintStatus()

	// ── 9. Customer cancels -> server terminated ──
	printSection("Phase 6: Cancellation -> Server Terminated")

	clock.Advance(20 * 24 * time.Hour)
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("cancel", agg.Cancel("switching to competitor", metadata))
	must("save", contractRepo.Save(ctx, agg))
	for _, h := range registry.GetOnContractCancelHooks() {
		fireNonFatal("OnContractCancelHook.OnContractCancel", h.Name(), func() error {
			return h.OnContractCancel(plugin.NewContext(ctx), agg)
		})
	}

	serverMgr.PrintStatus()

	// ── Summary ──
	printSection("Summary: Event-Driven Integration Flow")
	fmt.Println("  Contract Event          Plugin Hook                Server Action")
	fmt.Println("  " + strings.Repeat("-", 70))
	fmt.Println("  Contract Created     -> OnContractCreate        -> (prepare resources)")
	fmt.Println("  Contract Activated   -> OnContractActivate      -> (mark ready)")
	fmt.Println("  Payment Completed    -> AfterCharge             -> Provision server (once)")
	fmt.Println("  Cancel Scheduled     -> OnContractCancelScheduled   -> (flag decommission)")
	fmt.Println("  Cancel Unscheduled   -> OnContractCancelUnscheduled -> (clear flag)")
	fmt.Println("  Payment Failed       -> OnPaymentFailed         -> (alert)")
	fmt.Println("  Contract Suspended   -> OnContractSuspend       -> Stop server")
	fmt.Println("  Payment Retried      -> AfterCharge             -> (already provisioned)")
	fmt.Println("  Contract Resumed     -> OnContractResume        -> Restart server")
	fmt.Println("  Contract Cancelled   -> OnContractCancel        -> Terminate server")
	fmt.Println()
	fmt.Println("  Firing responsibility (docs/internals/plugin-system.md §5.3):")
	fmt.Println("    - Payment hooks (AfterCharge/OnPaymentFailed) are fired by the CORE")
	fmt.Println("      inside PaymentService.ProcessPayment — never fire them manually.")
	fmt.Println("    - Contract lifecycle hooks are fired by the INTEGRATOR, after the")
	fmt.Println("      state transition is saved, via plugin.SafeInvoke (non-fatal).")
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

func (m *serverManager) Provision(contractID string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[contractID] = serverStateRunning
	msg := fmt.Sprintf("SERVER PROVISIONED: contract=%s -> allocating CPU/RAM/disk, installing OS, configuring network",
		contractID[:12]+"...")
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
//             OnContractResume, OnContractCancel, OnContractCancelScheduled,
//             OnContractCancelUnscheduled, AfterCharge, OnPaymentFailed
// ═══════════════════════════════════════════════════════════════════

// Compile-time interface checks.
var (
	_ plugin.OnContractCreateHook            = (*serverProvisioningPlugin)(nil)
	_ plugin.OnContractActivateHook          = (*serverProvisioningPlugin)(nil)
	_ plugin.OnContractSuspendHook           = (*serverProvisioningPlugin)(nil)
	_ plugin.OnContractResumeHook            = (*serverProvisioningPlugin)(nil)
	_ plugin.OnContractCancelHook            = (*serverProvisioningPlugin)(nil)
	_ plugin.OnContractCancelScheduledHook   = (*serverProvisioningPlugin)(nil)
	_ plugin.OnContractCancelUnscheduledHook = (*serverProvisioningPlugin)(nil)
	_ plugin.AfterChargeHook                 = (*serverProvisioningPlugin)(nil)
	_ plugin.OnPaymentFailedHook             = (*serverProvisioningPlugin)(nil)
)

type serverProvisioningPlugin struct {
	mgr      *serverManager
	clock    shared.Clock
	priority int
	// In production, you'd look up the contract from the invoice via a repository.
	// For this demo, we track the active contract ID directly.
	activeContractID shared.ContractID
}

func newServerProvisioningPlugin(mgr *serverManager, clock shared.Clock) *serverProvisioningPlugin {
	return &serverProvisioningPlugin{mgr: mgr, clock: clock}
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
	fmt.Printf("  >> [Provisioning] Contract created: %s (price: %s) - awaiting payment\n",
		c.ContractID(), c.PriceID())
	return nil
}

// OnContractActivateHook - mark as ready for provisioning
func (p *serverProvisioningPlugin) OnContractActivate(_ *plugin.Context, c *contract.ContractAggregate) error {
	fmt.Printf("  >> [Provisioning] Contract activated: %s - ready for provisioning after payment\n",
		c.ContractID())
	return nil
}

// AfterChargeHook - payment succeeded -> provision or confirm server.
// Fired automatically by PaymentService.ProcessPayment (§5.3); provisioning
// happens exactly once because it only triggers from the none/pending states.
func (p *serverProvisioningPlugin) AfterCharge(_ *plugin.PaymentContext) error {
	// PaymentContext provides type-safe access to invoice and contract
	cid := string(p.activeContractID)
	state := p.mgr.GetState(cid)
	if state == serverStateNone || state == serverStatePending {
		p.mgr.Provision(cid, p.clock.Now())
		return nil
	}
	fmt.Printf("  >> [Provisioning] Payment confirmed: %s... (server: %s) - no provisioning needed\n",
		cid[:12], state)
	return nil
}

// OnPaymentFailedHook - alert on payment failure (fired automatically by
// PaymentService.ProcessPayment when the gateway declines; non-fatal).
func (p *serverProvisioningPlugin) OnPaymentFailed(_ *plugin.PaymentContext, cause error) error {
	fmt.Printf("  >> [Provisioning] ALERT: payment failed for contract %s...: %v\n",
		string(p.activeContractID)[:12], cause)
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

// OnContractCancelScheduledHook - flag the server for end-of-period decommission
func (p *serverProvisioningPlugin) OnContractCancelScheduled(_ *plugin.Context, c *contract.ContractAggregate) error {
	fmt.Printf("  >> [Provisioning] Cancellation scheduled: %s - server keeps running until period end\n",
		c.ContractID())
	return nil
}

// OnContractCancelUnscheduledHook - clear the pending decommission flag
func (p *serverProvisioningPlugin) OnContractCancelUnscheduled(_ *plugin.Context, c *contract.ContractAggregate) error {
	fmt.Printf("  >> [Provisioning] Cancellation unscheduled: %s - pending decommission cleared\n",
		c.ContractID())
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

// fireNonFatal invokes one integrator-fired lifecycle hook via
// plugin.FireNonFatal — panic isolation (SafeInvoke) plus non-fatal
// error/panic logging (LogNonFatalHookError, nil logger = slog.Default()),
// exactly the fatality policy docs/internals/plugin-system.md §5.4 prescribes
// for integrator-fired hooks. One misbehaving plugin cannot abort the
// integration flow or starve later hooks.
func fireNonFatal(hookType, pluginName string, fn func() error) {
	plugin.FireNonFatal(nil, hookType, pluginName, fn)
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
