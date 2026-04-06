package service

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// --- Mock gateway ---

type mockGateway struct {
	failCharge bool
}

func (g *mockGateway) ID() string                                 { return "mock" }
func (g *mockGateway) SupportedMethods() []port.PaymentMethodType { return nil }
func (g *mockGateway) Charge(_ context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
	if g.failCharge {
		return nil, fmt.Errorf("card declined")
	}
	return &port.ChargeResponse{
		TransactionID: "txn-" + req.IdempotencyKey,
		Status:        port.TransactionStatusCaptured,
		Amount:        req.Amount,
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

// --- Mock repos ---

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
func (m *mockPaymentRepo) FindByIdempotencyKey(_ context.Context, _ string) (*payment.Payment, error) {
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

type mockCustomerGateway struct {
	customer *port.Customer
	err      error
}

func (m *mockCustomerGateway) CreateCustomer(_ context.Context, _ *port.CreateCustomerRequest) (*port.Customer, error) {
	return nil, nil
}
func (m *mockCustomerGateway) UpdateCustomer(_ context.Context, _ *port.UpdateCustomerRequest) (*port.Customer, error) {
	return nil, nil
}
func (m *mockCustomerGateway) GetCustomer(_ context.Context, _ string) (*port.Customer, error) {
	return m.customer, m.err
}
func (m *mockCustomerGateway) DeleteCustomer(_ context.Context, _ string) error { return nil }

type mockEventStore struct{}

func (m *mockEventStore) Append(_ context.Context, _ string, _ []eventstore.Event, _ int) error {
	return nil
}
func (m *mockEventStore) Load(_ context.Context, _ string) ([]eventstore.Event, error) {
	return nil, nil
}
func (m *mockEventStore) LoadUntilVersion(_ context.Context, _ string, _ int) ([]eventstore.Event, error) {
	return nil, nil
}
func (m *mockEventStore) LoadUntil(_ context.Context, _ string, _ time.Time) ([]eventstore.Event, error) {
	return nil, nil
}
func (m *mockEventStore) LoadRange(_ context.Context, _ string, _, _ time.Time) ([]eventstore.Event, error) {
	return nil, nil
}
func (m *mockEventStore) Subscribe(_ context.Context, _ int64) (<-chan eventstore.Event, error) {
	return nil, nil
}
func (m *mockEventStore) SaveSnapshot(_ context.Context, _ eventstore.Snapshot) error { return nil }
func (m *mockEventStore) LoadSnapshot(_ context.Context, _ string) (*eventstore.Snapshot, error) {
	return nil, nil
}
func (m *mockEventStore) LoadSnapshotBefore(_ context.Context, _ string, _ time.Time) (*eventstore.Snapshot, error) {
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

// --- Helpers ---

func newPaymentTestClock() shared.FixedClock {
	return shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
}

func newSimpleFinalizedInvoice() *invoice.Invoice {
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
	)
	if err != nil {
		panic(fmt.Sprintf("newSimpleFinalizedInvoice: NewInvoice failed: %v", err))
	}
	_ = inv.Finalize()
	return inv
}

func newFinalizedInvoice(accountID shared.AccountID, contractID shared.ContractID, amount shared.Money, pmID *string) *invoice.Invoice {
	opts := []invoice.InvoiceOption{
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
	}
	if pmID != nil {
		opts = append(opts, invoice.WithPaymentMethodID(pmID))
	}
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(),
		accountID,
		contractID,
		amount,
		shared.Zero(amount.Currency()),
		shared.Zero(amount.Currency()),
		opts...,
	)
	if err != nil {
		panic(fmt.Sprintf("newFinalizedInvoice: NewInvoice failed: %v", err))
	}
	return inv
}

func strPtr(s string) *string {
	return &s
}

// --- PaymentContext hook tests (from main) ---

func TestProcessPayment_AfterChargeHook_ReceivesPaymentContext(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	spy := &afterChargeSpyPlugin{}

	registry := plugin.NewRegistry()
	_ = registry.Register(spy)

	svc := NewPaymentService(&mockGateway{}, &mockPaymentRepo{}, invRepo, nil, &mockEventStore{}, registry, clock)

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
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	spy := &onPaymentFailedSpyPlugin{}

	registry := plugin.NewRegistry()
	_ = registry.Register(spy)

	svc := NewPaymentService(&mockGateway{failCharge: true}, &mockPaymentRepo{}, invRepo, nil, &mockEventStore{}, registry, clock)

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

// --- ResolvePaymentMethod tests ---

func TestResolvePaymentMethod_ExplicitInput(t *testing.T) {
	clock := newPaymentTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	inv := newFinalizedInvoice(agg.AccountID(), agg.ContractID(), jpy(1000), nil)
	invRepo := &mockInvoiceRepoForPayment{inv: inv}

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		invRepo,
		&mockContractRepo{agg: agg},
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-explicit",
		Amount:          jpy(1000),
		IdempotencyKey:  "test-001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pmt == nil {
		t.Fatal("expected payment, got nil")
	}
}

func TestResolvePaymentMethod_FallbackToInvoice(t *testing.T) {
	clock := newPaymentTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	pmID := "pm-invoice-level"
	inv := newFinalizedInvoice(agg.AccountID(), agg.ContractID(), jpy(1000), &pmID)

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		&mockContractRepo{agg: agg},
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	resolved, err := svc.ResolvePaymentMethod(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "pm-invoice-level" {
		t.Errorf("expected pm-invoice-level, got %s", resolved)
	}
}

func TestResolvePaymentMethod_FallbackToContract(t *testing.T) {
	clock := newPaymentTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	_ = agg.ChangePaymentMethod(strPtr("pm-contract-level"), eventstore.EventMetadata{UserID: "test"})

	inv := newFinalizedInvoice(agg.AccountID(), agg.ContractID(), jpy(1000), nil)

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		&mockContractRepo{agg: agg},
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	resolved, err := svc.ResolvePaymentMethod(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "pm-contract-level" {
		t.Errorf("expected pm-contract-level, got %s", resolved)
	}
}

func TestResolvePaymentMethod_FallbackToCustomer(t *testing.T) {
	clock := newPaymentTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	inv := newFinalizedInvoice(agg.AccountID(), agg.ContractID(), jpy(1000), nil)

	customerPM := "pm-customer-default"
	custGateway := &mockCustomerGateway{
		customer: &port.Customer{
			ID:                     string(agg.AccountID()),
			DefaultPaymentMethodID: &customerPM,
		},
	}

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		&mockContractRepo{agg: agg},
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithCustomerGateway(custGateway),
	)

	resolved, err := svc.ResolvePaymentMethod(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "pm-customer-default" {
		t.Errorf("expected pm-customer-default, got %s", resolved)
	}
}

func TestResolvePaymentMethod_InvoiceOverridesContract(t *testing.T) {
	clock := newPaymentTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	_ = agg.ChangePaymentMethod(strPtr("pm-contract"), eventstore.EventMetadata{UserID: "test"})

	invPM := "pm-invoice"
	inv := newFinalizedInvoice(agg.AccountID(), agg.ContractID(), jpy(1000), &invPM)

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		&mockContractRepo{agg: agg},
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	resolved, err := svc.ResolvePaymentMethod(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "pm-invoice" {
		t.Errorf("expected pm-invoice (invoice-level override), got %s", resolved)
	}
}

func TestResolvePaymentMethod_NoPaymentMethodFound(t *testing.T) {
	clock := newPaymentTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	inv := newFinalizedInvoice(agg.AccountID(), agg.ContractID(), jpy(1000), nil)

	custGateway := &mockCustomerGateway{
		customer: &port.Customer{
			ID:                     string(agg.AccountID()),
			DefaultPaymentMethodID: nil,
		},
	}

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		&mockContractRepo{agg: agg},
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithCustomerGateway(custGateway),
	)

	_, err := svc.ResolvePaymentMethod(context.Background(), inv)
	if err == nil {
		t.Fatal("expected error when no payment method found")
	}
}

func TestProcessPayment_AutoResolvesPaymentMethod(t *testing.T) {
	clock := newPaymentTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	_ = agg.ChangePaymentMethod(strPtr("pm-auto-resolved"), eventstore.EventMetadata{UserID: "test"})

	inv := newFinalizedInvoice(agg.AccountID(), agg.ContractID(), jpy(1000), nil)
	invRepo := &mockInvoiceRepoForPayment{inv: inv}

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		invRepo,
		&mockContractRepo{agg: agg},
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		Amount:         jpy(1000),
		IdempotencyKey: "test-auto",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pmt == nil {
		t.Fatal("expected payment, got nil")
	}
}
