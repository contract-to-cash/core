// multi-service-demo shows how multiple independent service plugins coexist
// in a single billing system, each reacting only to its own contract types.
//
// Scenario: A hosting company that sells 3 products:
//   - VPS servers   (plan-vps-*)     -> ServerPlugin provisions/stops VMs
//   - SSL certs     (plan-ssl-*)     -> SSLPlugin issues/revokes certificates
//   - Domain names  (plan-domain-*)  -> DomainPlugin registers/suspends domains
//
// All three plugins are registered simultaneously. When a contract event fires,
// every plugin receives it but only the relevant one acts -- the others skip.
//
// Run: go run ./examples/multi-service-demo/
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

func main() {
	ctx := context.Background()
	clock := &advancingClock{current: time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)}

	es := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(es, clock)

	fmt.Println("=== Multi-Service Demo ===")
	fmt.Println("A hosting company with 3 products: VPS, SSL, Domain")
	fmt.Println()

	// ── Register 3 service plugins ──
	registry := plugin.NewRegistry()

	opLog := &operationLog{}
	serverPlugin := &serverProvisionPlugin{log: opLog}
	sslPlugin := &sslCertPlugin{log: opLog}
	domainPlugin := &domainRegPlugin{log: opLog}

	must("register server", registry.Register(serverPlugin))
	must("register ssl", registry.Register(sslPlugin))
	must("register domain", registry.Register(domainPlugin))

	must("init", registry.InitializeAll(ctx, map[string]plugin.Config{
		"server-provision": {"priority": plugin.PriorityNormal},
		"ssl-cert":         {"priority": plugin.PriorityNormal},
		"domain-reg":       {"priority": plugin.PriorityNormal},
	}))

	printSection("Registered Plugins (all receive every event)")
	fmt.Println("  - server-provision  (handles plan-vps-*)")
	fmt.Println("  - ssl-cert          (handles plan-ssl-*)")
	fmt.Println("  - domain-reg        (handles plan-domain-*)")
	fmt.Println()

	metadata := eventstore.EventMetadata{UserID: "customer-tanaka"}
	accountID := shared.AccountID("acct-tanaka")

	// ═══════════════════════════════════════
	// Customer buys 3 different products
	// ═══════════════════════════════════════

	printSection("Phase 1: Customer purchases 3 products")

	// ── VPS contract ──
	vpsID := shared.NewContractID()
	vps := contract.NewContractAggregate(vpsID, clock)
	must("create vps", vps.Create(contract.CreateContractCommand{
		AccountID: accountID, PlanID: "plan-vps-standard",
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        moneyJPY(5000), BasePrice: moneyJPY(5000),
	}, metadata))
	must("activate vps", vps.Activate(metadata))
	must("save vps", contractRepo.Save(ctx, vps))
	fireActivateHooks(registry, ctx, vps)

	// ── SSL contract ──
	clock.Advance(time.Minute)
	sslID := shared.NewContractID()
	ssl := contract.NewContractAggregate(sslID, clock)
	must("create ssl", ssl.Create(contract.CreateContractCommand{
		AccountID: accountID, PlanID: "plan-ssl-wildcard",
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleYearly,
		Price:        moneyJPY(20000), BasePrice: moneyJPY(20000),
	}, metadata))
	must("activate ssl", ssl.Activate(metadata))
	must("save ssl", contractRepo.Save(ctx, ssl))
	fireActivateHooks(registry, ctx, ssl)

	// ── Domain contract ──
	clock.Advance(time.Minute)
	domID := shared.NewContractID()
	dom := contract.NewContractAggregate(domID, clock)
	must("create domain", dom.Create(contract.CreateContractCommand{
		AccountID: accountID, PlanID: "plan-domain-jp",
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleYearly,
		Price:        moneyJPY(1500), BasePrice: moneyJPY(1500),
	}, metadata))
	must("activate domain", dom.Activate(metadata))
	must("save domain", contractRepo.Save(ctx, dom))
	fireActivateHooks(registry, ctx, dom)

	// ═══════════════════════════════════════
	// VPS payment fails -> only VPS is suspended
	// ═══════════════════════════════════════

	printSection("Phase 2: VPS payment fails -> only VPS suspended")

	clock.Advance(30 * 24 * time.Hour)
	vps, _ = contractRepo.FindByID(ctx, vpsID)
	must("suspend vps", vps.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingSkip,
		Reason:          "payment failed",
	}, metadata))
	must("save vps", contractRepo.Save(ctx, vps))
	fireSuspendHooks(registry, ctx, vps)

	fmt.Println()
	fmt.Println("  Note: SSL and Domain are unaffected -- plugins filter by PlanID")

	// ═══════════════════════════════════════
	// VPS payment retried -> VPS resumed
	// ═══════════════════════════════════════

	printSection("Phase 3: VPS payment succeeds -> VPS resumed")

	clock.Advance(3 * 24 * time.Hour)
	vps, _ = contractRepo.FindByID(ctx, vpsID)
	must("resume vps", vps.Resume(metadata))
	must("save vps", contractRepo.Save(ctx, vps))
	fireResumeHooks(registry, ctx, vps)

	// ═══════════════════════════════════════
	// Customer cancels domain only
	// ═══════════════════════════════════════

	printSection("Phase 4: Customer cancels domain registration only")

	clock.Advance(10 * 24 * time.Hour)
	dom, _ = contractRepo.FindByID(ctx, domID)
	must("cancel domain", dom.Cancel("no longer needed", metadata))
	must("save domain", contractRepo.Save(ctx, dom))
	fireCancelHooks(registry, ctx, dom)

	fmt.Println()
	fmt.Println("  Note: VPS and SSL continue running -- only domain was cancelled")

	// ═══════════════════════════════════════
	// Summary
	// ═══════════════════════════════════════

	printSection("Final Contract Status")

	vps, _ = contractRepo.FindByID(ctx, vpsID)
	ssl, _ = contractRepo.FindByID(ctx, sslID)
	dom, _ = contractRepo.FindByID(ctx, domID)

	fmt.Printf("  %-20s %-12s %s\n", "Product", "Status", "Plan")
	fmt.Printf("  %s\n", strings.Repeat("-", 50))
	printContractStatus("VPS Server", vps)
	printContractStatus("SSL Certificate", ssl)
	printContractStatus("Domain Name", dom)

	printSection("Full Operation Log")
	for _, entry := range opLog.entries {
		fmt.Printf("  [%s] %s\n", entry.at.Format("2006-01-02 15:04"), entry.msg)
	}

	printSection("How It Works")
	fmt.Println("  Each plugin checks PlanID prefix before acting:")
	fmt.Println()
	fmt.Println("    func (p *ServerPlugin) OnContractSuspend(ctx, c) error {")
	fmt.Println("        if !strings.HasPrefix(c.PlanID(), \"plan-vps-\") {")
	fmt.Println("            return nil  // not my responsibility")
	fmt.Println("        }")
	fmt.Println("        return p.stopServer(c)  // only VPS contracts")
	fmt.Println("    }")
	fmt.Println()
	fmt.Println("  Benefits:")
	fmt.Println("    - Each plugin is single-responsibility (one service type)")
	fmt.Println("    - Adding a new product = adding a new plugin (no core changes)")
	fmt.Println("    - Plugins are independently testable and deployable")
	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}

// ═══════════════════════════════════════════════════════════════════
// Service Plugins -- each filters by PlanID prefix
// ═══════════════════════════════════════════════════════════════════

// ── Server Provisioning Plugin ──

type serverProvisionPlugin struct {
	log      *operationLog
	priority int
}

func (p *serverProvisionPlugin) Name() string    { return "server-provision" }
func (p *serverProvisionPlugin) Version() string { return "1.0.0" }
func (p *serverProvisionPlugin) Priority() int   { return p.priority }
func (p *serverProvisionPlugin) Initialize(_ context.Context, cfg plugin.Config) error {
	if v, ok := cfg["priority"]; ok {
		if n, ok := v.(int); ok {
			p.priority = n
		}
	}
	return nil
}
func (p *serverProvisionPlugin) Shutdown(_ context.Context) error { return nil }

func (p *serverProvisionPlugin) handles(c *contract.ContractAggregate) bool {
	return strings.HasPrefix(string(c.PlanID()), "plan-vps-")
}

func (p *serverProvisionPlugin) OnContractActivate(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	msg := fmt.Sprintf("[Server] PROVISIONED VM for %s (plan: %s) -- allocating CPU/RAM/disk",
		c.ContractID(), c.PlanID())
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  🟢 %s\n", msg)
	return nil
}

func (p *serverProvisionPlugin) OnContractSuspend(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	reason := ""
	if c.SuspensionConfig() != nil {
		reason = c.SuspensionConfig().Reason
	}
	msg := fmt.Sprintf("[Server] STOPPED VM for %s -- reason: %s, data preserved",
		c.ContractID(), reason)
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  🔴 %s\n", msg)
	return nil
}

func (p *serverProvisionPlugin) OnContractResume(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	msg := fmt.Sprintf("[Server] RESTARTED VM for %s -- services coming back online",
		c.ContractID())
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  🟢 %s\n", msg)
	return nil
}

func (p *serverProvisionPlugin) OnContractCancel(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	msg := fmt.Sprintf("[Server] TERMINATED VM for %s -- backup created, resources released",
		c.ContractID())
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  ⚫ %s\n", msg)
	return nil
}

// ── SSL Certificate Plugin ──

type sslCertPlugin struct {
	log      *operationLog
	priority int
}

func (p *sslCertPlugin) Name() string    { return "ssl-cert" }
func (p *sslCertPlugin) Version() string { return "1.0.0" }
func (p *sslCertPlugin) Priority() int   { return p.priority }
func (p *sslCertPlugin) Initialize(_ context.Context, cfg plugin.Config) error {
	if v, ok := cfg["priority"]; ok {
		if n, ok := v.(int); ok {
			p.priority = n
		}
	}
	return nil
}
func (p *sslCertPlugin) Shutdown(_ context.Context) error { return nil }

func (p *sslCertPlugin) handles(c *contract.ContractAggregate) bool {
	return strings.HasPrefix(string(c.PlanID()), "plan-ssl-")
}

func (p *sslCertPlugin) OnContractActivate(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	msg := fmt.Sprintf("[SSL] ISSUED certificate for %s (plan: %s) -- generating key pair, validating domain",
		c.ContractID(), c.PlanID())
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  🔒 %s\n", msg)
	return nil
}

func (p *sslCertPlugin) OnContractSuspend(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	msg := fmt.Sprintf("[SSL] SUSPENDED certificate for %s -- marked as inactive in CA",
		c.ContractID())
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  ⚠️  %s\n", msg)
	return nil
}

func (p *sslCertPlugin) OnContractResume(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	msg := fmt.Sprintf("[SSL] REACTIVATED certificate for %s -- marked as active in CA",
		c.ContractID())
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  🔒 %s\n", msg)
	return nil
}

func (p *sslCertPlugin) OnContractCancel(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	msg := fmt.Sprintf("[SSL] REVOKED certificate for %s -- added to CRL, notified browser vendors",
		c.ContractID())
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  ❌ %s\n", msg)
	return nil
}

// ── Domain Registration Plugin ──

type domainRegPlugin struct {
	log      *operationLog
	priority int
}

func (p *domainRegPlugin) Name() string    { return "domain-reg" }
func (p *domainRegPlugin) Version() string { return "1.0.0" }
func (p *domainRegPlugin) Priority() int   { return p.priority }
func (p *domainRegPlugin) Initialize(_ context.Context, cfg plugin.Config) error {
	if v, ok := cfg["priority"]; ok {
		if n, ok := v.(int); ok {
			p.priority = n
		}
	}
	return nil
}
func (p *domainRegPlugin) Shutdown(_ context.Context) error { return nil }

func (p *domainRegPlugin) handles(c *contract.ContractAggregate) bool {
	return strings.HasPrefix(string(c.PlanID()), "plan-domain-")
}

func (p *domainRegPlugin) OnContractActivate(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	msg := fmt.Sprintf("[Domain] REGISTERED domain for %s (plan: %s) -- WHOIS updated, DNS zone created",
		c.ContractID(), c.PlanID())
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  🌐 %s\n", msg)
	return nil
}

func (p *domainRegPlugin) OnContractSuspend(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	msg := fmt.Sprintf("[Domain] SUSPENDED domain for %s -- DNS serving parking page",
		c.ContractID())
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  ⚠️  %s\n", msg)
	return nil
}

func (p *domainRegPlugin) OnContractResume(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	msg := fmt.Sprintf("[Domain] RESTORED domain for %s -- DNS records reactivated",
		c.ContractID())
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  🌐 %s\n", msg)
	return nil
}

func (p *domainRegPlugin) OnContractCancel(ctx *plugin.Context, c *contract.ContractAggregate) error {
	if !p.handles(c) {
		return nil
	}
	msg := fmt.Sprintf("[Domain] RELEASED domain for %s -- entered redemption grace period (30 days)",
		c.ContractID())
	p.log.Add(c.UpdatedAt(), msg)
	fmt.Printf("  🚫 %s\n", msg)
	return nil
}

// ═══════════════════════════════════════════════════════════════════
// Hook dispatch helpers
// ═══════════════════════════════════════════════════════════════════

func fireActivateHooks(r *plugin.Registry, ctx context.Context, c *contract.ContractAggregate) {
	pctx := plugin.NewContext(ctx)
	for _, h := range r.GetOnContractActivateHooks() {
		must("hook:activate", h.OnContractActivate(pctx, c))
	}
}

func fireSuspendHooks(r *plugin.Registry, ctx context.Context, c *contract.ContractAggregate) {
	pctx := plugin.NewContext(ctx)
	for _, h := range r.GetOnContractSuspendHooks() {
		must("hook:suspend", h.OnContractSuspend(pctx, c))
	}
}

func fireResumeHooks(r *plugin.Registry, ctx context.Context, c *contract.ContractAggregate) {
	pctx := plugin.NewContext(ctx)
	for _, h := range r.GetOnContractResumeHooks() {
		must("hook:resume", h.OnContractResume(pctx, c))
	}
}

func fireCancelHooks(r *plugin.Registry, ctx context.Context, c *contract.ContractAggregate) {
	pctx := plugin.NewContext(ctx)
	for _, h := range r.GetOnContractCancelHooks() {
		must("hook:cancel", h.OnContractCancel(pctx, c))
	}
}

// ═══════════════════════════════════════════════════════════════════
// Helpers
// ═══════════════════════════════════════════════════════════════════

type logEntry struct {
	at  time.Time
	msg string
}

type operationLog struct {
	entries []logEntry
}

func (l *operationLog) Add(at time.Time, msg string) {
	l.entries = append(l.entries, logEntry{at: at, msg: msg})
}

type advancingClock struct{ current time.Time }

func (c *advancingClock) Now() time.Time          { return c.current }
func (c *advancingClock) Advance(d time.Duration) { c.current = c.current.Add(d) }

func moneyJPY(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func must(action string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR [%s]: %v\n", action, err)
		os.Exit(1)
	}
}

func printSection(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  %s\n", title)
	fmt.Println(strings.Repeat("─", 60))
}

func printContractStatus(name string, c *contract.ContractAggregate) {
	icon := map[contract.ContractStatus]string{
		contract.ContractStatusActive:    "🟢",
		contract.ContractStatusSuspended: "🔴",
		contract.ContractStatusCancelled: "⚫",
		contract.ContractStatusDraft:     "⚪",
	}[c.Status()]
	fmt.Printf("  %s %-20s %-12s %s\n", icon, name, c.Status(), c.PlanID())
}
