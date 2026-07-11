package service

import (
	"context"
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// countingGateway embeds mockGateway and records how many times Charge is
// called, so a veto test can assert the gateway was never touched.
type countingGateway struct {
	mockGateway
	chargeCalls int
}

func (g *countingGateway) Charge(ctx context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
	g.chargeCalls++
	return g.mockGateway.Charge(ctx, req)
}

// panicBeforeChargePlugin panics inside the veto-capable BeforeCharge hook.
type panicBeforeChargePlugin struct{}

func (p *panicBeforeChargePlugin) Name() string                                        { return "panic-before-charge" }
func (p *panicBeforeChargePlugin) Version() string                                     { return "1.0.0" }
func (p *panicBeforeChargePlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *panicBeforeChargePlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *panicBeforeChargePlugin) Priority() int                                       { return 100 }
func (p *panicBeforeChargePlugin) BeforeCharge(_ *plugin.PaymentContext, _ shared.Money) error {
	panic("before-charge exploded")
}

// panicAfterChargePlugin panics inside the non-fatal AfterCharge hook, which
// fires AFTER the gateway charge succeeded and the payment was persisted.
type panicAfterChargePlugin struct{}

func (p *panicAfterChargePlugin) Name() string                                        { return "panic-after-charge" }
func (p *panicAfterChargePlugin) Version() string                                     { return "1.0.0" }
func (p *panicAfterChargePlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *panicAfterChargePlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *panicAfterChargePlugin) Priority() int                                       { return 100 } // before the spy (default)
func (p *panicAfterChargePlugin) AfterCharge(_ *plugin.PaymentContext) error {
	panic("after-charge exploded")
}

// TestProcessPayment_BeforeChargePanic_Vetoes verifies that a panic in the
// veto-capable BeforeCharge hook aborts the payment before the gateway is
// charged, surfacing as a structured error that names the plugin.
func TestProcessPayment_BeforeChargePanic_Vetoes(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	gw := &countingGateway{}

	registry := plugin.NewRegistry()
	if err := registry.Register(&panicBeforeChargePlugin{}); err != nil {
		t.Fatalf("register: %v", err)
	}

	svc := NewPaymentService(gw, &mockPaymentRepo{}, invRepo, nil, &mockEventStore{}, registry, clock)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-panic-before",
	})
	if err == nil {
		t.Fatal("expected error from panicking BeforeCharge hook")
	}
	if pe, ok := plugin.AsPanic(err); !ok {
		t.Errorf("expected wrapped *PluginPanicError, got %T: %v", err, err)
	} else if pe.PluginName != "panic-before-charge" {
		t.Errorf("panic error should name the plugin, got %q", pe.PluginName)
	}
	if gw.chargeCalls != 0 {
		t.Errorf("gateway must NOT be charged when BeforeCharge vetoes, got %d calls", gw.chargeCalls)
	}
}

// TestProcessPayment_AfterChargePanic_NonFatal is the headline issue #193
// scenario: a customer's AfterCharge hook panics AFTER the gateway charge
// succeeded. The panic must be recovered so ProcessPayment still returns the
// recorded payment (no charged-but-unrecorded state), and a later non-fatal
// hook must still run.
func TestProcessPayment_AfterChargePanic_NonFatal(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := &mockPaymentRepo{}

	spy := &afterChargeSpyPlugin{} // priority 500 → runs after the panicking one (100)
	registry := plugin.NewRegistry()
	if err := registry.Register(&panicAfterChargePlugin{}); err != nil {
		t.Fatalf("register panic plugin: %v", err)
	}
	if err := registry.Register(spy); err != nil {
		t.Fatalf("register spy: %v", err)
	}

	svc := NewPaymentService(&mockGateway{}, paymentRepo, invRepo, nil, &mockEventStore{}, registry, clock)

	p, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-panic-after",
	})
	if err != nil {
		t.Fatalf("ProcessPayment must not fail when a non-fatal AfterCharge hook panics: %v", err)
	}
	if p == nil {
		t.Fatal("expected the recorded payment to be returned (no charged-but-unrecorded state)")
	}
	if paymentRepo.saved == nil {
		t.Error("expected the successful payment to be persisted")
	}
	if !spy.called {
		t.Error("a panic in one non-fatal hook must not prevent later AfterCharge hooks from running")
	}
}
