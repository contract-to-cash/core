package service

// Tests for the gateway customer-ID resolution seam (issue #231):
// ProcessPayment and ResolvePaymentMethod must send the RESOLVED gateway-side
// customer ID (not the internal AccountID) to every gateway call that carries
// one, must abort BEFORE the gateway when resolution fails, and must preserve
// the legacy identity mapping when no resolver is wired.

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// fakeCustomerIDResolver is a configurable port.CustomerIDResolver.
type fakeCustomerIDResolver struct {
	mu       sync.Mutex
	resolved string
	err      error
	calls    []shared.AccountID
}

func (r *fakeCustomerIDResolver) ResolveCustomerID(_ context.Context, accountID shared.AccountID) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, accountID)
	if r.err != nil {
		return "", r.err
	}
	return r.resolved, nil
}

// customerCapturingGateway records the CustomerID of every ChargeRequest.
type customerCapturingGateway struct {
	mockGateway
	mu                sync.Mutex
	chargeCustomerIDs []string
}

func (g *customerCapturingGateway) Charge(ctx context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
	g.mu.Lock()
	g.chargeCustomerIDs = append(g.chargeCustomerIDs, req.CustomerID)
	g.mu.Unlock()
	return g.mockGateway.Charge(ctx, req)
}

// customerIDCapturingCustomerGateway records the customerID passed to GetCustomer.
type customerIDCapturingCustomerGateway struct {
	mockCustomerGateway
	gotCustomerIDs []string
}

func (m *customerIDCapturingCustomerGateway) GetCustomer(_ context.Context, customerID string) (*port.Customer, error) {
	m.gotCustomerIDs = append(m.gotCustomerIDs, customerID)
	return m.customer, m.err
}

func TestProcessPayment_CustomerIDResolver_ResolvedIDReachesGateway(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &customerCapturingGateway{}
	resolver := &fakeCustomerIDResolver{resolved: "cus_stripe_123"}

	svc := NewPaymentService(
		gw,
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithCustomerIDResolver(resolver),
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-resolver-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pmt == nil {
		t.Fatal("expected payment")
	}

	if len(gw.chargeCustomerIDs) != 1 {
		t.Fatalf("expected 1 Charge call, got %d", len(gw.chargeCustomerIDs))
	}
	if gw.chargeCustomerIDs[0] != "cus_stripe_123" {
		t.Errorf("gateway must receive the RESOLVED customer ID, got %q", gw.chargeCustomerIDs[0])
	}
	if len(resolver.calls) != 1 || resolver.calls[0] != inv.AccountID() {
		t.Errorf("resolver must be called once with the invoice's account ID %q, got %v", inv.AccountID(), resolver.calls)
	}
}

func TestProcessPayment_CustomerIDResolver_ErrorAbortsBeforeGateway(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &customerCapturingGateway{}
	resolveErr := errors.New("no gateway customer registered for account")
	resolver := &fakeCustomerIDResolver{err: resolveErr}
	beforeSpy := &beforeChargeSpyPlugin{}
	registry := plugin.NewRegistry()
	_ = registry.Register(beforeSpy)
	paymentRepo := &mockPaymentRepo{}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		registry,
		clock,
		WithCustomerIDResolver(resolver),
	)

	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-resolver-2",
	})
	if err == nil {
		t.Fatal("expected error from failing resolver")
	}
	if !errors.Is(err, resolveErr) {
		t.Errorf("returned error must wrap the resolver error, got: %v", err)
	}
	if pmt != nil {
		t.Errorf("expected nil payment, got %+v", pmt)
	}
	// Money-safety: the gateway must never be charged on a resolution failure.
	if len(gw.chargeCustomerIDs) != 0 {
		t.Errorf("gateway must NOT be called when customer ID resolution fails, got %d Charge calls", len(gw.chargeCustomerIDs))
	}
	// Resolution runs before the BeforeCharge hooks, so plugins never observe a
	// phantom charge attempt for a call that could not reach the gateway.
	if beforeSpy.calls != 0 {
		t.Errorf("BeforeCharge must not fire when resolution fails, got %d calls", beforeSpy.calls)
	}
	if paymentRepo.saved != nil {
		t.Error("no payment record may be persisted on a resolution failure")
	}
}

func TestProcessPayment_NoResolver_AccountIDPassthrough(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &customerCapturingGateway{}

	svc := NewPaymentService(
		gw,
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		// no WithCustomerIDResolver
	)

	if _, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-resolver-3",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gw.chargeCustomerIDs) != 1 {
		t.Fatalf("expected 1 Charge call, got %d", len(gw.chargeCustomerIDs))
	}
	if gw.chargeCustomerIDs[0] != string(inv.AccountID()) {
		t.Errorf("without a resolver the legacy AccountID identity mapping must be preserved, got %q want %q",
			gw.chargeCustomerIDs[0], inv.AccountID())
	}
}

func TestProcessPayment_CustomerIDResolver_EmptyResolvedID_Aborts(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	gw := &customerCapturingGateway{}
	resolver := &fakeCustomerIDResolver{resolved: ""} // no error, but empty ID

	svc := NewPaymentService(
		gw,
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithCustomerIDResolver(resolver),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-resolver-4",
	})
	if err == nil {
		t.Fatal("expected error for empty resolved customer ID")
	}
	assertDomainError(t, err, shared.ErrCodeBusinessRule)
	if len(gw.chargeCustomerIDs) != 0 {
		t.Errorf("gateway must NOT be called for an empty resolved customer ID, got %d calls", len(gw.chargeCustomerIDs))
	}
}

func TestResolvePaymentMethod_CustomerIDResolver_UsedForCustomerLookup(t *testing.T) {
	clock := newPaymentTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	inv := newFinalizedInvoice(agg.AccountID(), agg.ContractID(), jpy(1000), nil)

	customerPM := "pm-customer-default"
	custGateway := &customerIDCapturingCustomerGateway{
		mockCustomerGateway: mockCustomerGateway{
			customer: &port.Customer{
				ID:                     "cus_resolved_77",
				DefaultPaymentMethodID: &customerPM,
			},
		},
	}
	resolver := &fakeCustomerIDResolver{resolved: "cus_resolved_77"}

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		&mockContractRepo{agg: agg},
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithCustomerGateway(custGateway),
		WithCustomerIDResolver(resolver),
	)

	resolved, err := svc.ResolvePaymentMethod(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "pm-customer-default" {
		t.Errorf("expected pm-customer-default, got %s", resolved)
	}
	if len(custGateway.gotCustomerIDs) != 1 || custGateway.gotCustomerIDs[0] != "cus_resolved_77" {
		t.Errorf("GetCustomer must receive the RESOLVED customer ID, got %v", custGateway.gotCustomerIDs)
	}
}

func TestResolvePaymentMethod_CustomerIDResolver_ErrorAbortsBeforeCustomerLookup(t *testing.T) {
	clock := newPaymentTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	inv := newFinalizedInvoice(agg.AccountID(), agg.ContractID(), jpy(1000), nil)

	custGateway := &customerIDCapturingCustomerGateway{}
	resolveErr := errors.New("mapping store unavailable")
	resolver := &fakeCustomerIDResolver{err: resolveErr}

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		&mockContractRepo{agg: agg},
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithCustomerGateway(custGateway),
		WithCustomerIDResolver(resolver),
	)

	_, err := svc.ResolvePaymentMethod(context.Background(), inv)
	if err == nil {
		t.Fatal("expected error from failing resolver")
	}
	if !errors.Is(err, resolveErr) {
		t.Errorf("returned error must wrap the resolver error, got: %v", err)
	}
	if len(custGateway.gotCustomerIDs) != 0 {
		t.Errorf("GetCustomer must NOT be called when resolution fails, got %v", custGateway.gotCustomerIDs)
	}
}
