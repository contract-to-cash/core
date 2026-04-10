package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// --- Mock gateway ---

type mockGateway struct {
	failCharge      bool
	requiresAction  bool
	threeDSRedirect string
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
	return &port.ChargeResponse{
		TransactionID: "txn-" + req.IdempotencyKey,
		Status:        port.TransactionStatusCaptured,
		Amount:        req.Amount,
		CreatedAt:     time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
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
	refundCalled bool
	refundReq    *port.RefundRequest
	refundErr    error
}

func (g *spyGateway) Void(_ context.Context, req *port.VoidRequest) (*port.VoidResponse, error) {
	g.voidCalled = true
	g.voidReq = req
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

// --- Saga compensation tests (Issue #82) ---

func TestProcessPayment_SagaCompensation_CallsRefundNotVoid(t *testing.T) {
	// When Charge succeeds but local save fails, the saga compensation
	// must call Refund (not Void) because Charge is authorize+capture.
	// Void only works on pre-capture authorizations.
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
		IdempotencyKey:  "key-comp",
	})

	// Should fail because local save failed
	if err == nil {
		t.Fatal("expected error from local save failure")
	}

	// Compensation should have called Refund, NOT Void
	if gw.voidCalled {
		t.Error("Void should NOT be called for saga compensation after Charge (captured transaction)")
	}
	if !gw.refundCalled {
		t.Fatal("Refund should be called as saga compensation after Charge fails to save locally")
	}
}

func TestProcessPayment_SagaCompensation_RefundUsesCorrectTransactionID(t *testing.T) {
	// Verify the refund compensation uses the correct gateway transaction ID.
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

	_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-txnid",
	})

	if !gw.refundCalled {
		t.Fatal("Refund should be called as compensation")
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
	// When both local save and compensation refund fail,
	// the error should contain both failures for manual reconciliation.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &spyGateway{refundErr: fmt.Errorf("gateway timeout")}

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

	existingPayment := payment.NewPayment(
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
