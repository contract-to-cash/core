package service

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// --- Mock gateway ---

type mockGateway struct {
	failCharge bool
}

func (g *mockGateway) ID() string                                 { return "mock" }
func (g *mockGateway) SupportedMethods() []port.PaymentMethodType { return nil }
func (g *mockGateway) Charge(_ context.Context, _ *port.ChargeRequest) (*port.ChargeResponse, error) {
	if g.failCharge {
		return nil, fmt.Errorf("card declined")
	}
	return &port.ChargeResponse{
		TransactionID: "txn-001",
		Status:        port.TransactionStatusCaptured,
		Amount:        shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		CreatedAt:     time.Now(),
	}, nil
}
func (g *mockGateway) Authorize(_ context.Context, _ *port.AuthorizeRequest) (*port.AuthorizeResponse, error) {
	return nil, nil
}
func (g *mockGateway) Capture(_ context.Context, _ *port.CaptureRequest) (*port.CaptureResponse, error) {
	return nil, nil
}
func (g *mockGateway) Void(_ context.Context, _ *port.VoidRequest) (*port.VoidResponse, error) {
	return nil, nil
}
func (g *mockGateway) Refund(_ context.Context, _ *port.RefundRequest) (*port.RefundResponse, error) {
	return &port.RefundResponse{TransactionID: "refund-001"}, nil
}
func (g *mockGateway) Cancel(_ context.Context, _ *port.CancelRequest) (*port.CancelResponse, error) {
	return nil, nil
}
func (g *mockGateway) GetTransaction(_ context.Context, _ string) (*port.Transaction, error) {
	return nil, nil
}
func (g *mockGateway) RegisterPaymentMethod(_ context.Context, _ *port.RegisterPaymentMethodRequest) (*port.PaymentMethodDetail, error) {
	return nil, nil
}
func (g *mockGateway) DeletePaymentMethod(_ context.Context, _ string) error { return nil }
func (g *mockGateway) GetPaymentMethod(_ context.Context, _ string) (*port.PaymentMethodDetail, error) {
	return nil, nil
}
func (g *mockGateway) ListPaymentMethods(_ context.Context, _ string) ([]*port.PaymentMethodDetail, error) {
	return nil, nil
}

// --- Mock repos for payment tests ---

type mockPaymentRepo struct {
	saved *payment.Payment
}

func (m *mockPaymentRepo) Save(_ context.Context, p *payment.Payment) error {
	m.saved = p
	return nil
}
func (m *mockPaymentRepo) FindByID(_ context.Context, _ shared.PaymentID) (*payment.Payment, error) {
	if m.saved != nil {
		return m.saved, nil
	}
	return nil, fmt.Errorf("not found")
}
func (m *mockPaymentRepo) FindByInvoiceID(_ context.Context, _ shared.InvoiceID) ([]*payment.Payment, error) {
	return nil, nil
}

type mockInvoiceRepoForPayment struct {
	inv *invoice.Invoice
}

func (m *mockInvoiceRepoForPayment) Save(_ context.Context, inv *invoice.Invoice) error {
	m.inv = inv
	return nil
}
func (m *mockInvoiceRepoForPayment) FindByID(_ context.Context, _ shared.InvoiceID) (*invoice.Invoice, error) {
	if m.inv != nil {
		return m.inv, nil
	}
	return nil, fmt.Errorf("not found")
}
func (m *mockInvoiceRepoForPayment) FindByContractID(_ context.Context, _ shared.ContractID) ([]*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepoForPayment) FindByAccountID(_ context.Context, _ shared.AccountID) ([]*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepoForPayment) FindOverdue(_ context.Context) ([]*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepoForPayment) FindByStatus(_ context.Context, _ invoice.InvoiceStatus) ([]*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepoForPayment) FindByIDAsOf(_ context.Context, _ shared.InvoiceID, _ time.Time) (*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepoForPayment) FindByContractAndStatus(_ context.Context, _ shared.ContractID, _ invoice.InvoiceStatus) ([]*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepoForPayment) FindByContractAndPeriod(_ context.Context, _ shared.ContractID, _ shared.DateRange) ([]*invoice.Invoice, error) {
	return nil, nil
}
func (m *mockInvoiceRepoForPayment) FindUnpaidByContract(_ context.Context, _ shared.ContractID) ([]*invoice.Invoice, error) {
	return nil, nil
}

// --- Hook spy plugins ---

type afterChargeSpyPlugin struct {
	called      bool
	receivedCtx *plugin.PaymentContext
}

func (p *afterChargeSpyPlugin) Name() string                                        { return "after-charge-spy" }
func (p *afterChargeSpyPlugin) Version() string                                     { return "1.0.0" }
func (p *afterChargeSpyPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *afterChargeSpyPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *afterChargeSpyPlugin) Priority() int                                       { return 500 }
func (p *afterChargeSpyPlugin) AfterCharge(ctx *plugin.PaymentContext) error {
	p.called = true
	p.receivedCtx = ctx
	return nil
}

type onPaymentFailedSpyPlugin struct {
	called      bool
	receivedCtx *plugin.PaymentContext
	receivedErr error
}

func (p *onPaymentFailedSpyPlugin) Name() string                                        { return "failed-spy" }
func (p *onPaymentFailedSpyPlugin) Version() string                                     { return "1.0.0" }
func (p *onPaymentFailedSpyPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *onPaymentFailedSpyPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *onPaymentFailedSpyPlugin) Priority() int                                       { return 500 }
func (p *onPaymentFailedSpyPlugin) OnPaymentFailed(ctx *plugin.PaymentContext, err error) error {
	p.called = true
	p.receivedCtx = ctx
	p.receivedErr = err
	return nil
}

// --- Tests ---

func newFinalizedInvoice() *invoice.Invoice {
	inv := invoice.NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
	)
	_ = inv.Finalize()
	return inv
}

func TestProcessPayment_AfterChargeHook_ReceivesPaymentContext(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	inv := newFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	spy := &afterChargeSpyPlugin{}

	registry := plugin.NewRegistry()
	_ = registry.Register(spy)

	svc := NewPaymentService(&mockGateway{}, &mockPaymentRepo{}, invRepo, nil, registry, clock)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !spy.called {
		t.Fatal("AfterCharge hook was not called")
	}
	if spy.receivedCtx == nil {
		t.Fatal("PaymentContext was nil")
	}
	if spy.receivedCtx.Payment() == nil {
		t.Error("PaymentContext.Payment() should not be nil after successful charge")
	}
	if spy.receivedCtx.Invoice() == nil {
		t.Error("PaymentContext.Invoice() should not be nil")
	}
	if spy.receivedCtx.ContractID() != inv.ContractID() {
		t.Errorf("expected contract ID %s, got %s", inv.ContractID(), spy.receivedCtx.ContractID())
	}
}

func TestProcessPayment_OnPaymentFailedHook_ReceivesPaymentContext(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	inv := newFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	spy := &onPaymentFailedSpyPlugin{}

	registry := plugin.NewRegistry()
	_ = registry.Register(spy)

	svc := NewPaymentService(&mockGateway{failCharge: true}, &mockPaymentRepo{}, invRepo, nil, registry, clock)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-002",
	})
	if err == nil {
		t.Fatal("expected error for failed charge")
	}

	if !spy.called {
		t.Fatal("OnPaymentFailed hook was not called")
	}
	if spy.receivedCtx == nil {
		t.Fatal("PaymentContext was nil")
	}
	if spy.receivedCtx.Payment() == nil {
		t.Error("PaymentContext.Payment() should contain the failed payment record")
	}
	if spy.receivedCtx.Invoice() == nil {
		t.Error("PaymentContext.Invoice() should not be nil")
	}
	if spy.receivedErr == nil {
		t.Error("expected error to be passed to hook")
	}
}
