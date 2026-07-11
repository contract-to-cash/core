package service

import (
	"context"
	"strings"
	"testing"

	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// --- panicking plugins (issue #193) ---

// panicDiscountPlugin panics inside a veto-capable billing hook.
type panicDiscountPlugin struct{}

func (p *panicDiscountPlugin) Name() string                                        { return "panic-discount" }
func (p *panicDiscountPlugin) Version() string                                     { return "1.0.0" }
func (p *panicDiscountPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *panicDiscountPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *panicDiscountPlugin) Priority() int                                       { return 100 }
func (p *panicDiscountPlugin) CalculateDiscount(_ *plugin.CalculationContext) (shared.Money, error) {
	panic("discount plugin exploded")
}

// panicAfterCalcPlugin panics inside AfterCalculation, which runs inside the tx.
type panicAfterCalcPlugin struct{}

func (p *panicAfterCalcPlugin) Name() string                                        { return "panic-aftercalc" }
func (p *panicAfterCalcPlugin) Version() string                                     { return "1.0.0" }
func (p *panicAfterCalcPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *panicAfterCalcPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *panicAfterCalcPlugin) Priority() int                                       { return 100 }
func (p *panicAfterCalcPlugin) BeforeCalculation(_ *plugin.CalculationContext) error {
	return nil
}
func (p *panicAfterCalcPlugin) AfterCalculation(_ *plugin.CalculationContext, _ *invoice.Invoice) error {
	panic("aftercalc plugin exploded")
}

// panicOnInvoiceIssuedPlugin panics inside a non-fatal metrics hook.
type panicOnInvoiceIssuedPlugin struct{}

func (p *panicOnInvoiceIssuedPlugin) Name() string                                        { return "panic-issued" }
func (p *panicOnInvoiceIssuedPlugin) Version() string                                     { return "1.0.0" }
func (p *panicOnInvoiceIssuedPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *panicOnInvoiceIssuedPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *panicOnInvoiceIssuedPlugin) Priority() int                                       { return 100 } // runs before the spy (500)
func (p *panicOnInvoiceIssuedPlugin) OnInvoiceIssued(_ *plugin.Context, _ *invoice.Invoice) error {
	panic("metrics plugin exploded")
}

// TestGenerateInvoice_DiscountPanic_AbortsCleanly verifies that a panic in a
// veto-capable DiscountHook is converted to a pipeline error naming the plugin,
// and that nothing is persisted.
func TestGenerateInvoice_DiscountPanic_AbortsCleanly(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	invRepo := &mockInvoiceRepo{}

	registry := plugin.NewRegistry()
	if err := registry.Register(&panicDiscountPlugin{}); err != nil {
		t.Fatalf("register: %v", err)
	}

	svc := NewBillingService(
		&mockContractRepo{agg: agg}, invRepo, &mockUsageRepo{},
		balance.BalanceConfig{}, priceRepoFor(priceEntity), &mockProductRepo{},
		registry, BillingConfig{DaysUntilDue: 30}, clock,
	)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err == nil {
		t.Fatal("expected error from panicking discount hook")
	}
	if pe, ok := plugin.AsPanic(err); !ok {
		t.Errorf("expected wrapped *PluginPanicError, got %T: %v", err, err)
	} else if pe.PluginName != "panic-discount" {
		t.Errorf("panic error should name the plugin, got %q", pe.PluginName)
	}
	if !strings.Contains(err.Error(), "DiscountHook") {
		t.Errorf("error should mention DiscountHook, got %q", err.Error())
	}
	if invRepo.saved != nil {
		t.Error("no invoice must be saved when a veto hook panics")
	}
}

// TestGenerateInvoice_AfterCalculationPanic_AbortsTxCleanly verifies that a
// panic in AfterCalculation (which fires INSIDE the tx, before Save) unwinds as
// a returned error so the transaction aborts cleanly and nothing is persisted —
// the panic must not propagate out through tx.Run.
func TestGenerateInvoice_AfterCalculationPanic_AbortsTxCleanly(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	invRepo := &mockInvoiceRepo{}

	registry := plugin.NewRegistry()
	if err := registry.Register(&panicAfterCalcPlugin{}); err != nil {
		t.Fatalf("register: %v", err)
	}

	svc := NewBillingService(
		&mockContractRepo{agg: agg}, invRepo, &mockUsageRepo{},
		balance.BalanceConfig{}, priceRepoFor(priceEntity), &mockProductRepo{},
		registry, BillingConfig{DaysUntilDue: 30}, clock,
	)

	// Must return an error, NOT panic through GenerateInvoice.
	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err == nil {
		t.Fatal("expected error from panicking AfterCalculation hook")
	}
	if _, ok := plugin.AsPanic(err); !ok {
		t.Errorf("expected wrapped *PluginPanicError, got %T: %v", err, err)
	}
	if invRepo.saved != nil {
		t.Error("invoice must not be saved when AfterCalculation panics (tx aborts cleanly)")
	}
}

// TestFinalizeInvoice_OnInvoiceIssuedPanic_NonFatal verifies that a panic in a
// non-fatal OnInvoiceIssued hook does not fail finalization and does not stop
// the remaining hooks from running.
func TestFinalizeInvoice_OnInvoiceIssuedPanic_NonFatal(t *testing.T) {
	inv := newDraftInvoiceForFinalize(t)
	invRepo := &mockInvoiceRepo{byID: inv}

	spy := &onInvoiceIssuedSpyPlugin{} // Priority 500 → runs after the panicking one (100)
	registry := plugin.NewRegistry()
	if err := registry.Register(&panicOnInvoiceIssuedPlugin{}); err != nil {
		t.Fatalf("register panic plugin: %v", err)
	}
	if err := registry.Register(spy); err != nil {
		t.Fatalf("register spy: %v", err)
	}

	svc := newFinalizeTestService(invRepo, registry)

	got, err := svc.FinalizeInvoice(context.Background(), inv.ID())
	if err != nil {
		t.Fatalf("finalization must not fail on a non-fatal hook panic: %v", err)
	}
	if got.Status() != invoice.InvoiceStatusFinalized {
		t.Errorf("status: got %s, want finalized", got.Status())
	}
	if invRepo.saved == nil {
		t.Error("expected finalized invoice to be saved despite the hook panic")
	}
	if !spy.called {
		t.Error("a panic in one non-fatal hook must not prevent later hooks from running")
	}
}
