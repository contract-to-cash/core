package plugin

import (
	"context"
	"errors"
	"testing"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
)

// allHooksPlugin implements every one of the 20 hook interfaces so that a single
// registration can be asserted to fan out into all hook slices.
type allHooksPlugin struct {
	basePlugin
}

// Billing calculation hooks
func (p *allHooksPlugin) CalculateDiscount(ctx *CalculationContext) (shared.Money, error) {
	return shared.Zero(shared.CurrencyJPY), nil
}
func (p *allHooksPlugin) CalculateTax(ctx *CalculationContext) (shared.Money, error) {
	return shared.Zero(shared.CurrencyJPY), nil
}
func (p *allHooksPlugin) BeforeCalculation(ctx *CalculationContext) error { return nil }
func (p *allHooksPlugin) AfterCalculation(ctx *CalculationContext, inv *invoice.Invoice) error {
	return nil
}

// Contract lifecycle hooks
func (p *allHooksPlugin) OnContractCreate(ctx *Context, c *contract.ContractAggregate) error {
	return nil
}
func (p *allHooksPlugin) OnContractActivate(ctx *Context, c *contract.ContractAggregate) error {
	return nil
}
func (p *allHooksPlugin) OnContractSuspend(ctx *Context, c *contract.ContractAggregate) error {
	return nil
}
func (p *allHooksPlugin) OnContractResume(ctx *Context, c *contract.ContractAggregate) error {
	return nil
}
func (p *allHooksPlugin) OnContractCancel(ctx *Context, c *contract.ContractAggregate) error {
	return nil
}
func (p *allHooksPlugin) OnContractRenew(ctx *Context, c *contract.ContractAggregate) error {
	return nil
}
func (p *allHooksPlugin) OnContractTrialEnd(ctx *Context, c *contract.ContractAggregate, converted bool) error {
	return nil
}

// Payment hooks
func (p *allHooksPlugin) BeforeCharge(ctx *PaymentContext, amount shared.Money) error { return nil }
func (p *allHooksPlugin) AfterCharge(ctx *PaymentContext) error                       { return nil }
func (p *allHooksPlugin) OnPaymentFailed(ctx *PaymentContext, err error) error        { return nil }
func (p *allHooksPlugin) OnRefund(ctx *PaymentContext, refundAmount shared.Money) error {
	return nil
}

// Metrics hooks
func (p *allHooksPlugin) OnContractChange(ctx *Context, event ContractChangeEvent) error { return nil }
func (p *allHooksPlugin) OnInvoiceIssued(ctx *Context, inv *invoice.Invoice) error       { return nil }
func (p *allHooksPlugin) OnPaymentProcessed(ctx *PaymentContext) error                   { return nil }

// Invoice generation hooks
func (p *allHooksPlugin) BuildDocument(ctx *Context, inv *invoice.Invoice, doc *InvoiceDocument) error {
	return nil
}
func (p *allHooksPlugin) AfterRender(ctx *Context, doc *InvoiceDocument, rendered []byte) error {
	return nil
}
func (p *allHooksPlugin) AfterDelivery(ctx *Context, doc *InvoiceDocument, result *DeliveryResult) error {
	return nil
}

// Credit note hooks
func (p *allHooksPlugin) OnCreditNoteIssued(ctx *Context, cn *invoice.CreditNote) error { return nil }
func (p *allHooksPlugin) OnInvoiceRevised(ctx *Context, original, replacement *invoice.Invoice) error {
	return nil
}

// Compile-time assertions that allHooksPlugin implements every hook interface.
var (
	_ DiscountHook           = (*allHooksPlugin)(nil)
	_ TaxHook                = (*allHooksPlugin)(nil)
	_ InvoiceLifecycleHook   = (*allHooksPlugin)(nil)
	_ OnContractCreateHook   = (*allHooksPlugin)(nil)
	_ OnContractActivateHook = (*allHooksPlugin)(nil)
	_ OnContractSuspendHook  = (*allHooksPlugin)(nil)
	_ OnContractResumeHook   = (*allHooksPlugin)(nil)
	_ OnContractCancelHook   = (*allHooksPlugin)(nil)
	_ OnContractRenewHook    = (*allHooksPlugin)(nil)
	_ OnContractTrialEndHook = (*allHooksPlugin)(nil)
	_ BeforeChargeHook       = (*allHooksPlugin)(nil)
	_ AfterChargeHook        = (*allHooksPlugin)(nil)
	_ OnPaymentFailedHook    = (*allHooksPlugin)(nil)
	_ OnRefundHook           = (*allHooksPlugin)(nil)
	_ OnContractChangeHook   = (*allHooksPlugin)(nil)
	_ OnInvoiceIssuedHook    = (*allHooksPlugin)(nil)
	_ OnPaymentProcessedHook = (*allHooksPlugin)(nil)
	_ InvoiceGenerationHook  = (*allHooksPlugin)(nil)
	_ OnCreditNoteIssuedHook = (*allHooksPlugin)(nil)
	_ OnInvoiceRevisedHook   = (*allHooksPlugin)(nil)
)

// TestRegister_DistributesToAllHookSlices verifies that a plugin implementing all
// 20 hook interfaces is registered into every corresponding getter's slice.
func TestRegister_DistributesToAllHookSlices(t *testing.T) {
	r := NewRegistry()
	p := &allHooksPlugin{
		basePlugin: basePlugin{name: "all", version: "1.0.0", priority: PriorityNormal},
	}
	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []struct {
		name string
		n    int
	}{
		{"discount", len(r.GetDiscountHooks())},
		{"tax", len(r.GetTaxHooks())},
		{"invoiceLifecycle", len(r.GetInvoiceLifecycleHooks())},
		{"onContractCreate", len(r.GetOnContractCreateHooks())},
		{"onContractActivate", len(r.GetOnContractActivateHooks())},
		{"onContractSuspend", len(r.GetOnContractSuspendHooks())},
		{"onContractResume", len(r.GetOnContractResumeHooks())},
		{"onContractCancel", len(r.GetOnContractCancelHooks())},
		{"onContractRenew", len(r.GetOnContractRenewHooks())},
		{"onContractTrialEnd", len(r.GetOnContractTrialEndHooks())},
		{"beforeCharge", len(r.GetBeforeChargeHooks())},
		{"afterCharge", len(r.GetAfterChargeHooks())},
		{"onPaymentFailed", len(r.GetOnPaymentFailedHooks())},
		{"onRefund", len(r.GetOnRefundHooks())},
		{"onContractChange", len(r.GetOnContractChangeHooks())},
		{"onInvoiceIssued", len(r.GetOnInvoiceIssuedHooks())},
		{"onPaymentProcessed", len(r.GetOnPaymentProcessedHooks())},
		{"invoiceGeneration", len(r.GetInvoiceGenerationHooks())},
		{"onCreditNoteIssued", len(r.GetOnCreditNoteIssuedHooks())},
		{"onInvoiceRevised", len(r.GetOnInvoiceRevisedHooks())},
	}
	if len(checks) != 20 {
		t.Fatalf("expected 20 hook categories, got %d", len(checks))
	}
	for _, c := range checks {
		if c.n != 1 {
			t.Errorf("hook %q: expected 1 registered hook, got %d", c.name, c.n)
		}
	}
}

// TestRegister_NarrowPluginOnlyPopulatesImplementedHooks verifies that a plugin
// implementing a single hook interface does not leak into unrelated slices.
func TestRegister_NarrowPluginOnlyPopulatesImplementedHooks(t *testing.T) {
	r := NewRegistry()
	p := &discountOnlyPlugin{
		basePlugin: basePlugin{name: "discount-only", version: "1.0.0", priority: PriorityNormal},
	}
	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(r.GetDiscountHooks()); got != 1 {
		t.Errorf("expected 1 discount hook, got %d", got)
	}
	// All other categories must remain empty.
	empties := []int{
		len(r.GetTaxHooks()),
		len(r.GetInvoiceLifecycleHooks()),
		len(r.GetOnContractCreateHooks()),
		len(r.GetOnContractActivateHooks()),
		len(r.GetOnContractSuspendHooks()),
		len(r.GetOnContractResumeHooks()),
		len(r.GetOnContractCancelHooks()),
		len(r.GetOnContractRenewHooks()),
		len(r.GetOnContractTrialEndHooks()),
		len(r.GetBeforeChargeHooks()),
		len(r.GetAfterChargeHooks()),
		len(r.GetOnPaymentFailedHooks()),
		len(r.GetOnRefundHooks()),
		len(r.GetOnContractChangeHooks()),
		len(r.GetOnInvoiceIssuedHooks()),
		len(r.GetOnPaymentProcessedHooks()),
		len(r.GetInvoiceGenerationHooks()),
		len(r.GetOnCreditNoteIssuedHooks()),
		len(r.GetOnInvoiceRevisedHooks()),
	}
	for i, n := range empties {
		if n != 0 {
			t.Errorf("unrelated hook slice #%d: expected 0, got %d", i, n)
		}
	}
}

// TestGetHooks_ReturnSortedCopy verifies the getters return priority-sorted copies
// and that mutating the returned slice does not affect the registry's internal state.
func TestGetHooks_ReturnSortedCopy(t *testing.T) {
	r := NewRegistry()
	low := &discountOnlyPlugin{basePlugin: basePlugin{name: "low", priority: PriorityLow}}
	high := &discountOnlyPlugin{basePlugin: basePlugin{name: "high", priority: PriorityHigh}}
	normal := &discountOnlyPlugin{basePlugin: basePlugin{name: "normal", priority: PriorityNormal}}

	// Register out of priority order.
	for _, p := range []*discountOnlyPlugin{low, normal, high} {
		if err := r.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.name, err)
		}
	}

	hooks := r.GetDiscountHooks()
	wantOrder := []string{"high", "normal", "low"}
	if len(hooks) != len(wantOrder) {
		t.Fatalf("expected %d hooks, got %d", len(wantOrder), len(hooks))
	}
	for i, name := range wantOrder {
		if hooks[i].Name() != name {
			t.Errorf("position %d: expected %q, got %q", i, name, hooks[i].Name())
		}
	}

	// Mutate the returned copy; the registry must be unaffected.
	hooks[0] = nil
	again := r.GetDiscountHooks()
	if again[0] == nil {
		t.Fatal("mutating returned slice affected registry internal state")
	}
	if again[0].Name() != "high" {
		t.Errorf("expected first hook still %q after mutation, got %q", "high", again[0].Name())
	}
}

// TestGetHooks_EmptyReturnsNil verifies getters return nil for empty categories.
func TestGetHooks_EmptyReturnsNil(t *testing.T) {
	r := NewRegistry()
	if got := r.GetTaxHooks(); got != nil {
		t.Errorf("expected nil for empty tax hooks, got %v", got)
	}
	if got := r.GetInvoiceGenerationHooks(); got != nil {
		t.Errorf("expected nil for empty invoice generation hooks, got %v", got)
	}
}

// failInitPlugin returns an error from Initialize.
type failInitPlugin struct {
	basePlugin
	initErr error
}

func (p *failInitPlugin) Initialize(ctx context.Context, config Config) error { return p.initErr }

// failShutdownPlugin returns an error from Shutdown.
type failShutdownPlugin struct {
	basePlugin
	shutdownErr error
}

func (p *failShutdownPlugin) Shutdown(ctx context.Context) error { return p.shutdownErr }

func TestInitializeAll_PropagatesError(t *testing.T) {
	r := NewRegistry()
	sentinel := errors.New("init boom")
	p := &failInitPlugin{
		basePlugin: basePlugin{name: "bad-init", priority: PriorityNormal},
		initErr:    sentinel,
	}
	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err := r.InitializeAll(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from InitializeAll, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected wrapped sentinel error, got %v", err)
	}
}

func TestInitializeAll_NilConfigUsesEmpty(t *testing.T) {
	r := NewRegistry()
	p := &discountOnlyPlugin{basePlugin: basePlugin{name: "no-config", priority: PriorityNormal}}
	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Pass nil configs map; Initialize should still be called with an empty Config.
	if err := r.InitializeAll(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.initialized {
		t.Error("expected plugin to be initialized with empty config")
	}
}

func TestShutdownAll_PropagatesError(t *testing.T) {
	r := NewRegistry()
	sentinel := errors.New("shutdown boom")
	p := &failShutdownPlugin{
		basePlugin:  basePlugin{name: "bad-shutdown", priority: PriorityNormal},
		shutdownErr: sentinel,
	}
	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err := r.ShutdownAll(context.Background())
	if err == nil {
		t.Fatal("expected error from ShutdownAll, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected wrapped sentinel error, got %v", err)
	}
}

// TestInitializeShutdownAll_PriorityOrder verifies Initialize runs in ascending
// priority order and Shutdown runs in descending priority order.
func TestInitializeShutdownAll_PriorityOrder(t *testing.T) {
	r := NewRegistry()
	var order []string
	rec := func(name string) { order = append(order, name) }

	a := &orderedPlugin{basePlugin: basePlugin{name: "a", priority: PriorityHigh}, rec: rec}
	b := &orderedPlugin{basePlugin: basePlugin{name: "b", priority: PriorityLow}, rec: rec}
	c := &orderedPlugin{basePlugin: basePlugin{name: "c", priority: PriorityNormal}, rec: rec}

	for _, p := range []*orderedPlugin{b, a, c} {
		if err := r.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.name, err)
		}
	}

	if err := r.InitializeAll(context.Background(), nil); err != nil {
		t.Fatalf("InitializeAll: %v", err)
	}
	// Ascending priority: a(High=100), c(Normal=500), b(Low=900)
	wantInit := []string{"init:a", "init:c", "init:b"}
	if !equalStrings(order, wantInit) {
		t.Errorf("init order = %v, want %v", order, wantInit)
	}

	order = nil
	if err := r.ShutdownAll(context.Background()); err != nil {
		t.Fatalf("ShutdownAll: %v", err)
	}
	// Descending priority: b, c, a
	wantShutdown := []string{"shutdown:b", "shutdown:c", "shutdown:a"}
	if !equalStrings(order, wantShutdown) {
		t.Errorf("shutdown order = %v, want %v", order, wantShutdown)
	}
}

// orderedPlugin records the order in which Initialize/Shutdown are called.
type orderedPlugin struct {
	basePlugin
	rec func(string)
}

func (p *orderedPlugin) Initialize(ctx context.Context, config Config) error {
	p.rec("init:" + p.name)
	return nil
}
func (p *orderedPlugin) Shutdown(ctx context.Context) error {
	p.rec("shutdown:" + p.name)
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
