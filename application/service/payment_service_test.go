package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// --- Mock gateway ---

type mockGateway struct {
	failCharge              bool
	requiresAction          bool
	threeDSRedirect         string
	chargePaymentMethodType port.PaymentMethodType // if set, returned in ChargeResponse
	chargeStatus            port.TransactionStatus // if set, overrides the default Captured status
}

func (g *mockGateway) ID() string                                 { return "mock" }
func (g *mockGateway) SupportedMethods() []port.PaymentMethodType { return nil }
func (g *mockGateway) Charge(_ context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
	if g.failCharge {
		return nil, fmt.Errorf("card declined")
	}
	if g.requiresAction {
		resp := &port.ChargeResponse{
			TransactionID: "txn-" + req.IdempotencyKey,
			Status:        port.TransactionStatusRequiresAction,
			Amount:        req.Amount,
			CreatedAt:     time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
			ThreeDSecure: &port.ThreeDSecureResult{
				Status:      port.ThreeDSecureStatusRequired,
				RedirectURL: &g.threeDSRedirect,
			},
		}
		return resp, nil
	}
	status := port.TransactionStatusCaptured
	if g.chargeStatus != "" {
		status = g.chargeStatus
	}
	return &port.ChargeResponse{
		TransactionID:     "txn-" + req.IdempotencyKey,
		Status:            status,
		Amount:            req.Amount,
		PaymentMethodType: g.chargePaymentMethodType,
		CreatedAt:         time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}, nil
}
func (g *mockGateway) Authorize(_ context.Context, _ *port.AuthorizeRequest) (*port.AuthorizeResponse, error) {
	return nil, nil
}
func (g *mockGateway) Capture(_ context.Context, _ *port.CaptureRequest) (*port.CaptureResponse, error) {
	return nil, nil
}
func (g *mockGateway) Void(_ context.Context, _ *port.VoidRequest) (*port.VoidResponse, error) {
	// A one-step Charge (authorize+capture) produces a captured transaction,
	// which cannot be Voided. Modelling this as an error is what drives the
	// issue #86 Void→Refund fallback for the common instantly-settled card
	// case, so saga compensation falls back to Refund. Gateways/tests that
	// exercise the pre-settlement Void-succeeds path (e.g. spyGateway)
	// override this method.
	return nil, fmt.Errorf("cannot void a captured transaction")
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
	saved    *payment.Payment
	saveErr  error            // if set, Save returns this error
	existing *payment.Payment // if set, FindByIdempotencyKey returns this
}

func (m *mockPaymentRepo) Save(_ context.Context, p *payment.Payment) error {
	if m.saveErr != nil {
		return m.saveErr
	}
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
	if m.existing != nil {
		return m.existing, nil
	}
	return nil, nil
}

type mockInvoiceRepoForPayment struct {
	inv        *invoice.Invoice
	saveCount  int        // spy: counts Save calls — catches pointer-aliasing silent holes
	saveAmount []*big.Rat // snapshot of inv.PaidAmount() at each Save call
}

func (m *mockInvoiceRepoForPayment) Save(_ context.Context, inv *invoice.Invoice) error {
	m.inv = inv
	m.saveCount++
	m.saveAmount = append(m.saveAmount, new(big.Rat).Set(inv.PaidAmount().Amount()))
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
func (m *mockCustomerGateway) SetDefaultPaymentMethod(_ context.Context, _, _ string) error {
	return nil
}

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
func (m *mockEventStore) LoadAll(_ context.Context, _ int64, _ int) ([]eventstore.Event, error) {
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

type beforeChargeSpyPlugin struct {
	calls int
}

func (p *beforeChargeSpyPlugin) Name() string                                        { return "before-charge-spy" }
func (p *beforeChargeSpyPlugin) Version() string                                     { return "1.0.0" }
func (p *beforeChargeSpyPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *beforeChargeSpyPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *beforeChargeSpyPlugin) Priority() int                                       { return 500 }
func (p *beforeChargeSpyPlugin) BeforeCharge(_ *plugin.PaymentContext, _ shared.Money) error {
	p.calls++
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
		panic("newSimpleFinalizedInvoice: " + err.Error())
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
		panic("newFinalizedInvoice: " + err.Error())
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

// --- Spy gateway for compensation tracking ---

type spyGateway struct {
	mockGateway
	voidCalled   bool
	voidReq      *port.VoidRequest
	voidErr      error
	refundCalled bool
	refundReq    *port.RefundRequest
	refundErr    error
}

func (g *spyGateway) Void(_ context.Context, req *port.VoidRequest) (*port.VoidResponse, error) {
	g.voidCalled = true
	g.voidReq = req
	if g.voidErr != nil {
		return nil, g.voidErr
	}
	return &port.VoidResponse{}, nil
}

func (g *spyGateway) Refund(_ context.Context, req *port.RefundRequest) (*port.RefundResponse, error) {
	g.refundCalled = true
	g.refundReq = req
	if g.refundErr != nil {
		return nil, g.refundErr
	}
	return &port.RefundResponse{TransactionID: "refund-comp"}, nil
}

// --- Failing TxManager to trigger compensation ---

type paymentFailingTxManager struct{}

func (m *paymentFailingTxManager) RunInTx(_ context.Context, _ func(context.Context, tx.Repos) error) error {
	return fmt.Errorf("simulated database failure")
}

// --- Saga compensation tests (Issue #82 / #86) ---

func TestProcessPayment_SagaCompensation_VoidSucceeds_NoRefund(t *testing.T) {
	// Issue #86: when Charge succeeds but local save fails, the saga
	// compensation tries Void FIRST. When Void succeeds (pre-settlement
	// charge cancelled), Refund MUST NOT be called — otherwise a
	// non-idempotent gateway could double-reverse the charge.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &spyGateway{} // Void returns success by default

	svc := NewPaymentService(
		gw,
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(&paymentFailingTxManager{}),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-comp",
	})

	// Should fail because local save failed
	if err == nil {
		t.Fatal("expected error from local save failure")
	}

	// Compensation should have tried Void first...
	if !gw.voidCalled {
		t.Fatal("Void should be called first as saga compensation")
	}
	// ...and since Void succeeded, Refund must NOT be called (money safety).
	if gw.refundCalled {
		t.Error("Refund must NOT be called when Void already reversed the charge")
	}
	// Void must target the charge transaction with a deterministic idempotency key.
	expectedTxnID := "txn-key-comp"
	if gw.voidReq.AuthorizationID != expectedTxnID {
		t.Errorf("expected Void AuthorizationID %q, got %q", expectedTxnID, gw.voidReq.AuthorizationID)
	}
	expectedVoidKey := "comp-void-" + expectedTxnID
	if gw.voidReq.IdempotencyKey != expectedVoidKey {
		t.Errorf("expected Void IdempotencyKey %q, got %q", expectedVoidKey, gw.voidReq.IdempotencyKey)
	}
}

func TestProcessPayment_SagaCompensation_VoidFails_FallsBackToRefund(t *testing.T) {
	// Issue #86: when Void fails (charge already captured/settled — the
	// common case for instantly-settling credit cards), compensation falls
	// back to Refund with the correct transaction ID and deterministic key.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &spyGateway{voidErr: fmt.Errorf("cannot void a settled transaction")}

	svc := NewPaymentService(
		gw,
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(&paymentFailingTxManager{}),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-txnid",
	})

	// ProcessPayment still returns an error (local save failed), but
	// compensation (Void→Refund fallback) succeeded, so it is the plain
	// "local save failed (gateway charge refunded)" error.
	if err == nil {
		t.Fatal("expected error from local save failure")
	}

	if !gw.voidCalled {
		t.Fatal("Void should be attempted first")
	}
	if !gw.refundCalled {
		t.Fatal("Refund should be called as fallback after Void fails")
	}
	// mockGateway.Charge returns "txn-" + IdempotencyKey
	expectedTxnID := "txn-key-txnid"
	if gw.refundReq.TransactionID != expectedTxnID {
		t.Errorf("expected refund TransactionID %q, got %q", expectedTxnID, gw.refundReq.TransactionID)
	}
	// Amount should explicitly match the charged amount
	if gw.refundReq.Amount == nil {
		t.Fatal("expected explicit Amount on compensation refund, got nil")
	}
	expectedAmount := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	if gw.refundReq.Amount.Amount().Cmp(expectedAmount.Amount()) != 0 {
		t.Errorf("expected refund Amount %v, got %v", expectedAmount.Amount(), gw.refundReq.Amount.Amount())
	}
	// Reason should be set for gateway audit trail
	if gw.refundReq.Reason != port.RefundReasonOther {
		t.Errorf("expected refund Reason %q, got %q", port.RefundReasonOther, gw.refundReq.Reason)
	}
	// IdempotencyKey should be set to prevent double-refund on retry
	expectedKey := "comp-refund-" + expectedTxnID
	if gw.refundReq.IdempotencyKey != expectedKey {
		t.Errorf("expected refund IdempotencyKey %q, got %q", expectedKey, gw.refundReq.IdempotencyKey)
	}
}

func TestProcessPayment_SagaCompensation_RefundFailure_ReturnsCompoundError(t *testing.T) {
	// When local save fails and BOTH compensation reversals (Void then the
	// Refund fallback, issue #86) fail, the error should contain both the
	// save and compensation failures for manual reconciliation.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &spyGateway{
		voidErr:   fmt.Errorf("cannot void a settled transaction"),
		refundErr: fmt.Errorf("gateway timeout"),
	}

	svc := NewPaymentService(
		gw,
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(&paymentFailingTxManager{}),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-double-fail",
	})

	if err == nil {
		t.Fatal("expected error when both save and compensation fail")
	}
	// Error should mention both failures
	errMsg := err.Error()
	if !strings.Contains(errMsg, "local save failed") {
		t.Errorf("expected error to mention local save failure, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "compensation also failed") {
		t.Errorf("expected error to mention compensation failure, got: %s", errMsg)
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

// --- 3D Secure requires_action tests ---

func TestProcessPayment_RequiresAction_ReturnsPendingPayment(t *testing.T) {
	// When gateway returns requires_action (3DS needed), ProcessPayment
	// should NOT mark the payment as completed. It should save a pending
	// payment and return a specific error indicating 3DS is required.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := &mockPaymentRepo{}

	gw := &mockGateway{
		requiresAction:  true,
		threeDSRedirect: "https://bank.example.com/3ds",
	}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-3ds",
	})

	// Should return ErrRequiresAction (sentinel error, usable with errors.Is)
	if err == nil {
		t.Fatal("expected error for requires_action status")
	}
	if !errors.Is(err, ErrRequiresAction) {
		t.Errorf("expected error to wrap ErrRequiresAction, got: %s", err.Error())
	}

	// Payment should be saved in pending status (not completed)
	if pmt == nil {
		t.Fatal("expected pending payment to be returned")
	}
	if pmt.Status() != payment.PaymentStatusPending {
		t.Errorf("expected payment status %q, got %q", payment.PaymentStatusPending, pmt.Status())
	}

	// Payment should be persisted for later completion after 3DS callback
	if paymentRepo.saved == nil {
		t.Error("expected payment to be saved to repository")
	}
}

func TestProcessPayment_RequiresAction_DoesNotRecordPaymentOnInvoice(t *testing.T) {
	// When 3DS is required, the invoice should NOT be updated as paid.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}

	gw := &mockGateway{
		requiresAction:  true,
		threeDSRedirect: "https://bank.example.com/3ds",
	}

	svc := NewPaymentService(
		gw,
		&mockPaymentRepo{},
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-3ds-inv",
	})

	// Invoice paid amount should still be zero
	if !inv.PaidAmount().IsZero() {
		t.Errorf("invoice should not have recorded payment, but paidAmount is %v", inv.PaidAmount().Amount())
	}
}

func TestProcessPayment_RequiresAction_SaveFailure_ReturnsError(t *testing.T) {
	// When the pending payment save fails, ProcessPayment must return an error
	// (not silently ignore it) because the gateway has an active authorization.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()

	gw := &mockGateway{
		requiresAction:  true,
		threeDSRedirect: "https://bank.example.com/3ds",
	}

	svc := NewPaymentService(
		gw,
		&mockPaymentRepo{saveErr: fmt.Errorf("database connection lost")},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-3ds-save-fail",
	})

	if err == nil {
		t.Fatal("expected error when pending payment save fails")
	}
	if pmt != nil {
		t.Error("expected nil payment when save fails")
	}
	// Should NOT be ErrRequiresAction — this is a system error
	if errors.Is(err, ErrRequiresAction) {
		t.Error("save failure should not be wrapped as ErrRequiresAction")
	}
}

func TestProcessPayment_RequiresAction_Idempotency_ReturnsCachedPayment(t *testing.T) {
	// When a pending payment already exists for the same idempotency key,
	// ProcessPayment should return it without creating a duplicate authorization.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()

	existingPayment, _ := payment.NewPayment(
		shared.NewPaymentID(),
		inv.ID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"existing-txn-001",
		clock.Now(),
	)

	gw := &mockGateway{
		requiresAction:  true,
		threeDSRedirect: "https://bank.example.com/3ds",
	}

	svc := NewPaymentService(
		gw,
		&mockPaymentRepo{existing: existingPayment},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-3ds-retry",
	})

	// Should return ErrRequiresAction with the existing payment
	if !errors.Is(err, ErrRequiresAction) {
		t.Fatalf("expected ErrRequiresAction, got: %v", err)
	}
	if pmt == nil {
		t.Fatal("expected existing payment to be returned")
	}
	if pmt.GatewayTransactionID() != "existing-txn-001" {
		t.Errorf("expected existing transaction ID, got %q", pmt.GatewayTransactionID())
	}
	// Gateway should still have been called (we can't prevent that), but
	// the idempotency key on the ChargeRequest should prevent duplicate auth on the GW side
}

// --- Issue #88: PaymentMethod should not be hardcoded ---

func TestProcessPayment_UsesPaymentMethodTypeFromChargeResponse(t *testing.T) {
	// When the gateway returns a PaymentMethodType in ChargeResponse,
	// the Payment entity must use that type instead of hardcoded credit_card.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := &mockPaymentRepo{}

	gw := &mockGateway{
		chargePaymentMethodType: port.PaymentMethodTypeBankTransfer,
	}

	svc := NewPaymentService(gw, paymentRepo, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-bank",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pmt.Method() != payment.PaymentMethodBankTransfer {
		t.Errorf("expected payment method %q, got %q", payment.PaymentMethodBankTransfer, pmt.Method())
	}
}

func TestProcessPayment_UsesPaymentMethodFromInput_WhenChargeResponseEmpty(t *testing.T) {
	// When ChargeResponse does not include PaymentMethodType,
	// the Payment entity should use the PaymentMethod from ProcessPaymentInput.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := &mockPaymentRepo{}

	gw := &mockGateway{} // chargePaymentMethodType is zero value (empty)

	svc := NewPaymentService(gw, paymentRepo, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		PaymentMethod:   payment.PaymentMethodConvenience,
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-conv",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pmt.Method() != payment.PaymentMethodConvenience {
		t.Errorf("expected payment method %q, got %q", payment.PaymentMethodConvenience, pmt.Method())
	}
}

func TestProcessPayment_DefaultsToCreditCard_WhenNoPaymentMethodInfo(t *testing.T) {
	// When neither ChargeResponse nor ProcessPaymentInput specifies a payment method,
	// the Payment entity should default to credit_card for backward compatibility.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := &mockPaymentRepo{}

	gw := &mockGateway{} // no PaymentMethodType

	svc := NewPaymentService(gw, paymentRepo, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		// PaymentMethod not set
		Amount:         shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:       shared.CurrencyJPY,
		IdempotencyKey: "key-default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pmt.Method() != payment.PaymentMethodCreditCard {
		t.Errorf("expected default payment method %q, got %q", payment.PaymentMethodCreditCard, pmt.Method())
	}
}

func TestProcessPayment_FailedPayment_UsesCorrectPaymentMethod(t *testing.T) {
	// When the gateway charge fails, the failed payment record should still
	// use the correct payment method, not hardcoded credit_card.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := &mockPaymentRepo{}

	gw := &mockGateway{failCharge: true}

	svc := NewPaymentService(gw, paymentRepo, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

	_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		PaymentMethod:   payment.PaymentMethodDirectDebit,
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-fail-dd",
	})

	if paymentRepo.saved == nil {
		t.Fatal("expected failed payment to be saved")
	}
	if paymentRepo.saved.Method() != payment.PaymentMethodDirectDebit {
		t.Errorf("expected failed payment method %q, got %q", payment.PaymentMethodDirectDebit, paymentRepo.saved.Method())
	}
}

func TestProcessPayment_RequiresAction_UsesCorrectPaymentMethod(t *testing.T) {
	// When 3DS is required, the pending payment should use the input payment method.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := &mockPaymentRepo{}

	gw := &mockGateway{
		requiresAction:  true,
		threeDSRedirect: "https://bank.example.com/3ds",
	}

	svc := NewPaymentService(gw, paymentRepo, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

	pmt, _ := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		PaymentMethod:   payment.PaymentMethodCarrier,
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-3ds-carrier",
	})

	if pmt == nil {
		t.Fatal("expected pending payment to be returned")
	}
	if pmt.Method() != payment.PaymentMethodCarrier {
		t.Errorf("expected pending payment method %q, got %q", payment.PaymentMethodCarrier, pmt.Method())
	}
}

// --- Issue #85: state mutations must be inside RunInTx ---

func TestProcessPayment_TxFailure_InvoiceStateNotMutated(t *testing.T) {
	// When RunInTx fails, the invoice's in-memory state must NOT be mutated.
	// Before the fix, RecordPayment was called outside RunInTx, so even when
	// the transaction failed, the invoice was left in paid/partial_paid status.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()

	statusBefore := inv.Status()
	paidBefore := inv.PaidAmount()

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(&paymentFailingTxManager{}),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-tx-fail-state",
	})
	if err == nil {
		t.Fatal("expected error from tx failure")
	}

	// Invoice state must remain unchanged after tx failure
	if inv.Status() != statusBefore {
		t.Errorf("invoice status mutated after tx failure: expected %q, got %q", statusBefore, inv.Status())
	}
	if inv.PaidAmount().Amount().Cmp(paidBefore.Amount()) != 0 {
		t.Errorf("invoice paidAmount mutated after tx failure: expected %v, got %v", paidBefore.Amount(), inv.PaidAmount().Amount())
	}
}

func TestProcessPayment_Idempotency_DoesNotMutateInvoiceForDuplicateKey(t *testing.T) {
	// When a payment with the same idempotency key already exists, the invoice
	// must NOT have RecordPayment called (no double accounting).
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()

	existingPayment, _ := payment.NewPayment(
		shared.NewPaymentID(),
		inv.ID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"txn-existing",
		clock.Now(),
	)
	_ = existingPayment.Complete()

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{existing: existingPayment},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "duplicate-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return the existing payment
	if pmt.GatewayTransactionID() != "txn-existing" {
		t.Errorf("expected existing payment, got txn ID %q", pmt.GatewayTransactionID())
	}

	// Invoice must NOT have been mutated (no double RecordPayment)
	if !inv.PaidAmount().IsZero() {
		t.Errorf("invoice paidAmount should be zero for idempotent duplicate, got %v", inv.PaidAmount().Amount())
	}
}

func TestProcessPayment_TxFailure_SagaCompensationFires(t *testing.T) {
	// When RunInTx fails, saga compensation must be triggered.
	// Before the fix, if RecordPayment failed outside RunInTx, the function
	// returned early without calling saga.Compensate(), leaving a charged
	// payment without a reversal. Post-#86 the compensation reverses via
	// Void first (Refund is only the fallback when Void fails).
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &spyGateway{}

	svc := NewPaymentService(
		gw,
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(&paymentFailingTxManager{}),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-tx-fail-saga",
	})
	if err == nil {
		t.Fatal("expected error from tx failure")
	}

	// Saga compensation must have fired. Void is the first-line reversal
	// (issue #86); the default spyGateway.Void succeeds, so Refund is not
	// reached on this path.
	if !gw.voidCalled {
		t.Fatal("saga compensation (void) must fire when RunInTx fails")
	}
}

// --- Issue #88: portMethodToPaymentMethod unit tests ---

func Test_portMethodToPaymentMethod(t *testing.T) {
	tests := []struct {
		input     port.PaymentMethodType
		want      payment.PaymentMethod
		wantKnown bool
	}{
		{port.PaymentMethodTypeCreditCard, payment.PaymentMethodCreditCard, true},
		{port.PaymentMethodTypeDebitCard, payment.PaymentMethodDebitCard, true},
		{port.PaymentMethodTypeBankTransfer, payment.PaymentMethodBankTransfer, true},
		{port.PaymentMethodTypeConvenienceStore, payment.PaymentMethodConvenience, true},
		{port.PaymentMethodTypeQRCode, payment.PaymentMethodQRCode, true},
		{port.PaymentMethodTypeDirectDebit, payment.PaymentMethodDirectDebit, true},
		{port.PaymentMethodTypeCarrier, payment.PaymentMethodCarrier, true},
		{port.PaymentMethodTypePostpay, payment.PaymentMethodPostpay, true},
		{"unknown_type", payment.PaymentMethodCreditCard, false},
		{"", payment.PaymentMethodCreditCard, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			got, known := portMethodToPaymentMethod(tt.input)
			if got != tt.want {
				t.Errorf("portMethodToPaymentMethod(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if known != tt.wantKnown {
				t.Errorf("portMethodToPaymentMethod(%q) known = %v, want %v", tt.input, known, tt.wantKnown)
			}
		})
	}
}

func Test_resolvePaymentMethodType(t *testing.T) {
	// resolvePaymentMethodType is a method on PaymentService; create a minimal instance for testing.
	svc := NewPaymentService(&mockGateway{}, &mockPaymentRepo{}, &mockInvoiceRepoForPayment{}, nil, &mockEventStore{}, plugin.NewRegistry(), newPaymentTestClock())

	tests := []struct {
		name         string
		chargeMethod port.PaymentMethodType
		inputMethod  payment.PaymentMethod
		want         payment.PaymentMethod
	}{
		{"ChargeResponse takes priority", port.PaymentMethodTypeBankTransfer, payment.PaymentMethodCarrier, payment.PaymentMethodBankTransfer},
		{"Input used when ChargeResponse empty", "", payment.PaymentMethodConvenience, payment.PaymentMethodConvenience},
		{"Default to credit_card when both empty", "", "", payment.PaymentMethodCreditCard},
		{"ChargeResponse QRCode", port.PaymentMethodTypeQRCode, "", payment.PaymentMethodQRCode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.resolvePaymentMethodType(tt.chargeMethod, tt.inputMethod)
			if got != tt.want {
				t.Errorf("resolvePaymentMethodType(%q, %q) = %q, want %q", tt.chargeMethod, tt.inputMethod, got, tt.want)
			}
		})
	}
}

// --- PR #93 review: chargeResp.Status guard ---

func TestProcessPayment_UnexpectedChargeStatus_ReturnsError(t *testing.T) {
	// When the gateway returns err==nil but a non-success status (not Captured/Succeeded),
	// ProcessPayment must NOT proceed to the success path and record a completed payment.
	// TransactionStatusPending is intentionally absent: it is now a first-class
	// async-settlement outcome (ErrPaymentPending) — see payment_settlement_test.go.
	unexpectedStatuses := []port.TransactionStatus{
		port.TransactionStatusFailed,
		port.TransactionStatusCanceled,
		port.TransactionStatusAuthorized,
		port.TransactionStatusRefunded,
		port.TransactionStatusPartiallyRefunded,
	}

	for _, status := range unexpectedStatuses {
		t.Run(string(status), func(t *testing.T) {
			clock := newPaymentTestClock()
			inv := newSimpleFinalizedInvoice()
			paymentRepo := &mockPaymentRepo{}

			gw := &mockGateway{chargeStatus: status}

			svc := NewPaymentService(gw, paymentRepo, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

			pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
				PaymentMethodID: "pm-001",
				Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
				Currency:        shared.CurrencyJPY,
				IdempotencyKey:  "key-unexpected-" + string(status),
			})

			if err == nil {
				t.Fatalf("expected error for unexpected charge status %q, got nil", status)
			}
			if pmt != nil {
				t.Errorf("expected nil payment for unexpected status %q, got payment with status %q", status, pmt.Status())
			}
			// Invoice must not be mutated
			if !inv.PaidAmount().IsZero() {
				t.Errorf("invoice should not record payment for unexpected status %q", status)
			}
		})
	}
}

func TestProcessPayment_SucceededStatus_Completes(t *testing.T) {
	// TransactionStatusSucceeded (in addition to Captured) should be treated as success.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()

	gw := &mockGateway{chargeStatus: port.TransactionStatusSucceeded}

	svc := NewPaymentService(gw, &mockPaymentRepo{}, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-succeeded",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pmt.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected completed status, got %q", pmt.Status())
	}
}

// --- Issue #87: Compensation marker + fresh-key retry ---
//
// These tests and helpers reproduce the exact race described in Issue #87:
// Charge succeeds → local save fails → saga compensation Refund fires → caller
// retries with the SAME IdempotencyKey → gateway replays the original (now
// refunded) charge response. The fix records the compensated key in an
// IdempotencyStore and, on retry, re-charges with a freshly generated key.

// fakeIdempotencyStore is a test double for port.IdempotencyStore.
// It tracks (originalKey -> effectiveKey) mappings and exposes injection
// points for errors so tests can verify the error-handling code paths.
type fakeIdempotencyStore struct {
	mu            sync.Mutex
	mapping       map[string]string // originalKey -> effectiveKey
	markErr       error             // if set, MarkCompensated returns this
	resolveErr    error             // if set, ResolveEffectiveKey returns this
	markCalls     int
	resolveCalls  int
	resolveValues []string // every resolved effective key, in call order
}

func newFakeIdempotencyStore() *fakeIdempotencyStore {
	return &fakeIdempotencyStore{mapping: make(map[string]string)}
}

func (s *fakeIdempotencyStore) MarkCompensated(_ context.Context, originalKey, effectiveKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markCalls++
	if s.markErr != nil {
		return s.markErr
	}
	if originalKey == "" {
		return fmt.Errorf("empty original key")
	}
	// First-call-wins: do not overwrite an existing mapping so repeated
	// compensations produce a single stable effective key (matches the
	// IdempotencyStore contract).
	if _, exists := s.mapping[originalKey]; !exists {
		s.mapping[originalKey] = effectiveKey
	}
	return nil
}

func (s *fakeIdempotencyStore) ResolveEffectiveKey(_ context.Context, originalKey string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolveCalls++
	if s.resolveErr != nil {
		return "", false, s.resolveErr
	}
	eff, ok := s.mapping[originalKey]
	s.resolveValues = append(s.resolveValues, eff)
	return eff, ok, nil
}

func (s *fakeIdempotencyStore) isMarked(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.mapping[key]
	return ok
}

// fakePaymentRepo is a map-backed repository that supports multiple payments,
// IdempotencyKey lookup, and optional save failure injection.
// savedSnapshot captures (paymentID, status) at the moment of Save, so tests
// can detect Save *calls* even when the underlying map stores pointer
// references. Without this, removing a `repos.Payments.Save(existing)` line
// in the Pending upgrade path would be invisible to tests because the
// existing pointer already reflects the mutated state.
type savedSnapshot struct {
	ID     shared.PaymentID
	Status payment.PaymentStatus
}

type fakePaymentRepo struct {
	mu            sync.Mutex
	byID          map[shared.PaymentID]*payment.Payment
	saveCount     int
	saveFailUntil int // first N saves fail with saveErr
	saveErr       error
	// saveHistory records a snapshot of (ID, status) for every successful
	// Save call, so pointer-aliased mutations without Save can be detected
	// by test assertions like "Save was called on payment X with status Completed".
	saveHistory []savedSnapshot
}

func newFakePaymentRepo() *fakePaymentRepo {
	return &fakePaymentRepo{byID: make(map[shared.PaymentID]*payment.Payment)}
}

func (r *fakePaymentRepo) Save(_ context.Context, p *payment.Payment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveCount++
	if r.saveCount <= r.saveFailUntil {
		if r.saveErr != nil {
			return r.saveErr
		}
		return fmt.Errorf("simulated save failure (call %d)", r.saveCount)
	}
	r.byID[p.ID()] = p
	r.saveHistory = append(r.saveHistory, savedSnapshot{
		ID:     p.ID(),
		Status: p.Status(),
	})
	return nil
}

// wasSavedWithStatus reports whether Save was called at least once on the
// given payment ID while the payment held the given status. This is the
// only reliable way to catch the M-SW6 silent hole (pointer aliasing) in
// the Pending → Completed upgrade path.
func (r *fakePaymentRepo) wasSavedWithStatus(id shared.PaymentID, status payment.PaymentStatus) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, snap := range r.saveHistory {
		if snap.ID == id && snap.Status == status {
			return true
		}
	}
	return false
}

// raceFakePaymentRepo simulates a race between the pre-charge idempotency
// lookup and the in-tx idempotency lookup: the FIRST FindByIdempotencyKey
// call returns nil (pre-charge sees no existing payment) and SUBSEQUENT
// calls return the seeded "delayed" payment (a concurrent writer landed
// between the two reads). This is the only way to exercise the in-tx
// terminal-state rejection branch without also tripping the pre-charge
// short-circuit — making the defense-in-depth testable.
type raceFakePaymentRepo struct {
	*fakePaymentRepo
	delayed  *payment.Payment
	findMu   sync.Mutex
	findCall int
}

func newRaceFakePaymentRepo(delayed *payment.Payment) *raceFakePaymentRepo {
	return &raceFakePaymentRepo{
		fakePaymentRepo: newFakePaymentRepo(),
		delayed:         delayed,
	}
}

func (r *raceFakePaymentRepo) FindByIdempotencyKey(_ context.Context, _ string) (*payment.Payment, error) {
	r.findMu.Lock()
	r.findCall++
	callNum := r.findCall
	r.findMu.Unlock()
	if callNum == 1 {
		return nil, nil // pre-charge lookup: no existing payment visible yet
	}
	return r.delayed, nil // in-tx lookup: the concurrent writer has landed
}

func (r *fakePaymentRepo) FindByID(_ context.Context, id shared.PaymentID) (*payment.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("payment %s not found", id)
	}
	return p, nil
}

func (r *fakePaymentRepo) FindByInvoiceID(_ context.Context, invoiceID shared.InvoiceID) ([]*payment.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*payment.Payment
	for _, p := range r.byID {
		if p.InvoiceID() == invoiceID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *fakePaymentRepo) FindByIdempotencyKey(_ context.Context, key string) (*payment.Payment, error) {
	if key == "" {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.byID {
		if p.IdempotencyKey() == key {
			return p, nil
		}
	}
	return nil, nil
}

func (r *fakePaymentRepo) countCompleted() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, p := range r.byID {
		if p.Status() == payment.PaymentStatusCompleted {
			n++
		}
	}
	return n
}

// seed inserts a payment directly without going through the save-failure
// injection logic. Tests use this to pre-populate the repo with existing
// payments that the service is expected to find via FindByIdempotencyKey.
func (r *fakePaymentRepo) seed(p *payment.Payment) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[p.ID()] = p
}

// switchableTxManager fails the first N calls to RunInTx and then succeeds.
// Unlike paymentFailingTxManager (which always fails), this lets us reproduce
// a first-call failure followed by a successful retry within the same test.
type switchableTxManager struct {
	mu          sync.Mutex
	callCount   int
	failUntil   int // first N calls fail
	paymentRepo payment.Repository
	invoiceRepo invoice.Repository
}

func (m *switchableTxManager) RunInTx(ctx context.Context, fn func(context.Context, tx.Repos) error) error {
	m.mu.Lock()
	m.callCount++
	currentCall := m.callCount
	m.mu.Unlock()
	if currentCall <= m.failUntil {
		return fmt.Errorf("simulated tx failure (call %d)", currentCall)
	}
	return fn(ctx, tx.Repos{
		Payments: m.paymentRepo,
		Invoices: m.invoiceRepo,
	})
}

// trackingGateway records every Charge request's IdempotencyKey and can
// be configured to return specific ChargeResponses per call.
type trackingGateway struct {
	mockGateway
	mu              sync.Mutex
	chargeKeys      []string
	refundKeys      []string
	refundTxnIDs    []string
	chargeResponses []port.ChargeResponse // if non-empty, consumed in order
	chargeErrs      []error               // if non-empty, consumed in order
	refundErr       error
}

func (g *trackingGateway) Charge(_ context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	idx := len(g.chargeKeys)
	g.chargeKeys = append(g.chargeKeys, req.IdempotencyKey)

	if idx < len(g.chargeErrs) && g.chargeErrs[idx] != nil {
		return nil, g.chargeErrs[idx]
	}
	if idx < len(g.chargeResponses) {
		resp := g.chargeResponses[idx]
		return &resp, nil
	}
	// default: successful capture
	return &port.ChargeResponse{
		TransactionID: "txn-" + req.IdempotencyKey,
		Status:        port.TransactionStatusCaptured,
		Amount:        req.Amount,
		CreatedAt:     time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}, nil
}

func (g *trackingGateway) Refund(_ context.Context, req *port.RefundRequest) (*port.RefundResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refundKeys = append(g.refundKeys, req.IdempotencyKey)
	g.refundTxnIDs = append(g.refundTxnIDs, req.TransactionID)
	if g.refundErr != nil {
		return nil, g.refundErr
	}
	return &port.RefundResponse{TransactionID: "refund-" + req.TransactionID}, nil
}

// --- BLOCKER test: actual race reproduction ---

func TestProcessPayment_CompensatedKey_RetryReChargesWithNewKey(t *testing.T) {
	// Reproduces Issue #87 end-to-end with ONE service, ONE repo, ONE invoice:
	//   1. First call: Charge succeeds → tx fails → saga compensation Refund →
	//      IdempotencyStore.MarkCompensated("key-87") recorded.
	//   2. Second call: same service, same key. The store says "compensated",
	//      so a fresh key is generated and the gateway sees a new Charge.
	//   3. Final state: exactly one completed Payment with a GatewayTransactionID
	//      that is NOT the refunded "txn-key-87".
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	gw := &trackingGateway{}

	txm := &switchableTxManager{
		failUntil:   1, // only the first call fails
		paymentRepo: paymentRepo,
		invoiceRepo: invRepo,
	}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(txm),
		WithIdempotencyStore(store),
	)

	amount := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	input := ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          amount,
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-87",
	}

	// First call — must fail due to tx failure; compensation Refund fires;
	// key is marked as compensated.
	_, err := svc.ProcessPayment(context.Background(), inv.ID(), input)
	if err == nil {
		t.Fatal("expected first call to fail (tx failure)")
	}
	// Capture the refunded transaction id as reported by the gateway so we
	// can later assert the retried payment is NOT linked to it, without
	// relying on the trackingGateway's internal "txn-" naming convention.
	if len(gw.refundTxnIDs) != 1 {
		t.Fatalf("expected 1 compensation refund, got %d", len(gw.refundTxnIDs))
	}
	refundedTxnID := gw.refundTxnIDs[0]
	if !store.isMarked("key-87") {
		t.Fatal("expected key-87 to be marked as compensated after saga compensation")
	}

	// Second call — SAME service, SAME repo, SAME invoice, SAME key.
	// The service must detect the marker and charge with a new key.
	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), input)
	if err != nil {
		t.Fatalf("retry must succeed after compensation marker routes to new key: %v", err)
	}
	if pmt == nil {
		t.Fatal("expected payment from retry")
	}
	if pmt.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected completed status, got %q", pmt.Status())
	}

	// Critical regression check: the recorded payment must NOT be tied to
	// the refunded transaction. We compare against the gateway-reported
	// refunded txn id rather than a literal string, so this assertion holds
	// even if the trackingGateway's id format changes.
	if pmt.GatewayTransactionID() == refundedTxnID {
		t.Fatalf("payment is linked to the refunded transaction %q — Issue #87 regression", refundedTxnID)
	}

	// The gateway must have seen exactly two Charge calls, and the second must
	// have used a different key (not "key-87").
	if len(gw.chargeKeys) != 2 {
		t.Fatalf("expected 2 Charge calls, got %d (%v)", len(gw.chargeKeys), gw.chargeKeys)
	}
	if gw.chargeKeys[0] != "key-87" {
		t.Errorf("first Charge key should be the original, got %q", gw.chargeKeys[0])
	}
	if gw.chargeKeys[1] == "key-87" {
		t.Error("second Charge must use a fresh key, got the original")
	}
	if gw.chargeKeys[1] == "" {
		t.Error("fresh key must be non-empty")
	}

	// Exactly one completed payment must exist in the repository.
	if n := paymentRepo.countCompleted(); n != 1 {
		t.Errorf("expected exactly 1 completed payment in repo, got %d", n)
	}

	// The invoice must have recorded the payment exactly once.
	if inv.PaidAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("expected invoice paid amount %v, got %v", amount.Amount(), inv.PaidAmount().Amount())
	}

	// Mutation-C guard: the persisted Payment's IdempotencyKey must be the
	// EFFECTIVE key, not the original. If the success path accidentally
	// reverted to `SetIdempotencyKey(input.IdempotencyKey)`, the retry call
	// would save the payment under "key-87" — exactly the key linked to
	// the refunded gateway transaction. The RunInTx idempotency check on
	// subsequent retries would then miss (because the effective key is
	// what's stored in the idempotency store) and would double-record.
	if pmt.IdempotencyKey() == "key-87" {
		t.Errorf("persisted payment must carry the effective key, not the compensated original; got %q", pmt.IdempotencyKey())
	}
	if pmt.IdempotencyKey() == "" {
		t.Error("persisted payment IdempotencyKey must not be empty after a post-compensation retry")
	}
}

// --- Backwards compatibility: no store → pre-fix behavior ---

func TestProcessPayment_NoIdempotencyStore_LegacyBehavior(t *testing.T) {
	// When WithIdempotencyStore is not provided, ProcessPayment must behave
	// exactly as before: no re-charge logic, the original input key is used
	// verbatim at the gateway and the payment record. This preserves
	// backwards compatibility for existing consumers.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &trackingGateway{}
	paymentRepo := &mockPaymentRepo{}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-legacy",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gw.chargeKeys) != 1 {
		t.Fatalf("expected exactly 1 Charge call (no re-charge), got %d", len(gw.chargeKeys))
	}
	// The Charge must have used the input key verbatim — no derivation.
	if gw.chargeKeys[0] != "key-legacy" {
		t.Errorf("Charge key must be the input key unchanged, got %q", gw.chargeKeys[0])
	}
	// The persisted Payment must carry the input key as its IdempotencyKey
	// (unchanged — no ULID suffix leak from the retry path).
	if pmt == nil {
		t.Fatal("expected payment")
	}
	if pmt.IdempotencyKey() != "key-legacy" {
		t.Errorf("payment IdempotencyKey must equal input key, got %q", pmt.IdempotencyKey())
	}
}

// --- Compensation marker written on failure path ---

func TestProcessPayment_NewRetryEffectiveKey_UsesInjectedClock(t *testing.T) {
	// CLAUDE.md rule: time.Now() must never be called directly in domain
	// or application code — the service must obtain time via shared.Clock.
	// This test pins the invariant for newRetryEffectiveKey: the ULID
	// timestamp embedded in the generated effective key must correspond
	// to the injected FixedClock, not to wall-clock time.
	//
	// A regression that reverted to `ulid.Timestamp(time.Now())` would
	// produce a suffix whose ULID timestamp reflects the actual test
	// execution time (≈2026-04-11). With the Clock-backed fix, the ULID
	// timestamp is frozen at 2026-01-15 (the FixedClock time) — verifiable
	// by decoding the ULID and checking its Time() component.
	clock := newPaymentTestClock() // fixed at 2026-01-15 UTC
	inv := newSimpleFinalizedInvoice()
	store := newFakeIdempotencyStore()

	svc := NewPaymentService(
		&trackingGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(&paymentFailingTxManager{}),
		WithIdempotencyStore(store),
	)

	_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-clock",
	})

	eff := store.mapping["key-clock"]
	if eff == "" {
		t.Fatal("expected compensation marker to be written")
	}
	// effective key layout: "<original>-<26-char ULID>"
	const ulidLen = 26
	if len(eff) < len("key-clock-")+ulidLen {
		t.Fatalf("unexpected effective key format: %q", eff)
	}
	ulidStr := eff[len("key-clock-"):]
	parsed, err := ulid.Parse(ulidStr)
	if err != nil {
		t.Fatalf("failed to parse ULID %q: %v", ulidStr, err)
	}
	got := ulid.Time(parsed.Time())
	want := clock.Now()
	// ULID time has millisecond precision. Compare at millisecond level.
	if got.UnixMilli() != want.UnixMilli() {
		t.Errorf("ULID timestamp must match FixedClock; got %v, want %v", got, want)
	}
}

func TestProcessPayment_CompensationFires_MarksKey(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	store := newFakeIdempotencyStore()

	svc := NewPaymentService(
		&trackingGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(&paymentFailingTxManager{}),
		WithIdempotencyStore(store),
	)

	_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-mark",
	})

	if !store.isMarked("key-mark") {
		t.Error("expected key-mark to be marked after compensation")
	}
}

// --- Empty IdempotencyKey must not attempt any store interaction ---

func TestProcessPayment_EmptyKey_StoreNotConsulted(t *testing.T) {
	// An empty IdempotencyKey cannot be meaningfully tracked by the store
	// (the gateway has no way to replay an empty key either), so the service
	// must skip ResolveEffectiveKey / MarkCompensated entirely and proceed.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	store := newFakeIdempotencyStore()
	gw := &trackingGateway{}
	paymentRepo := &mockPaymentRepo{}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithIdempotencyStore(store),
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "", // empty
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.resolveCalls != 0 {
		t.Errorf("ResolveEffectiveKey must not be called for empty key, got %d calls", store.resolveCalls)
	}
	if store.markCalls != 0 {
		t.Errorf("MarkCompensated must not be called for empty key, got %d calls", store.markCalls)
	}

	// The charge must have gone through normally with an empty key and no
	// derivation (i.e. no "-"+ULID suffix leaking from the retry helper).
	if len(gw.chargeKeys) != 1 {
		t.Fatalf("expected exactly 1 Charge call, got %d", len(gw.chargeKeys))
	}
	if gw.chargeKeys[0] != "" {
		t.Errorf("Charge must receive the empty key verbatim, got %q", gw.chargeKeys[0])
	}

	// The saved Payment must not have a spurious IdempotencyKey.
	if pmt == nil {
		t.Fatal("expected payment")
	}
	if pmt.IdempotencyKey() != "" {
		t.Errorf("payment IdempotencyKey must remain empty, got %q", pmt.IdempotencyKey())
	}
}

// --- ResolveEffectiveKey storage error is fatal ---

func TestProcessPayment_ResolveEffectiveKeyError_FailsFast(t *testing.T) {
	// If the store fails to answer, we cannot safely proceed: a compensated
	// key would be replayed against the gateway. The service must return the
	// error before charging anything.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	store := newFakeIdempotencyStore()
	store.resolveErr = fmt.Errorf("database unreachable")
	gw := &trackingGateway{}

	svc := NewPaymentService(
		gw,
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithIdempotencyStore(store),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-store-err",
	})
	if err == nil {
		t.Fatal("expected error when ResolveEffectiveKey fails")
	}
	if len(gw.chargeKeys) != 0 {
		t.Errorf("gateway must not be charged when idempotency check fails, got %d calls", len(gw.chargeKeys))
	}
}

// --- MarkCompensated failure is non-fatal (logged only) ---

func TestProcessPayment_MarkCompensatedError_NonFatal(t *testing.T) {
	// If the gateway charge succeeded and we compensated it, but then the
	// marker write fails, we must NOT pretend the payment succeeded. However,
	// returning a new error would confuse the caller (which already sees a
	// compensation refund). The service logs the marker write failure and
	// returns the original save-failure error to the caller.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	store := newFakeIdempotencyStore()
	store.markErr = fmt.Errorf("marker write failed")

	svc := NewPaymentService(
		&trackingGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(&paymentFailingTxManager{}),
		WithIdempotencyStore(store),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-mark-err",
	})
	if err == nil {
		t.Fatal("expected an error (local save failed and was compensated)")
	}
	// The error should be about the original save failure, not the marker.
	if !strings.Contains(err.Error(), "local save failed") {
		t.Errorf("expected error to mention local save failure, got: %v", err)
	}
	// The marker-write failure must NOT leak into the returned error —
	// callers need to see the primary cause (save failure), not a secondary
	// bookkeeping error they cannot act on.
	if strings.Contains(err.Error(), "marker write failed") {
		t.Errorf("marker write error must not leak into returned error, got: %v", err)
	}
	// The mark attempt should still have been made (once), proving the
	// service called MarkCompensated before swallowing its failure.
	if store.markCalls != 1 {
		t.Errorf("expected 1 MarkCompensated call, got %d", store.markCalls)
	}
}

// --- H3: ProcessPayment converges on a stable effective key across retries ---

func TestProcessPayment_CompensatedKey_StableEffectiveKeyAcrossRetries(t *testing.T) {
	// Locks in the end-to-end invariant that multiple retries of the SAME
	// original key (after a single compensation) all resolve to the SAME
	// effective key via the IdempotencyStore. If a regression caused the
	// service to regenerate the effective key on every retry, the second
	// and third Charge calls would use distinct keys.
	//
	// Scenario (single service, single repo, single invoice):
	//   1. Call #1: Charge success → local save fails → saga compensation →
	//      store maps "key-h3" → "key-h3-<ULID>" (mapping M1).
	//   2. Call #2: resolves to M1 → Charge with effective key → local save
	//      fails again → saga compensation → MarkCompensated with a freshly
	//      generated M2, BUT first-call-wins preserves M1 in the store.
	//   3. Call #3: resolves to M1 (same as call #2), Charge with the same
	//      effective key as call #2. Gateway sees the same key — idempotent
	//      replay. This time tx succeeds, payment is saved.
	//
	// All three retries after the original must share the same effective
	// key (stable across retries). This proves ProcessPayment is reading
	// the stored value via ResolveEffectiveKey rather than generating a
	// fresh ULID each time.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	gw := &trackingGateway{}

	// Fail the first two tx calls so compensation fires twice. The second
	// MarkCompensated must be a no-op (first-call-wins) and subsequent
	// retries must resolve to the originally stored effective key.
	txm := &switchableTxManager{
		failUntil:   2,
		paymentRepo: paymentRepo,
		invoiceRepo: invRepo,
	}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(txm),
		WithIdempotencyStore(store),
	)

	input := ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-h3",
	}

	// 1st: fail + compensate (mapping created as M1)
	_, err1 := svc.ProcessPayment(context.Background(), inv.ID(), input)
	if err1 == nil {
		t.Fatal("first attempt should fail")
	}
	// 2nd: resolves M1, fails again, compensation fires — mapping must STAY
	// as M1 (first-call-wins).
	_, err2 := svc.ProcessPayment(context.Background(), inv.ID(), input)
	if err2 == nil {
		t.Fatal("second attempt should fail")
	}
	// 3rd: resolves M1 (same as call #2), charge succeeds this time.
	_, err3 := svc.ProcessPayment(context.Background(), inv.ID(), input)
	if err3 != nil {
		t.Fatalf("third attempt should succeed: %v", err3)
	}

	// Three Charge calls observed. Index 0 used the original key; indices
	// 1 and 2 must both use the SAME effective key (the stored M1).
	if len(gw.chargeKeys) != 3 {
		t.Fatalf("expected 3 Charge calls, got %d (%v)", len(gw.chargeKeys), gw.chargeKeys)
	}
	if gw.chargeKeys[0] != "key-h3" {
		t.Errorf("first Charge key should be the original, got %q", gw.chargeKeys[0])
	}
	if gw.chargeKeys[1] == "key-h3" {
		t.Error("second Charge must use the resolved effective key, got the original")
	}
	if gw.chargeKeys[2] != gw.chargeKeys[1] {
		t.Errorf("third Charge must use the SAME effective key as the second (first-call-wins); got %q then %q",
			gw.chargeKeys[1], gw.chargeKeys[2])
	}

	// The store must have been consulted on every call (3 resolves).
	if store.resolveCalls != 3 {
		t.Errorf("expected 3 ResolveEffectiveKey calls, got %d", store.resolveCalls)
	}

	// MarkCompensated should have been invoked twice (calls #1 and #2),
	// but the stored mapping must reflect ONLY the first call's value.
	if store.markCalls != 2 {
		t.Errorf("expected 2 MarkCompensated calls, got %d", store.markCalls)
	}

	// Exactly one completed Payment persisted in the repo.
	if n := paymentRepo.countCompleted(); n != 1 {
		t.Errorf("expected exactly 1 completed payment, got %d", n)
	}
}

// --- H1: Compensated key + 3DS retry uses effective key everywhere ---

func TestProcessPayment_CompensatedKey_Then3DS_UsesEffectiveKey(t *testing.T) {
	// Scenario exposing the H1 bug: after a compensation, a subsequent retry
	// hits the 3DS (requires_action) branch. The pending payment record and
	// the in-repo idempotency check must both use the EFFECTIVE key. Before
	// the fix, the 3DS branch used input.IdempotencyKey, so the pending
	// payment was saved under the ORIGINAL key while the gateway charge used
	// the EFFECTIVE key — later completion-path retries wouldn't find the
	// pending record and would create a duplicate payment.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()

	// Pre-populate the store so the retry call is in the "post-compensation"
	// state without actually running a failed first call. This keeps the
	// test focused on the 3DS interaction.
	_ = store.MarkCompensated(context.Background(), "key-3ds-after-comp", "effective-3ds-001")

	// The gateway returns requires_action — it records the IdempotencyKey
	// so we can verify it was the effective key, not the original.
	gw := &trackingGateway{
		chargeResponses: []port.ChargeResponse{
			{
				TransactionID: "txn-3ds-001",
				Status:        port.TransactionStatusRequiresAction,
				Amount:        shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
				CreatedAt:     time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithIdempotencyStore(store),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-3ds-after-comp",
	})
	if !errors.Is(err, ErrRequiresAction) {
		t.Fatalf("expected ErrRequiresAction, got: %v", err)
	}

	// The gateway must have been charged with the EFFECTIVE key, not the original.
	if len(gw.chargeKeys) != 1 {
		t.Fatalf("expected 1 Charge call, got %d", len(gw.chargeKeys))
	}
	if gw.chargeKeys[0] != "effective-3ds-001" {
		t.Errorf("expected Charge key %q, got %q", "effective-3ds-001", gw.chargeKeys[0])
	}

	// The pending payment must be stored under the EFFECTIVE key, not the
	// original. Otherwise later completion retries (success path) would
	// query with the effective key and miss the pending record.
	foundByEffective, _ := paymentRepo.FindByIdempotencyKey(context.Background(), "effective-3ds-001")
	if foundByEffective == nil {
		t.Error("pending payment must be findable by the effective key")
	}
	foundByOriginal, _ := paymentRepo.FindByIdempotencyKey(context.Background(), "key-3ds-after-comp")
	if foundByOriginal != nil {
		t.Error("pending payment must NOT be stored under the original key")
	}
}

// --- H2: RunInTx idempotency check must use effectiveKey ---

func TestProcessPayment_CompensatedKey_InTxIdempotencyCheckUsesEffectiveKey(t *testing.T) {
	// This test locks in the invariant that the RunInTx idempotency check
	// uses the EFFECTIVE key. It was previously a silent coverage hole:
	// reverting the check from `effectiveKey` to `input.IdempotencyKey`
	// left all other tests green because none of them pre-populated the
	// repo with a payment under an effective key.
	//
	// Setup: the store has already mapped "key-h2-orig" → "effective-h2".
	// The fake repo is seeded with a COMPLETED payment under the effective
	// key (simulating "a prior retry already succeeded"). A new ProcessPayment
	// call with the original key must find that existing payment via the
	// RunInTx check and return it, NOT double-record on the invoice.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	initialPaid := inv.PaidAmount()

	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	_ = store.MarkCompensated(context.Background(), "key-h2-orig", "effective-h2")

	// Seed the repo with a completed payment under the EFFECTIVE key.
	existing, _ := payment.NewPayment(
		shared.NewPaymentID(),
		inv.ID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"txn-effective-h2",
		clock.Now(),
	)
	existing.SetIdempotencyKey("effective-h2")
	_ = existing.Complete()
	paymentRepo.seed(existing)

	gw := &trackingGateway{}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithIdempotencyStore(store),
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-h2-orig",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The returned payment must be the SEEDED one (same ID), proving the
	// in-tx check used the effective key and returned the existing record
	// instead of creating a new Payment.
	if pmt == nil {
		t.Fatal("expected payment to be returned")
	}
	if pmt.ID() != existing.ID() {
		t.Errorf("expected the seeded payment ID %q, got %q — RunInTx lookup did not hit the effective key",
			existing.ID(), pmt.ID())
	}

	// Only ONE completed payment must exist; the seeded one.
	if n := paymentRepo.countCompleted(); n != 1 {
		t.Errorf("expected exactly 1 completed payment, got %d", n)
	}

	// The invoice must not have been double-recorded.
	if inv.PaidAmount().Amount().Cmp(initialPaid.Amount()) != 0 {
		t.Errorf("invoice paid amount mutated despite idempotent replay: before %v, after %v",
			initialPaid.Amount(), inv.PaidAmount().Amount())
	}
}

// --- H1 secondary: 3DS retry with same effective key returns the existing pending ---

func TestProcessPayment_CompensatedKey_Then3DS_IdempotentRetryReturnsExisting(t *testing.T) {
	// After a compensated-then-3DS first attempt saves a pending payment
	// under the effective key, a second attempt with the same original key
	// must resolve to the same effective key, find the pending payment, and
	// return it (not create a duplicate).
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	_ = store.MarkCompensated(context.Background(), "key-3ds-dup", "effective-3ds-dup")

	gw := &trackingGateway{
		chargeResponses: []port.ChargeResponse{
			{TransactionID: "txn-3ds-dup", Status: port.TransactionStatusRequiresAction, Amount: shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)},
			{TransactionID: "txn-3ds-dup", Status: port.TransactionStatusRequiresAction, Amount: shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)},
		},
	}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithIdempotencyStore(store),
	)

	input := ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-3ds-dup",
	}

	p1, err1 := svc.ProcessPayment(context.Background(), inv.ID(), input)
	p2, err2 := svc.ProcessPayment(context.Background(), inv.ID(), input)

	if !errors.Is(err1, ErrRequiresAction) || !errors.Is(err2, ErrRequiresAction) {
		t.Fatalf("both calls must return ErrRequiresAction, got %v / %v", err1, err2)
	}
	// Second call must return the EXISTING pending payment (not a fresh one).
	if p2 == nil || p2.IdempotencyKey() != "effective-3ds-dup" {
		t.Errorf("second call must return the existing pending payment under the effective key, got %+v", p2)
	}

	// Mutation-A guard: both returned payments MUST share the same ID. If
	// the 3DS idempotency lookup were (accidentally) reverted to use
	// input.IdempotencyKey instead of effectiveKey, the second call would
	// miss the existing record and save a fresh pending payment — a new
	// object with a different ID but the same IdempotencyKey.
	if p1 == nil || p2 == nil {
		t.Fatal("both calls must return a payment")
	}
	if p1.ID() != p2.ID() {
		t.Errorf("retried 3DS call must return the SAME pending payment instance; got %q then %q", p1.ID(), p2.ID())
	}

	// Repo must contain exactly ONE pending payment under the effective key,
	// confirming the second call did not persist a duplicate.
	allUnderEff := 0
	allPending := 0
	for _, p := range paymentRepo.byID {
		if p.IdempotencyKey() == "effective-3ds-dup" {
			allUnderEff++
		}
		if p.Status() == payment.PaymentStatusPending {
			allPending++
		}
	}
	if allUnderEff != 1 {
		t.Errorf("expected exactly 1 payment stored under effective-3ds-dup, got %d", allUnderEff)
	}
	if allPending != 1 {
		t.Errorf("expected exactly 1 pending payment in repo, got %d", allPending)
	}
}

// --- HIGH-1: Documented limitation — MarkCompensated failure leaves race window open ---

func TestProcessPayment_MarkCompensatedError_SubsequentRetry_ReplaysOriginalKey(t *testing.T) {
	// Documents the known limitation flagged in port/idempotency_store.go:
	// when MarkCompensated itself fails, the next retry cannot find the
	// marker and proceeds with the ORIGINAL key. If the local tx succeeds
	// on the retry, the payment is recorded against the ORIGINAL gateway
	// charge — which, from the gateway's point of view, has already been
	// refunded. This is Issue #87 recurring.
	//
	// This test pins down the exact failure mode so a future change that
	// silently alters it (e.g. falling back to a fresh effective key on
	// MarkCompensated error) fails loudly.
	//
	// Operators: the "failed to mark idempotency key as compensated" log
	// line is the canary for this scenario. Monitor it and investigate
	// store health; do not disable or lower its severity.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	store.markErr = fmt.Errorf("persistent store outage")
	gw := &trackingGateway{}

	// Only the first tx fails; the second succeeds so we can observe the
	// inconsistency on the retry.
	txm := &switchableTxManager{
		failUntil:   1,
		paymentRepo: paymentRepo,
		invoiceRepo: invRepo,
	}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(txm),
		WithIdempotencyStore(store),
	)

	input := ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-mark-err-retry",
	}

	// 1st: fails at tx, compensation fires, MarkCompensated fails (logged only).
	_, err1 := svc.ProcessPayment(context.Background(), inv.ID(), input)
	if err1 == nil {
		t.Fatal("expected first call to fail")
	}
	if len(gw.refundTxnIDs) != 1 {
		t.Fatalf("expected 1 compensation refund after first call, got %d", len(gw.refundTxnIDs))
	}
	// Sanity: MarkCompensated was attempted and failed — store has no mapping.
	if store.markCalls != 1 {
		t.Errorf("expected 1 MarkCompensated attempt on first call, got %d", store.markCalls)
	}
	if _, ok, _ := store.ResolveEffectiveKey(context.Background(), input.IdempotencyKey); ok {
		t.Fatal("store must have NO mapping after MarkCompensated failure")
	}

	// 2nd: retry — store returns (not found), service falls back to the
	// ORIGINAL key. Gateway-side the charge is idempotent-replayed (same
	// txn id as call 1). The local tx now succeeds and records the payment
	// under that same refunded transaction id.
	pmt, _ := svc.ProcessPayment(context.Background(), inv.ID(), input)

	// Documented (broken) behavior:
	if len(gw.chargeKeys) != 2 {
		t.Fatalf("expected 2 Charge calls, got %d", len(gw.chargeKeys))
	}
	if gw.chargeKeys[0] != "key-mark-err-retry" || gw.chargeKeys[1] != "key-mark-err-retry" {
		t.Errorf("both Charge calls must use the original key; got %v", gw.chargeKeys)
	}
	// The gateway replayed the cached response, so the second call's
	// transaction id matches the FIRST (refunded) one. Guard with an
	// explicit nil check so a silent regression to "pmt = nil" fails
	// loudly rather than short-circuiting the assertion.
	if pmt == nil {
		t.Fatal("documented limitation: retry must return a Payment instance linked to the refunded transaction; got nil")
	}
	if pmt.GatewayTransactionID() != gw.refundTxnIDs[0] {
		t.Errorf("documented limitation: retry payment SHOULD be linked to the refunded transaction id %q, got %q — this is NOT a regression; it's the known race that monitoring the marker error log must catch",
			gw.refundTxnIDs[0], pmt.GatewayTransactionID())
	}
}

// --- BLOCKER-1 PROBE: 3DS → Captured on retry must upgrade the pending record ---

func TestProcessPayment_3DS_ThenCaptured_UpgradesPendingToCompleted(t *testing.T) {
	// Scenario surfaced by the v3 adversarial review:
	//   1. Call 1 — gateway returns requires_action; pending payment persisted.
	//   2. Call 2 — user completed 3DS on the hosted page, so the gateway now
	//      returns Captured for the SAME idempotency key (this is what Stripe
	//      PaymentIntents, Adyen, and Braintree do when the customer finishes
	//      authentication and the gateway auto-captures).
	//   3. Correct behavior: the existing Pending payment is upgraded to
	//      Completed AND the invoice's RecordPayment fires. The returned
	//      Payment must have status Completed and the invoice must show the
	//      amount as paid.
	//
	// Before the BLOCKER-1 fix, the RunInTx idempotency check returned the
	// Pending payment as-is without calling Complete()/RecordPayment(), so
	// the caller saw "success" but the invoice paid amount stayed zero.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()

	amount := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	gw := &trackingGateway{
		chargeResponses: []port.ChargeResponse{
			{
				TransactionID: "txn-3ds-K",
				Status:        port.TransactionStatusRequiresAction,
				Amount:        amount,
				CreatedAt:     time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
			},
			{
				TransactionID: "txn-3ds-K",
				Status:        port.TransactionStatusCaptured, // 3DS completed, gateway auto-captured
				Amount:        amount,
				CreatedAt:     time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	input := ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          amount,
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-3ds-captured",
	}

	// Call 1: expect ErrRequiresAction with a pending payment saved.
	_, err1 := svc.ProcessPayment(context.Background(), inv.ID(), input)
	if !errors.Is(err1, ErrRequiresAction) {
		t.Fatalf("first call must return ErrRequiresAction, got: %v", err1)
	}
	if inv.PaidAmount().Amount().Sign() != 0 {
		t.Errorf("invoice must not be paid yet after 3DS pending, got %v", inv.PaidAmount().Amount())
	}

	// Snapshot the pre-call-2 state so we can detect whether Save was
	// actually invoked in the upgrade path (vs pointer-aliased mutation
	// that would be invisible otherwise).
	pendingBeforeCall2, _ := paymentRepo.FindByIdempotencyKey(context.Background(), "key-3ds-captured")
	if pendingBeforeCall2 == nil {
		t.Fatal("setup error: pending payment must be findable after call 1")
	}
	pendingID := pendingBeforeCall2.ID()
	priorPaymentSaveCount := paymentRepo.saveCount
	priorInvoiceSaveCount := invRepo.saveCount

	// Call 2: gateway returns Captured. The service must upgrade the pending
	// record to Completed and record the payment on the invoice.
	pmt, err2 := svc.ProcessPayment(context.Background(), inv.ID(), input)
	if err2 != nil {
		t.Fatalf("second call must succeed, got: %v", err2)
	}
	if pmt == nil {
		t.Fatal("expected payment")
	}
	if pmt.Status() != payment.PaymentStatusCompleted {
		t.Errorf("pending payment must be upgraded to Completed, got status %q", pmt.Status())
	}
	if inv.PaidAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("invoice must be recorded as paid at %v, got %v", amount.Amount(), inv.PaidAmount().Amount())
	}
	if n := paymentRepo.countCompleted(); n != 1 {
		t.Errorf("expected exactly 1 completed payment, got %d", n)
	}
	// Mutation M-SW6 guard: the Pending → Completed upgrade MUST persist
	// via repos.Payments.Save. A pointer-aliased in-memory mutation is
	// NOT sufficient — a durable SQL repo would lose the update. We assert
	// the save spy recorded at least one Save for this payment ID where
	// the snapshot captured Status=Completed.
	if !paymentRepo.wasSavedWithStatus(pendingID, payment.PaymentStatusCompleted) {
		t.Errorf("Pending upgrade must call repos.Payments.Save(existing) with Status=Completed; save history: %v", paymentRepo.saveHistory)
	}
	if paymentRepo.saveCount <= priorPaymentSaveCount {
		t.Error("Pending upgrade must call Save at least once; saveCount did not advance")
	}
	// Mutation M-SW7 guard: the invoice must also be saved (so the paid
	// amount is durable in a real repo, not just in the pointer-aliased
	// in-memory invoice).
	if invRepo.saveCount <= priorInvoiceSaveCount {
		t.Error("Pending upgrade must call repos.Invoices.Save(inv); invRepo.saveCount did not advance")
	}
	// The last invoice save must show the full paid amount.
	if len(invRepo.saveAmount) == 0 {
		t.Error("invRepo.saveAmount is empty")
	} else {
		lastAmt := invRepo.saveAmount[len(invRepo.saveAmount)-1]
		if lastAmt.Cmp(amount.Amount()) != 0 {
			t.Errorf("last invoice Save recorded paid amount %v, want %v", lastAmt, amount.Amount())
		}
	}
}

// --- HIGH-v4-A: Non-Completed terminal states must not pass through as success ---

// --- BLOCKER v5: Terminal-state existing must short-circuit BEFORE Charge ---

func TestProcessPayment_ExistingTerminalState_ShortCircuits_NoGatewayNoMarker(t *testing.T) {
	// Live-reachable scenario discovered in the v5 adversarial review:
	// When a retry collides with an already-terminal payment (Refunded,
	// Failed, PartiallyRefunded, ChargedBack), the service must NOT:
	//   (a) call gateway.Charge — no spurious idempotent replay or wasted
	//       gateway API calls / rate-limit consumption
	//   (b) call gateway.Refund via saga compensation — no double-refund
	//       attempt against an already-refunded transaction
	//   (c) call store.MarkCompensated — no spurious marker write that
	//       would burn the original key for future legitimate retries
	//
	// Before the fix, terminal-state rejection happened INSIDE RunInTx,
	// which meant the rejection path went through saga.Compensate and
	// emitted a false "MANUAL RECONCILIATION REQUIRED" log line.
	tests := []struct {
		name   string
		mutate func(p *payment.Payment) // drives the existing payment to the target terminal state
	}{
		{
			name: "Refunded",
			mutate: func(p *payment.Payment) {
				_ = p.Complete()
				_ = p.MarkRefunded()
			},
		},
		{
			name: "PartiallyRefunded",
			mutate: func(p *payment.Payment) {
				_ = p.Complete()
				_ = p.MarkPartiallyRefunded()
			},
		},
		{
			name: "Failed",
			mutate: func(p *payment.Payment) {
				_ = p.Fail("card declined")
			},
		},
		{
			name: "ChargedBack",
			mutate: func(p *payment.Payment) {
				_ = p.Complete()
				_ = p.MarkChargedBack()
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := newPaymentTestClock()
			inv := newSimpleFinalizedInvoice()
			invRepo := &mockInvoiceRepoForPayment{inv: inv}
			paymentRepo := newFakePaymentRepo()
			store := newFakeIdempotencyStore()
			gw := &trackingGateway{}

			existing, _ := payment.NewPayment(
				shared.NewPaymentID(),
				inv.ID(),
				shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
				payment.PaymentMethodCreditCard,
				"txn-existing-terminal",
				clock.Now(),
			)
			existing.SetIdempotencyKey("key-terminal")
			tc.mutate(existing)
			paymentRepo.seed(existing)

			svc := NewPaymentService(
				gw,
				paymentRepo,
				invRepo,
				nil,
				&mockEventStore{},
				plugin.NewRegistry(),
				clock,
				WithIdempotencyStore(store),
			)

			pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
				PaymentMethodID: "pm-001",
				Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
				Currency:        shared.CurrencyJPY,
				IdempotencyKey:  "key-terminal",
			})

			// Must return an error, nil payment.
			if err == nil {
				t.Fatalf("expected error for terminal state %s", tc.name)
			}
			if pmt != nil {
				t.Errorf("expected nil payment, got %+v", pmt)
			}

			// Error must be a DomainError with ErrCodeConflict (not InvalidStateTransition).
			var domainErr *shared.DomainError
			if !errors.As(err, &domainErr) {
				t.Errorf("expected shared.DomainError, got %T: %v", err, err)
			} else if domainErr.Code != shared.ErrCodeConflict {
				t.Errorf("expected error code %q, got %q", shared.ErrCodeConflict, domainErr.Code)
			}

			// BLOCKER guard: gateway must NOT have been charged.
			if len(gw.chargeKeys) != 0 {
				t.Errorf("gateway must not be charged on terminal-state short-circuit; got chargeKeys=%v", gw.chargeKeys)
			}
			// BLOCKER guard: gateway must NOT have been refunded.
			if len(gw.refundTxnIDs) != 0 {
				t.Errorf("gateway must not be refunded on terminal-state short-circuit; got refundTxnIDs=%v", gw.refundTxnIDs)
			}
			// BLOCKER guard: store must NOT have been marked compensated.
			if store.markCalls != 0 {
				t.Errorf("store must not be marked compensated on terminal-state short-circuit; got markCalls=%d", store.markCalls)
			}
		})
	}
}

// --- MED-1: BeforeCharge must NOT fire when pre-charge short-circuits ---

func TestProcessPayment_ExistingTerminalState_DoesNotFireBeforeCharge(t *testing.T) {
	// BeforeCharge hooks are a pre-flight contract: they run right before
	// the gateway is actually charged so plugins can reserve inventory,
	// emit audit events, or do any "about to charge" bookkeeping. When
	// the pre-charge idempotency lookup short-circuits a retry because
	// the existing payment is in a terminal state, NO gateway charge
	// happens — so BeforeCharge hooks must NOT fire either. Firing them
	// for a charge that never happens leaks phantom reservations and
	// audit noise into consumer systems.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()

	existing, _ := payment.NewPayment(
		shared.NewPaymentID(),
		inv.ID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"txn-existing",
		clock.Now(),
	)
	existing.SetIdempotencyKey("key-terminal-bc")
	_ = existing.Complete()
	_ = existing.MarkRefunded()
	paymentRepo.seed(existing)

	spy := &beforeChargeSpyPlugin{}
	registry := plugin.NewRegistry()
	_ = registry.Register(spy)

	svc := NewPaymentService(
		&trackingGateway{},
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		registry,
		clock,
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-terminal-bc",
	})
	if err == nil {
		t.Fatal("expected error for terminal-state retry")
	}
	if spy.calls != 0 {
		t.Errorf("BeforeCharge must NOT fire when pre-charge short-circuits; got %d calls", spy.calls)
	}
}

// --- MED-1 regression: BeforeCharge still fires on the happy path ---

func TestProcessPayment_HappyPath_FiresBeforeCharge(t *testing.T) {
	// Negative guard for MED-1: the happy path (no existing payment,
	// regular Charge) MUST still fire BeforeCharge exactly once. If the
	// order change broke the happy-path invocation, this test catches it.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	spy := &beforeChargeSpyPlugin{}
	registry := plugin.NewRegistry()
	_ = registry.Register(spy)

	svc := NewPaymentService(
		&trackingGateway{},
		newFakePaymentRepo(),
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		registry,
		clock,
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-happy",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spy.calls != 1 {
		t.Errorf("BeforeCharge must fire exactly once on happy path; got %d calls", spy.calls)
	}
}

// --- Mutation D guard: in-tx lookup MUST use effectiveKey, not input key ---

func TestProcessPayment_InTxLookup_UsesEffectiveKeyWhenResolved(t *testing.T) {
	// This test pins the in-tx FindByIdempotencyKey to the EFFECTIVE key,
	// catching Mutation D (wrong key in RunInTx). Scenario:
	//
	//   1. Store has mapping "orig" → "eff".
	//   2. Seed a Pending payment in the repo under the EFFECTIVE key
	//      "eff" (simulating a prior 3DS call that already mapped + saved).
	//   3. Call ProcessPayment with input key "orig".
	//   4. Pre-charge lookup uses "eff" and sees Pending (Pending case
	//      is pass-through, so Charge proceeds).
	//   5. Gateway returns Captured.
	//   6. In-tx lookup MUST also use "eff" to find the same Pending
	//      record and upgrade it.
	//
	// If Mutation D reverts the in-tx lookup to `input.IdempotencyKey`
	// ("orig"), it misses the seeded Pending and creates a fresh Payment
	// with a new ID. The test catches this by asserting the returned
	// payment ID equals the seeded Pending's ID.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	_ = store.MarkCompensated(context.Background(), "orig-d", "eff-d")

	// Seed a Pending payment under the EFFECTIVE key.
	seeded, _ := payment.NewPayment(
		shared.NewPaymentID(),
		inv.ID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"txn-seeded-pending",
		clock.Now(),
	)
	seeded.SetIdempotencyKey("eff-d")
	paymentRepo.seed(seeded)
	seededID := seeded.ID()

	gw := &trackingGateway{} // returns Captured by default

	svc := NewPaymentService(
		gw,
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithIdempotencyStore(store),
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "orig-d",
	})
	if err != nil {
		t.Fatalf("expected upgrade path to succeed, got: %v", err)
	}
	if pmt == nil {
		t.Fatal("expected payment")
	}
	if pmt.ID() != seededID {
		t.Errorf("in-tx lookup must find the seeded Pending by effectiveKey; expected ID %q, got %q", seededID, pmt.ID())
	}
	if pmt.Status() != payment.PaymentStatusCompleted {
		t.Errorf("seeded Pending must be upgraded to Completed, got %q", pmt.Status())
	}
}

// --- Defense-in-depth: in-tx terminal rejection catches pre-charge/in-tx race ---

func TestProcessPayment_TerminalStateRace_InTxRejection(t *testing.T) {
	// Pre-charge lookup and in-tx lookup are two separate reads against
	// the payment repo. In a real SQL deployment, a concurrent writer
	// can land a terminal payment between these two reads. The pre-charge
	// read misses, so ProcessPayment proceeds to gateway.Charge and enters
	// RunInTx. The in-tx read then sees the terminal record and MUST
	// reject — otherwise the service would silently record a fresh
	// completed payment while the concurrent writer's terminal state
	// (Refunded/Failed/…) remains in the DB.
	//
	// This test pins the in-tx defense-in-depth. Without it, removing
	// the in-tx terminal rejection would silently pass the main test
	// suite (pre-charge catches everything in the happy path).
	tests := []struct {
		name     string
		mutate   func(p *payment.Payment)
		wantCode shared.ErrorCode
	}{
		{"Refunded", func(p *payment.Payment) { _ = p.Complete(); _ = p.MarkRefunded() }, shared.ErrCodeConflict},
		{"PartiallyRefunded", func(p *payment.Payment) { _ = p.Complete(); _ = p.MarkPartiallyRefunded() }, shared.ErrCodeConflict},
		{"Failed", func(p *payment.Payment) { _ = p.Fail("declined") }, shared.ErrCodeConflict},
		{"ChargedBack", func(p *payment.Payment) { _ = p.Complete(); _ = p.MarkChargedBack() }, shared.ErrCodeConflict},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := newPaymentTestClock()
			inv := newSimpleFinalizedInvoice()

			// Prepare the "delayed" terminal payment.
			delayed, _ := payment.NewPayment(
				shared.NewPaymentID(),
				inv.ID(),
				shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
				payment.PaymentMethodCreditCard,
				"txn-race-terminal",
				clock.Now(),
			)
			delayed.SetIdempotencyKey("key-race")
			tc.mutate(delayed)

			paymentRepo := newRaceFakePaymentRepo(delayed)
			gw := &trackingGateway{}

			svc := NewPaymentService(
				gw,
				paymentRepo,
				&mockInvoiceRepoForPayment{inv: inv},
				nil,
				&mockEventStore{},
				plugin.NewRegistry(),
				clock,
			)

			pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
				PaymentMethodID: "pm-001",
				Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
				Currency:        shared.CurrencyJPY,
				IdempotencyKey:  "key-race",
			})

			// Must return a ErrCodeConflict DomainError.
			if err == nil {
				t.Fatalf("expected error for in-tx terminal-state race (%s)", tc.name)
			}
			var domainErr *shared.DomainError
			if !errors.As(err, &domainErr) {
				t.Errorf("expected shared.DomainError, got %T: %v", err, err)
			} else if domainErr.Code != tc.wantCode {
				t.Errorf("expected error code %q, got %q", tc.wantCode, domainErr.Code)
			}
			if pmt != nil {
				t.Errorf("expected nil payment, got %+v", pmt)
			}

			// Pre-charge saw nothing → gateway WAS charged (that's the
			// point of the race simulation — the pre-charge defense
			// did not catch it).
			if len(gw.chargeKeys) != 1 {
				t.Errorf("expected 1 Charge call (pre-charge lookup missed), got %d", len(gw.chargeKeys))
			}

			// Compensation MUST have fired to refund the captured txn,
			// because the in-tx rejection returns a non-nil error from
			// the closure. This is the correct saga behavior for the
			// race case: the gateway actually charged, so we must
			// actually refund.
			if len(gw.refundTxnIDs) != 1 {
				t.Errorf("expected 1 compensation Refund after in-tx race rejection, got %d", len(gw.refundTxnIDs))
			}
		})
	}
}

func TestProcessPayment_ExistingRefunded_ReturnsError(t *testing.T) {
	// Live-reachable scenario exposed by the v4 adversarial review:
	//   1. Caller runs ProcessPayment with key "K" → Completed payment saved.
	//   2. Caller explicitly refunds the payment via PaymentService.Refund →
	//      the existing payment transitions to Refunded but its IdempotencyKey
	//      stays "K" (Refund does not mutate the key).
	//   3. A stale client (double-click, retry, queued job) calls
	//      ProcessPayment again with the same "K".
	//   4. Gateway replays the original Charge response (Captured).
	//   5. RunInTx looks up existing by key → finds the Refunded payment.
	//
	// Before the fix, step 5 returned the Refunded payment as (pmt, nil),
	// making the caller believe the charge succeeded and often causing
	// downstream code to act on a payment that had already been refunded.
	//
	// Correct behavior: return an error so the caller cannot mistake a
	// refunded payment for a fresh successful charge.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()

	// Seed a Refunded payment under the input key (no store in play — this
	// is the legacy path where input key == effective key).
	existing, _ := payment.NewPayment(
		shared.NewPaymentID(),
		inv.ID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"txn-existing-refunded",
		clock.Now(),
	)
	existing.SetIdempotencyKey("key-refunded")
	if err := existing.Complete(); err != nil {
		t.Fatalf("setup: Complete failed: %v", err)
	}
	if err := existing.MarkRefunded(); err != nil {
		t.Fatalf("setup: MarkRefunded failed: %v", err)
	}
	paymentRepo.seed(existing)

	svc := NewPaymentService(
		&trackingGateway{},
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-refunded",
	})

	if err == nil {
		t.Fatal("expected error when existing payment is Refunded")
	}
	if pmt != nil {
		t.Errorf("expected nil payment on Refunded-existing error, got %+v", pmt)
	}
}

func TestProcessPayment_ExistingFailed_ReturnsError(t *testing.T) {
	// Failed payments normally do NOT carry an IdempotencyKey (the failed
	// path at the top of ProcessPayment does not call SetIdempotencyKey),
	// but a future refactor could break that invariant. This test pins the
	// defensive guard so a seeded Failed payment under a matching key is
	// rejected rather than silently returned as "success".
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()

	existing, _ := payment.NewPayment(
		shared.NewPaymentID(),
		inv.ID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"txn-existing-failed",
		clock.Now(),
	)
	existing.SetIdempotencyKey("key-failed")
	if err := existing.Fail("card declined"); err != nil {
		t.Fatalf("setup: Fail failed: %v", err)
	}
	paymentRepo.seed(existing)

	svc := NewPaymentService(
		&trackingGateway{},
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-failed",
	})

	if err == nil {
		t.Fatal("expected error when existing payment is Failed")
	}
	if pmt != nil {
		t.Errorf("expected nil payment on Failed-existing error, got %+v", pmt)
	}
}

// --- Refund pre-flight validation (review: gateway must not fire on invalid refund) ---

// TestRefund_InvalidAmount_DoesNotCallGateway verifies that a refund which
// fails domain validation (amount exceeds the payment) is rejected BEFORE the
// irreversible gateway refund is invoked. Otherwise money moves at the gateway
// and the local save fails, forcing manual reconciliation.
func TestRefund_InvalidAmount_DoesNotCallGateway(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := &mockPaymentRepo{}
	gw := &spyGateway{}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-refund-validate",
	})
	if err != nil {
		t.Fatalf("unexpected error seeding payment: %v", err)
	}

	// Attempt to refund more than the payment amount → must be rejected up front.
	overAmount := shared.NewMoney(big.NewRat(20000, 1), shared.CurrencyJPY)
	err = svc.Refund(context.Background(), pmt.ID(), RefundInput{
		Amount: &overAmount,
		Reason: port.RefundReasonRequestedByCustomer,
	})
	if err == nil {
		t.Fatal("expected error for over-refund, got nil")
	}
	if gw.refundCalled {
		t.Error("gateway Refund must NOT be called when the refund fails validation")
	}
}

// newZeroAmountFinalizedInvoice builds a finalized invoice with AmountDue()==0
// (fully discounted / credited), the case a real gateway would reject on Charge.
func newZeroAmountFinalizedInvoice() *invoice.Invoice {
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		invoice.WithAmountDue(shared.Zero(shared.CurrencyJPY)),
	)
	if err != nil {
		panic("newZeroAmountFinalizedInvoice: " + err.Error())
	}
	_ = inv.Finalize()
	return inv
}

// TestProcessPayment_ZeroAmount_SettlesWithoutGateway verifies that a zero-amount
// invoice settles directly without calling the gateway (issue #197). The gateway
// is configured to fail if Charge is called, proving it is skipped.
func TestProcessPayment_ZeroAmount_SettlesWithoutGateway(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newZeroAmountFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	// failCharge: if the gateway is called, Charge errors and settlement fails.
	gw := &mockGateway{failCharge: true}

	svc := NewPaymentService(gw, &mockPaymentRepo{}, invRepo, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

	p, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		Amount:         shared.Zero(shared.CurrencyJPY),
		Currency:       shared.CurrencyJPY,
		IdempotencyKey: "idem-zero-1",
	})
	if err != nil {
		t.Fatalf("zero-amount settlement should succeed without gateway, got: %v", err)
	}
	if p == nil || p.Status() != payment.PaymentStatusCompleted {
		t.Fatalf("expected completed zero-amount payment, got %v", p)
	}
	if p.Amount().Amount().Sign() != 0 {
		t.Errorf("expected zero payment amount, got %s", p.Amount().Amount().RatString())
	}
	if inv.Status() != invoice.InvoiceStatusPaid {
		t.Errorf("expected invoice paid, got %s", inv.Status())
	}
}

// TestProcessPayment_ZeroAmount_NoPaymentMethodRequired verifies a zero-amount
// invoice settles even when no payment method can be resolved (issue #197).
func TestProcessPayment_ZeroAmount_NoPaymentMethodRequired(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newZeroAmountFinalizedInvoice()
	// No contract repo / customer gateway → ResolvePaymentMethod would fail, but
	// it must not be consulted for a zero settlement.
	svc := NewPaymentService(&mockGateway{failCharge: true}, &mockPaymentRepo{}, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

	p, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		Currency:       shared.CurrencyJPY,
		IdempotencyKey: "idem-zero-2",
	})
	if err != nil {
		t.Fatalf("zero settlement without a payment method should succeed, got: %v", err)
	}
	if p.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected completed, got %s", p.Status())
	}
}

// TestProcessPayment_ZeroAmount_Idempotent verifies that a zero-amount
// settlement honours the idempotency key: a completed payment already recorded
// under the key is returned without settling again or touching the gateway
// (issue #197). Mirrors TestProcessPayment_Idempotency_DoesNotMutateInvoiceForDuplicateKey.
func TestProcessPayment_ZeroAmount_Idempotent(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newZeroAmountFinalizedInvoice()

	existing, _ := payment.NewPayment(
		shared.NewPaymentID(), inv.ID(), shared.Zero(shared.CurrencyJPY),
		payment.PaymentMethodCreditCard, "", clock.Now(),
	)
	_ = existing.Complete()

	svc := NewPaymentService(&mockGateway{failCharge: true}, &mockPaymentRepo{existing: existing}, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

	p, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		Currency:       shared.CurrencyJPY,
		IdempotencyKey: "idem-zero-3",
	})
	if err != nil {
		t.Fatalf("idempotent zero replay failed: %v", err)
	}
	if p.ID() != existing.ID() {
		t.Errorf("expected the existing payment returned, got a new one")
	}
	// The invoice must not have been settled again.
	if !inv.PaidAmount().IsZero() {
		t.Errorf("invoice must not be re-settled on idempotent replay")
	}
}

// newZeroAmountFinalizedInvoiceWithID is newZeroAmountFinalizedInvoice with a
// caller-controlled ID, so a repo mock can return a FRESH instance per FindByID
// (mirroring a real RDBMS's per-read isolation) while keeping the ID stable.
func newZeroAmountFinalizedInvoiceWithID(id shared.InvoiceID) *invoice.Invoice {
	inv, err := invoice.NewInvoice(
		id,
		shared.AccountID("acc-zero-race"),
		shared.ContractID("con-zero-race"),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		invoice.WithAmountDue(shared.Zero(shared.CurrencyJPY)),
	)
	if err != nil {
		panic("newZeroAmountFinalizedInvoiceWithID: " + err.Error())
	}
	_ = inv.Finalize()
	return inv
}

// racedZeroInvoiceRepo returns a fresh invoice instance on every FindByID and
// records each returned instance, so a test can assert BY POINTER which copy
// the hooks observed (the post-convergence re-fetch vs the loser's local clone).
type racedZeroInvoiceRepo struct {
	mockInvoiceRepoForPayment
	id       shared.InvoiceID
	returned []*invoice.Invoice
}

func (r *racedZeroInvoiceRepo) FindByID(_ context.Context, _ shared.InvoiceID) (*invoice.Invoice, error) {
	inv := newZeroAmountFinalizedInvoiceWithID(r.id)
	r.returned = append(r.returned, inv)
	return inv, nil
}

// racedZeroPaymentRepo simulates the #97 race loser for the zero-amount path:
// the in-tx FindByIdempotencyKey sees no winner yet (the concurrent settlement
// has not committed when the check runs), Save then collides with the winner's
// unique idempotency key, and only after that collision does
// FindByIdempotencyKey surface the winner's record.
type racedZeroPaymentRepo struct {
	mockPaymentRepo
	winner        *payment.Payment
	saveAttempted bool
}

func (r *racedZeroPaymentRepo) Save(_ context.Context, _ *payment.Payment) error {
	r.saveAttempted = true
	return payment.ErrDuplicateIdempotencyKey
}

func (r *racedZeroPaymentRepo) FindByIdempotencyKey(_ context.Context, _ string) (*payment.Payment, error) {
	if r.saveAttempted {
		return r.winner, nil
	}
	return nil, nil
}

// TestProcessPayment_ZeroAmount_RacedLoser_RefetchesInvoiceForHooks verifies
// that the zero-amount settlement mirrors the gateway path's #97 convergence:
// when Save loses the duplicate-idempotency-key race and converges on the
// winner's payment, the invoice passed to AfterCharge / OnPaymentProcessed is
// RE-FETCHED from the repository (the winner's persisted state), not the
// loser's locally-mutated, never-persisted clone.
func TestProcessPayment_ZeroAmount_RacedLoser_RefetchesInvoiceForHooks(t *testing.T) {
	clock := newPaymentTestClock()
	invoiceID := shared.NewInvoiceID()
	invRepo := &racedZeroInvoiceRepo{id: invoiceID}

	winner, err := payment.NewPayment(
		shared.NewPaymentID(), invoiceID, shared.Zero(shared.CurrencyJPY),
		payment.PaymentMethodCreditCard, "", clock.Now(),
	)
	if err != nil {
		t.Fatalf("NewPayment: %v", err)
	}
	if err := winner.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	winner.SetIdempotencyKey("idem-zero-race")
	payRepo := &racedZeroPaymentRepo{winner: winner}

	spy := &onPaymentProcessedSpyPlugin{}
	reg := plugin.NewRegistry()
	if err := reg.Register(spy); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// failCharge gateway proves the settlement never touches the gateway.
	svc := NewPaymentService(&mockGateway{failCharge: true}, payRepo, invRepo, nil, &mockEventStore{}, reg, clock)

	p, err := svc.ProcessPayment(context.Background(), invoiceID, ProcessPaymentInput{
		Currency:       shared.CurrencyJPY,
		IdempotencyKey: "idem-zero-race",
	})
	if err != nil {
		t.Fatalf("raced-loser zero settlement must converge on the winner, got: %v", err)
	}
	if p.ID() != winner.ID() {
		t.Errorf("expected convergence on winner payment %s, got %s", winner.ID(), p.ID())
	}

	// FindByID must have been called three times: initial load, in-tx reload,
	// and the post-convergence re-fetch for the hooks.
	if got := len(invRepo.returned); got != 3 {
		t.Fatalf("expected 3 invoice FindByID calls (initial, in-tx, post-convergence re-fetch), got %d", got)
	}
	if !spy.called {
		t.Fatal("OnPaymentProcessed hook must fire on the raced-loser path")
	}
	// The hook must observe the re-fetched instance (index 2), not the in-tx
	// clone (index 1) whose RecordPayment mutation was never persisted.
	if spy.receivedInvoice != invRepo.returned[2] {
		t.Errorf("hooks must receive the re-fetched invoice (winner's persisted state), not the loser's local clone")
	}
	if spy.received == nil || spy.received.ID() != winner.ID() {
		t.Errorf("hooks must receive the winner payment")
	}
}

// contractLeakProbePlugin implements AfterChargeHook and OnPaymentProcessedHook.
// AfterCharge mutates its context via SetContract; OnPaymentProcessed records
// the contract it observes. It proves the two hook categories get ISOLATED
// PaymentContexts: a SetContract by an AfterCharge plugin must not leak into
// the metrics hooks (hidden inter-plugin coupling).
type contractLeakProbePlugin struct {
	clock            shared.Clock
	metricsCalled    bool
	metricsContract  *contract.ContractAggregate
	metricsPaymentID shared.PaymentID
}

func (p *contractLeakProbePlugin) Name() string    { return "contract-leak-probe" }
func (p *contractLeakProbePlugin) Version() string { return "1.0.0" }
func (p *contractLeakProbePlugin) Priority() int   { return 500 }
func (p *contractLeakProbePlugin) Initialize(_ context.Context, _ plugin.Config) error {
	return nil
}
func (p *contractLeakProbePlugin) Shutdown(_ context.Context) error { return nil }

func (p *contractLeakProbePlugin) AfterCharge(ctx *plugin.PaymentContext) error {
	ctx.SetContract(contract.NewContractAggregate(shared.ContractID("leaked-contract"), p.clock))
	return nil
}

func (p *contractLeakProbePlugin) OnPaymentProcessed(ctx *plugin.PaymentContext) error {
	p.metricsCalled = true
	p.metricsContract = ctx.Contract()
	if pm := ctx.Payment(); pm != nil {
		p.metricsPaymentID = pm.ID()
	}
	return nil
}

// TestProcessPayment_OnPaymentProcessed_ContextIsolatedFromAfterCharge verifies
// that OnPaymentProcessed hooks receive a FRESH PaymentContext, not the one the
// AfterCharge hooks ran against: a SetContract mutation by an AfterCharge
// plugin must not be observable by metrics plugins.
func TestProcessPayment_OnPaymentProcessed_ContextIsolatedFromAfterCharge(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	probe := &contractLeakProbePlugin{clock: clock}
	reg := plugin.NewRegistry()
	if err := reg.Register(probe); err != nil {
		t.Fatalf("Register: %v", err)
	}

	svc := NewPaymentService(&mockGateway{}, &mockPaymentRepo{}, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{}, reg, clock)

	if _, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "idem-ctx-isolation",
	}); err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}

	if !probe.metricsCalled {
		t.Fatal("OnPaymentProcessed must fire on the success path")
	}
	if probe.metricsContract != nil {
		t.Errorf("SetContract by an AfterCharge plugin leaked into the OnPaymentProcessed context: got contract %q, want nil",
			probe.metricsContract.ContractID())
	}
	if probe.metricsPaymentID == "" {
		t.Error("OnPaymentProcessed must still receive the payment on its fresh context")
	}
}

// TestProcessPayment_ZeroAmount_OnPaymentProcessed_ContextIsolatedFromAfterCharge
// verifies the same context isolation on the settleZeroAmountPayment path
// (AmountDue()==0, gateway never called): OnPaymentProcessed hooks must receive
// a FRESH PaymentContext, so a SetContract mutation by an AfterCharge plugin is
// not observable by metrics plugins on this path either.
func TestProcessPayment_ZeroAmount_OnPaymentProcessed_ContextIsolatedFromAfterCharge(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newZeroAmountFinalizedInvoice()
	probe := &contractLeakProbePlugin{clock: clock}
	reg := plugin.NewRegistry()
	if err := reg.Register(probe); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// failCharge gateway proves the settlement never touches the gateway.
	svc := NewPaymentService(&mockGateway{failCharge: true}, &mockPaymentRepo{}, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{}, reg, clock)

	if _, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		Currency:       shared.CurrencyJPY,
		IdempotencyKey: "idem-zero-ctx-isolation",
	}); err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}

	if !probe.metricsCalled {
		t.Fatal("OnPaymentProcessed must fire on the zero-amount settlement path")
	}
	if probe.metricsContract != nil {
		t.Errorf("SetContract by an AfterCharge plugin leaked into the OnPaymentProcessed context on the zero-amount path: got contract %q, want nil",
			probe.metricsContract.ContractID())
	}
	if probe.metricsPaymentID == "" {
		t.Error("OnPaymentProcessed must still receive the payment on its fresh context")
	}
}

// nilReturningInvoiceRepo mimics a BYO-DB adapter that violates the FindByID
// convention by returning (nil, nil) for a missing invoice instead of an error.
type nilReturningInvoiceRepo struct{ mockInvoiceRepoForPayment }

func (r *nilReturningInvoiceRepo) FindByID(_ context.Context, _ shared.InvoiceID) (*invoice.Invoice, error) {
	return nil, nil
}

// nilReturningPaymentRepo returns (nil, nil) for a missing payment.
type nilReturningPaymentRepo struct{ mockPaymentRepo }

func (r *nilReturningPaymentRepo) FindByID(_ context.Context, _ shared.PaymentID) (*payment.Payment, error) {
	return nil, nil
}

// TestProcessPayment_NilInvoice_ReturnsNotFound verifies the defensive nil-guard
// against a BYO-DB adapter returning (nil, nil) instead of a not-found error
// (issue #197): ProcessPayment returns a clean not-found error, not a nil panic.
func TestProcessPayment_NilInvoice_ReturnsNotFound(t *testing.T) {
	clock := newPaymentTestClock()
	svc := NewPaymentService(&mockGateway{}, &mockPaymentRepo{}, &nilReturningInvoiceRepo{}, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

	_, err := svc.ProcessPayment(context.Background(), shared.NewInvoiceID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "idem-nil-inv",
	})
	if err == nil {
		t.Fatal("expected not-found error for nil invoice")
	}
	assertDomainError(t, err, shared.ErrCodeNotFound)
}

// TestRefund_NilPayment_ReturnsNotFound verifies the defensive nil-guard in
// Refund against a (nil, nil) FindByID (issue #197).
func TestRefund_NilPayment_ReturnsNotFound(t *testing.T) {
	clock := newPaymentTestClock()
	svc := NewPaymentService(&mockGateway{}, &nilReturningPaymentRepo{}, &mockInvoiceRepoForPayment{}, nil, &mockEventStore{}, plugin.NewRegistry(), clock)

	err := svc.Refund(context.Background(), shared.NewPaymentID(), RefundInput{Reason: port.RefundReasonRequestedByCustomer})
	if err == nil {
		t.Fatal("expected not-found error for nil payment")
	}
	assertDomainError(t, err, shared.ErrCodeNotFound)
}

// TestRefund_FullRefund_ForwardsResolvedAmountToGateway verifies that a full
// refund (RefundInput.Amount == nil) sends the locally-resolved amount to the
// gateway rather than nil (issue #197), so the gateway and the ledger agree on
// exactly one figure.
func TestRefund_FullRefund_ForwardsResolvedAmountToGateway(t *testing.T) {
	clock := newPaymentTestClock()
	ctx := context.Background()

	inner := inmemory.NewInMemoryPaymentRepository()
	seed, err := payment.NewPayment(
		shared.NewPaymentID(),
		shared.NewInvoiceID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"gw_txn_197",
		clock.Now(),
	)
	if err != nil {
		t.Fatalf("NewPayment: %v", err)
	}
	if err := seed.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Record a prior partial refund so "remaining" (7000) differs from both the
	// full charge (10000) and nil — proving the resolved remaining is forwarded.
	if err := seed.RecordRefund(shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)); err != nil {
		t.Fatalf("RecordRefund: %v", err)
	}
	if err := inner.Save(ctx, seed); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	gw := &spyGateway{}
	svc := NewPaymentService(
		gw,
		inner,
		&mockInvoiceRepoForPayment{},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	// Full refund of the remainder: Amount left nil.
	if err := svc.Refund(ctx, seed.ID(), RefundInput{
		Reason: port.RefundReasonRequestedByCustomer,
	}); err != nil {
		t.Fatalf("Refund failed: %v", err)
	}

	if gw.refundReq == nil {
		t.Fatal("gateway Refund was not called")
	}
	if gw.refundReq.Amount == nil {
		t.Fatal("full refund must forward a non-nil amount to the gateway (issue #197)")
	}
	// remaining = 10000 - 3000 = 7000
	if gw.refundReq.Amount.Amount().Cmp(big.NewRat(7000, 1)) != 0 {
		t.Errorf("gateway refund amount = %s, want 7000 (resolved remaining)",
			gw.refundReq.Amount.Amount().RatString())
	}

	// Ledger must record the same 7000, so gateway and ledger agree.
	stored, err := inner.FindByID(ctx, seed.ID())
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	if stored.RefundedAmount().Amount().Cmp(big.NewRat(10000, 1)) != 0 {
		t.Errorf("ledger total refunded = %s, want 10000 (3000 prior + 7000 now)",
			stored.RefundedAmount().Amount().RatString())
	}
}

// --- OnPaymentProcessed metrics hook ---

type onPaymentProcessedSpyPlugin struct {
	called             bool
	received           *payment.Payment
	receivedInvoice    *invoice.Invoice
	receivedContractID shared.ContractID
	receivedAccountID  shared.AccountID
	err                error // if set, OnPaymentProcessed returns this error
}

func (p *onPaymentProcessedSpyPlugin) Name() string    { return "payment-processed-spy" }
func (p *onPaymentProcessedSpyPlugin) Version() string { return "1.0.0" }
func (p *onPaymentProcessedSpyPlugin) Initialize(_ context.Context, _ plugin.Config) error {
	return nil
}
func (p *onPaymentProcessedSpyPlugin) Shutdown(_ context.Context) error { return nil }
func (p *onPaymentProcessedSpyPlugin) Priority() int                    { return 500 }
func (p *onPaymentProcessedSpyPlugin) OnPaymentProcessed(ctx *plugin.PaymentContext) error {
	p.called = true
	p.received = ctx.Payment()
	p.receivedInvoice = ctx.Invoice()
	p.receivedContractID = ctx.ContractID()
	p.receivedAccountID = ctx.AccountID()
	return p.err
}

func TestProcessPayment_FiresOnPaymentProcessedHook(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	spy := &onPaymentProcessedSpyPlugin{}

	registry := plugin.NewRegistry()
	_ = registry.Register(spy)

	svc := NewPaymentService(&mockGateway{}, &mockPaymentRepo{}, invRepo, nil, &mockEventStore{}, registry, clock)

	p, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "idem-metrics-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !spy.called {
		t.Fatal("expected OnPaymentProcessed hook to be called on success path")
	}
	if spy.received == nil || spy.received.ID() != p.ID() {
		t.Error("OnPaymentProcessed hook received wrong payment")
	}
	if spy.received.Status() != payment.PaymentStatusCompleted {
		t.Errorf("hook saw payment status %s, want completed", spy.received.Status())
	}
	// Issue #223: the hook receives a *PaymentContext carrying the invoice, so
	// metrics plugins can attribute the payment to a contract/account.
	if spy.receivedInvoice == nil || spy.receivedInvoice.ID() != inv.ID() {
		t.Error("OnPaymentProcessed hook did not receive the paid invoice")
	}
	if spy.receivedContractID != inv.ContractID() {
		t.Errorf("hook saw contract ID %s, want %s", spy.receivedContractID, inv.ContractID())
	}
	if spy.receivedAccountID != inv.AccountID() {
		t.Errorf("hook saw account ID %s, want %s", spy.receivedAccountID, inv.AccountID())
	}
}

func TestProcessPayment_OnPaymentProcessedHookErrorIsNonFatal(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	spy := &onPaymentProcessedSpyPlugin{err: errors.New("metrics backend down")}

	registry := plugin.NewRegistry()
	_ = registry.Register(spy)

	svc := NewPaymentService(&mockGateway{}, &mockPaymentRepo{}, invRepo, nil, &mockEventStore{}, registry, clock)

	p, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "idem-metrics-2",
	})
	if err != nil {
		t.Fatalf("hook error must be non-fatal, got: %v", err)
	}
	if p == nil || p.Status() != payment.PaymentStatusCompleted {
		t.Error("payment must complete despite metrics hook failure")
	}
}

func TestProcessPayment_OnPaymentProcessedNotFiredOnGatewayFailure(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	spy := &onPaymentProcessedSpyPlugin{}

	registry := plugin.NewRegistry()
	_ = registry.Register(spy)

	svc := NewPaymentService(&mockGateway{failCharge: true}, &mockPaymentRepo{}, invRepo, nil, &mockEventStore{}, registry, clock)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "idem-metrics-3",
	})
	if err == nil {
		t.Fatal("expected gateway failure error")
	}
	if spy.called {
		t.Error("OnPaymentProcessed must not fire when the charge fails")
	}
}

// --- Refund optimistic-lock retry (issue #190) ---

// conflictInjectingPaymentRepo wraps a real in-memory payment repository and
// forces the first `failures` Save calls to return tx.ErrVersionConflict,
// simulating an optimistic-locking backend that lost a race to a concurrent
// writer. It lets the service-level Refund test exercise the RetryOnConflict
// wrapper deterministically without spinning up real goroutines.
type conflictInjectingPaymentRepo struct {
	inner     *inmemory.InMemoryPaymentRepository
	mu        sync.Mutex
	failures  int
	saveCalls int
}

func (r *conflictInjectingPaymentRepo) Save(ctx context.Context, p *payment.Payment) error {
	r.mu.Lock()
	r.saveCalls++
	if r.failures > 0 {
		r.failures--
		r.mu.Unlock()
		return tx.ErrVersionConflict
	}
	r.mu.Unlock()
	return r.inner.Save(ctx, p)
}
func (r *conflictInjectingPaymentRepo) FindByID(ctx context.Context, id shared.PaymentID) (*payment.Payment, error) {
	return r.inner.FindByID(ctx, id)
}
func (r *conflictInjectingPaymentRepo) FindByInvoiceID(ctx context.Context, id shared.InvoiceID) ([]*payment.Payment, error) {
	return r.inner.FindByInvoiceID(ctx, id)
}
func (r *conflictInjectingPaymentRepo) FindByIdempotencyKey(ctx context.Context, key string) (*payment.Payment, error) {
	return r.inner.FindByIdempotencyKey(ctx, key)
}

// TestRefund_RetriesOnVersionConflict verifies that PaymentService.Refund
// retries its local bookkeeping transaction when the payment repository reports
// an optimistic-lock conflict (issue #190). The gateway refund is deterministic
// and already succeeded, so the retry must re-read the payment and record the
// refund exactly once — surfacing no error to the caller.
func TestRefund_RetriesOnVersionConflict(t *testing.T) {
	clock := newPaymentTestClock()
	ctx := context.Background()

	inner := inmemory.NewInMemoryPaymentRepository()
	seed, err := payment.NewPayment(
		shared.NewPaymentID(),
		shared.NewInvoiceID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"gw_txn_190",
		clock.Now(),
	)
	if err != nil {
		t.Fatalf("NewPayment: %v", err)
	}
	if err := seed.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := inner.Save(ctx, seed); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	// Fail the first Save with a version conflict; the retry must succeed.
	repo := &conflictInjectingPaymentRepo{inner: inner, failures: 1}

	svc := NewPaymentService(
		&mockGateway{}, // Refund() returns success
		repo,
		&mockInvoiceRepoForPayment{},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	amount := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	if err := svc.Refund(ctx, seed.ID(), RefundInput{
		Amount: &amount,
		Reason: port.RefundReasonRequestedByCustomer,
	}); err != nil {
		t.Fatalf("Refund should succeed after a retried version conflict, got: %v", err)
	}

	if repo.saveCalls < 2 {
		t.Errorf("expected at least 2 Save attempts (conflict + retry), got %d", repo.saveCalls)
	}

	// Exactly one refund of 3000 must be booked.
	stored, err := inner.FindByID(ctx, seed.ID())
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	if stored.RefundedAmount().Amount().Cmp(big.NewRat(3000, 1)) != 0 {
		t.Errorf("expected refunded 3000 recorded once, got %s",
			stored.RefundedAmount().Amount().RatString())
	}
	if stored.Status() != payment.PaymentStatusPartiallyRefunded {
		t.Errorf("expected partially_refunded, got %s", stored.Status())
	}
}

// --- Transactional outbox writer (issue #248) ---

// TestProcessPayment_OutboxWriter_FiresOnNormalSuccess verifies that a wired
// PaymentOutboxWriter is invoked exactly once, with the persisted payment and
// invoice, on the normal gateway success path.
func TestProcessPayment_OutboxWriter_FiresOnNormalSuccess(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	writer := inmemory.NewInMemoryOutboxWriter()

	svc := NewPaymentService(
		&mockGateway{}, &mockPaymentRepo{}, invRepo, nil, &mockEventStore{},
		plugin.NewRegistry(), clock,
		WithPaymentOutboxWriter(writer),
	)

	p, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "outbox-key-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if writer.PaymentCount() != 1 {
		t.Fatalf("expected OnPaymentRecorded to fire exactly once, got %d", writer.PaymentCount())
	}
	entry := writer.PaymentEntries()[0]
	if entry.Payment == nil || entry.Payment.ID() != p.ID() {
		t.Errorf("outbox entry has wrong payment: %+v", entry.Payment)
	}
	if entry.Payment.Status() != payment.PaymentStatusCompleted {
		t.Errorf("outbox entry payment should be completed, got %s", entry.Payment.Status())
	}
	if entry.Invoice == nil || entry.Invoice.ID() != inv.ID() {
		t.Errorf("outbox entry has wrong invoice: %+v", entry.Invoice)
	}
}

// TestProcessPayment_OutboxWriter_ErrorTriggersSagaCompensation verifies the B1
// veto semantics: an OnPaymentRecorded error rolls the bookkeeping transaction
// back and, because the gateway was already charged, saga compensation reverses
// the charge (Void). ProcessPayment returns an error.
func TestProcessPayment_OutboxWriter_ErrorTriggersSagaCompensation(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &spyGateway{} // Void succeeds by default
	writer := inmemory.NewInMemoryOutboxWriter()
	writer.FailWith(errors.New("outbox insert failed"))

	svc := NewPaymentService(
		gw, &mockPaymentRepo{}, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{},
		plugin.NewRegistry(), clock,
		WithPaymentOutboxWriter(writer),
		WithoutPaymentTransactions(), // explicit noop still runs the closure
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "outbox-key-veto",
	})
	if err == nil {
		t.Fatal("expected ProcessPayment to fail when the outbox writer vetoes")
	}
	// The returned error must be tagged as an outbox veto (not a local save
	// failure) so operators can tell the two apart (issue #248 review fix 1).
	if !errors.Is(err, errPaymentOutboxVeto) {
		t.Errorf("expected error to wrap errPaymentOutboxVeto, got: %v", err)
	}
	if !gw.voidCalled {
		t.Fatal("expected saga compensation (Void) after outbox veto rolled the charge back")
	}
	if writer.PaymentCount() != 0 {
		t.Errorf("failed writer must not record an entry, got %d", writer.PaymentCount())
	}
}

// TestProcessPayment_OutboxWriter_NotCalledOnPreChargeReplay verifies that an
// idempotent replay short-circuit (a pre-existing Completed payment) does NOT
// fire the outbox writer: no new payment state is persisted.
func TestProcessPayment_OutboxWriter_NotCalledOnPreChargeReplay(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()

	existingPayment, _ := payment.NewPayment(
		shared.NewPaymentID(), inv.ID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard, "txn-existing", clock.Now(),
	)
	existingPayment.SetIdempotencyKey("outbox-key-replay")
	_ = existingPayment.Complete()

	writer := inmemory.NewInMemoryOutboxWriter()
	svc := NewPaymentService(
		&mockGateway{}, &mockPaymentRepo{existing: existingPayment},
		&mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{},
		plugin.NewRegistry(), clock,
		WithPaymentOutboxWriter(writer),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "outbox-key-replay",
	})
	if err != nil {
		t.Fatalf("idempotent replay should succeed, got: %v", err)
	}
	if writer.PaymentCount() != 0 {
		t.Errorf("outbox writer must NOT fire on idempotent replay, got %d", writer.PaymentCount())
	}
}

// TestProcessPayment_ZeroAmount_OutboxWriter_Fires verifies the outbox writer
// fires on the zero-amount settlement success path.
func TestProcessPayment_ZeroAmount_OutboxWriter_Fires(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newZeroAmountFinalizedInvoice()
	writer := inmemory.NewInMemoryOutboxWriter()

	svc := NewPaymentService(
		&mockGateway{failCharge: true}, &mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{},
		plugin.NewRegistry(), clock,
		WithPaymentOutboxWriter(writer),
	)

	p, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		Amount:         shared.Zero(shared.CurrencyJPY),
		Currency:       shared.CurrencyJPY,
		IdempotencyKey: "outbox-zero-1",
	})
	if err != nil {
		t.Fatalf("zero-amount settlement should succeed, got: %v", err)
	}
	if writer.PaymentCount() != 1 {
		t.Fatalf("expected outbox writer to fire once on zero settlement, got %d", writer.PaymentCount())
	}
	if got := writer.PaymentEntries()[0].Payment; got == nil || got.ID() != p.ID() {
		t.Errorf("zero-amount outbox entry has wrong payment: %+v", got)
	}
}

// TestProcessPayment_OutboxWriter_PanicIsolated verifies that a panicking outbox
// writer is converted into an error (not propagated as a raw panic through
// tx.Run), aborting the payment and triggering saga compensation.
func TestProcessPayment_OutboxWriter_PanicIsolated(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &spyGateway{}
	writer := inmemory.NewInMemoryOutboxWriter()
	writer.PanicNext("outbox boom")

	svc := NewPaymentService(
		gw, &mockPaymentRepo{}, &mockInvoiceRepoForPayment{inv: inv}, nil, &mockEventStore{},
		plugin.NewRegistry(), clock,
		WithPaymentOutboxWriter(writer),
		WithoutPaymentTransactions(),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "outbox-panic-1",
	})
	if err == nil {
		t.Fatal("expected a panicking outbox writer to abort ProcessPayment with an error")
	}
	if _, ok := plugin.AsPanic(err); !ok {
		t.Errorf("expected a *plugin.PluginPanicError, got: %v", err)
	}
	if !gw.voidCalled {
		t.Error("expected saga compensation after the outbox writer panicked")
	}
}

// TestProcessPayment_NoOutboxWriter_Unchanged verifies that without a wired
// writer the success path is unaffected (regression guard).
func TestProcessPayment_NoOutboxWriter_Unchanged(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()

	svc := NewPaymentService(
		&mockGateway{}, &mockPaymentRepo{}, &mockInvoiceRepoForPayment{inv: inv}, nil,
		&mockEventStore{}, plugin.NewRegistry(), clock,
	)

	p, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "outbox-none-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected completed payment, got %s", p.Status())
	}
}
