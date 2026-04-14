package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// --- Lightweight test gateway that tracks every Charge and Refund call ---

type integrationGateway struct {
	mu           sync.Mutex
	chargeKeys   []string
	refundTxnIDs []string
	// chargeResponses, if non-empty, is consumed in order and overrides
	// the default "Captured" response for each Charge call.
	chargeResponses []port.ChargeResponse
}

func (g *integrationGateway) ID() string                                 { return "integration-test" }
func (g *integrationGateway) SupportedMethods() []port.PaymentMethodType { return nil }

func (g *integrationGateway) Charge(_ context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	idx := len(g.chargeKeys)
	g.chargeKeys = append(g.chargeKeys, req.IdempotencyKey)
	if idx < len(g.chargeResponses) {
		resp := g.chargeResponses[idx]
		return &resp, nil
	}
	return &port.ChargeResponse{
		TransactionID: "txn-" + req.IdempotencyKey,
		Status:        port.TransactionStatusCaptured,
		Amount:        req.Amount,
		CreatedAt:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}

func (g *integrationGateway) Refund(_ context.Context, req *port.RefundRequest) (*port.RefundResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refundTxnIDs = append(g.refundTxnIDs, req.TransactionID)
	return &port.RefundResponse{TransactionID: "refund-" + req.TransactionID}, nil
}

func (g *integrationGateway) Authorize(_ context.Context, _ *port.AuthorizeRequest) (*port.AuthorizeResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *integrationGateway) Capture(_ context.Context, _ *port.CaptureRequest) (*port.CaptureResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *integrationGateway) Void(_ context.Context, _ *port.VoidRequest) (*port.VoidResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *integrationGateway) Cancel(_ context.Context, _ *port.CancelRequest) (*port.CancelResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *integrationGateway) GetTransaction(_ context.Context, _ string) (*port.Transaction, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *integrationGateway) RegisterPaymentMethod(_ context.Context, _ *port.RegisterPaymentMethodRequest) (*port.PaymentMethodDetail, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *integrationGateway) DeletePaymentMethod(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}
func (g *integrationGateway) GetPaymentMethod(_ context.Context, _ string) (*port.PaymentMethodDetail, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *integrationGateway) ListPaymentMethods(_ context.Context, _ string) ([]*port.PaymentMethodDetail, error) {
	return nil, fmt.Errorf("not implemented")
}

// isolatingInvoiceRepo wraps an invoice.Repository and returns a FRESH
// (snapshot-round-tripped) invoice instance on every FindByID call. This
// simulates the per-transaction snapshot isolation a real RDBMS provides,
// preventing concurrent PaymentService callers from accidentally sharing
// the same *Invoice pointer via the in-memory repo.
//
// Without this isolation, two goroutines that both load "the same" invoice
// see a single shared pointer whose state is concurrently mutated by
// RecordPayment — which is not what happens in production (Postgres would
// hand each tx a fresh row snapshot), and it masks the #97 race behind a
// pre-existing invariant violation on the invoice.
type isolatingInvoiceRepo struct {
	inner invoice.Repository
}

func (r *isolatingInvoiceRepo) Save(ctx context.Context, inv *invoice.Invoice) error {
	return r.inner.Save(ctx, inv)
}

func (r *isolatingInvoiceRepo) FindByID(ctx context.Context, id shared.InvoiceID) (*invoice.Invoice, error) {
	inv, err := r.inner.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	snap := inv.ToSnapshot()
	clone, err := invoice.InvoiceFromSnapshot(snap)
	if err != nil {
		return nil, err
	}
	return clone, nil
}

func (r *isolatingInvoiceRepo) FindByContractID(ctx context.Context, contractID shared.ContractID) ([]*invoice.Invoice, error) {
	return r.inner.FindByContractID(ctx, contractID)
}

func (r *isolatingInvoiceRepo) FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*invoice.Invoice, error) {
	return r.inner.FindByAccountID(ctx, accountID)
}

func (r *isolatingInvoiceRepo) FindOverdue(ctx context.Context) ([]*invoice.Invoice, error) {
	return r.inner.FindOverdue(ctx)
}

func (r *isolatingInvoiceRepo) FindByStatus(ctx context.Context, status invoice.InvoiceStatus) ([]*invoice.Invoice, error) {
	return r.inner.FindByStatus(ctx, status)
}

func (r *isolatingInvoiceRepo) FindByIDAsOf(ctx context.Context, id shared.InvoiceID, asOf time.Time) (*invoice.Invoice, error) {
	return r.inner.FindByIDAsOf(ctx, id, asOf)
}

func (r *isolatingInvoiceRepo) FindByContractAndStatus(ctx context.Context, contractID shared.ContractID, status invoice.InvoiceStatus) ([]*invoice.Invoice, error) {
	return r.inner.FindByContractAndStatus(ctx, contractID, status)
}

func (r *isolatingInvoiceRepo) FindByContractAndPeriod(ctx context.Context, contractID shared.ContractID, period shared.DateRange) ([]*invoice.Invoice, error) {
	return r.inner.FindByContractAndPeriod(ctx, contractID, period)
}

func (r *isolatingInvoiceRepo) FindUnpaidByContract(ctx context.Context, contractID shared.ContractID) ([]*invoice.Invoice, error) {
	return r.inner.FindUnpaidByContract(ctx, contractID)
}

// rendezvousPaymentRepo wraps a payment.Repository and blocks every Save
// call until `expected` callers have entered Save. This deterministically
// reproduces the #97 concurrent-success race: both racing goroutines pass
// through FindByIdempotencyKey (observing nil because neither has saved
// yet) and only then are released to call the inner Save simultaneously.
// Without a unique-idempotency-key guard, the race would persist two
// payment records under the same key.
//
// After the first `expected` Save calls, subsequent Save calls pass
// through without blocking so sequential retries after the race can
// proceed. Other methods (Find*) pass through transparently.
type rendezvousPaymentRepo struct {
	inner    payment.Repository
	barrier  chan struct{}
	expected int
	mu       sync.Mutex
	arrived  int
	released bool
}

func newRendezvousPaymentRepo(inner payment.Repository, expected int) *rendezvousPaymentRepo {
	return &rendezvousPaymentRepo{
		inner:    inner,
		barrier:  make(chan struct{}),
		expected: expected,
	}
}

func (r *rendezvousPaymentRepo) wait() {
	r.mu.Lock()
	if r.released {
		r.mu.Unlock()
		return
	}
	r.arrived++
	if r.arrived >= r.expected {
		r.released = true
		close(r.barrier)
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	<-r.barrier
}

func (r *rendezvousPaymentRepo) Save(ctx context.Context, p *payment.Payment) error {
	r.wait()
	return r.inner.Save(ctx, p)
}

func (r *rendezvousPaymentRepo) FindByID(ctx context.Context, id shared.PaymentID) (*payment.Payment, error) {
	return r.inner.FindByID(ctx, id)
}

func (r *rendezvousPaymentRepo) FindByInvoiceID(ctx context.Context, invoiceID shared.InvoiceID) ([]*payment.Payment, error) {
	return r.inner.FindByInvoiceID(ctx, invoiceID)
}

func (r *rendezvousPaymentRepo) FindByIdempotencyKey(ctx context.Context, key string) (*payment.Payment, error) {
	return r.inner.FindByIdempotencyKey(ctx, key)
}

// switchableIntegrationTxManager fails the first N RunInTx calls, then
// delegates to a NoopTxManager backed by real in-memory repositories.
type switchableIntegrationTxManager struct {
	mu        sync.Mutex
	callCount int
	failUntil int
	repos     tx.Repos
}

func (m *switchableIntegrationTxManager) RunInTx(ctx context.Context, fn func(context.Context, tx.Repos) error) error {
	m.mu.Lock()
	m.callCount++
	current := m.callCount
	m.mu.Unlock()
	if current <= m.failUntil {
		return fmt.Errorf("simulated tx failure (call %d)", current)
	}
	return fn(ctx, m.repos)
}

// --- Helper to build a finalized invoice directly via the in-memory repo ---

func newIntegrationInvoice(t *testing.T, ctx context.Context, invoiceRepo *inmemory.InMemoryInvoiceRepository, amount shared.Money) *invoice.Invoice {
	t.Helper()
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(),
		shared.AccountID("acc-idem"),
		shared.NewContractID(),
		amount,
		shared.Zero(amount.Currency()),
		shared.Zero(amount.Currency()),
	)
	if err != nil {
		t.Fatalf("failed to build invoice: %v", err)
	}
	if err := inv.Finalize(); err != nil {
		t.Fatalf("failed to finalize invoice: %v", err)
	}
	if err := invoiceRepo.Save(ctx, inv); err != nil {
		t.Fatalf("failed to save invoice: %v", err)
	}
	return inv
}

// TestPaymentIdempotency_CompensationThenRetry_Integration wires together
// the real PaymentService, InMemoryPaymentRepository, InMemoryInvoiceRepository
// and InMemoryIdempotencyStore and reproduces the Issue #87 race end-to-end.
//
// The first ProcessPayment call charges the gateway successfully, but the
// wrapped TxManager rejects the local save on the first call, triggering the
// saga compensation Refund and recording a compensation marker. The second
// call with the same original key must resolve to the stored effective key,
// issue a fresh Charge at the gateway, and succeed — without double-recording
// the payment or leaving the invoice linked to the refunded transaction.
func TestPaymentIdempotency_CompensationThenRetry_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	// Real in-memory repositories.
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()
	store := inmemory.NewInMemoryIdempotencyStore()

	amount := moneyJPY(10000)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	gw := &integrationGateway{}
	txm := &switchableIntegrationTxManager{
		failUntil: 1, // only the first call fails
		repos:     tx.Repos{Payments: paymentRepo, Invoices: invoiceRepo},
	}

	svc := service.NewPaymentService(
		gw,
		paymentRepo,
		invoiceRepo,
		nil, // contract repo not needed for this flow
		nil, // event store not exercised
		plugin.NewRegistry(),
		clock,
		service.WithPaymentTxManager(txm),
		service.WithIdempotencyStore(store),
	)

	input := service.ProcessPaymentInput{
		PaymentMethodID: "pm-integration",
		Amount:          amount,
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "integration-key-87",
	}

	// First call: must fail with the tx error after compensation fired.
	_, firstErr := svc.ProcessPayment(ctx, inv.ID(), input)
	if firstErr == nil {
		t.Fatal("expected first call to fail via tx manager")
	}
	if len(gw.refundTxnIDs) != 1 {
		t.Fatalf("expected 1 compensation refund, got %d: %v", len(gw.refundTxnIDs), gw.refundTxnIDs)
	}
	refundedTxnID := gw.refundTxnIDs[0]

	// Verify the store recorded the compensation marker.
	_, ok, err := store.ResolveEffectiveKey(ctx, "integration-key-87")
	if err != nil {
		t.Fatalf("ResolveEffectiveKey error: %v", err)
	}
	if !ok {
		t.Fatal("expected integration-key-87 to be marked as compensated")
	}

	// Second call: MUST succeed and MUST NOT record the refunded transaction.
	pmt, err := svc.ProcessPayment(ctx, inv.ID(), input)
	if err != nil {
		t.Fatalf("second call must succeed: %v", err)
	}
	if pmt == nil {
		t.Fatal("expected payment from retry")
	}
	if pmt.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected completed status, got %q", pmt.Status())
	}
	if pmt.GatewayTransactionID() == refundedTxnID {
		t.Fatalf("payment linked to the refunded transaction %q — Issue #87 regression", refundedTxnID)
	}

	// The gateway must have seen two distinct Charge keys.
	if len(gw.chargeKeys) != 2 {
		t.Fatalf("expected 2 Charge calls, got %d: %v", len(gw.chargeKeys), gw.chargeKeys)
	}
	if gw.chargeKeys[0] != "integration-key-87" {
		t.Errorf("first Charge key should be the original, got %q", gw.chargeKeys[0])
	}
	if gw.chargeKeys[1] == "integration-key-87" {
		t.Error("second Charge must use a fresh effective key")
	}

	// Exactly one payment must be persisted in the real repository for this invoice.
	persisted, err := paymentRepo.FindByInvoiceID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByInvoiceID error: %v", err)
	}
	completed := 0
	for _, p := range persisted {
		if p.Status() == payment.PaymentStatusCompleted {
			completed++
		}
	}
	if completed != 1 {
		t.Errorf("expected exactly 1 completed payment in real repo, got %d", completed)
	}

	// The real invoice in the repository must be recorded as paid exactly
	// once at the full amount.
	refreshed, err := invoiceRepo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID error: %v", err)
	}
	if refreshed.PaidAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("expected invoice paid amount %v, got %v", amount.Amount(), refreshed.PaidAmount().Amount())
	}
}

// TestPaymentIdempotency_3DSThenCaptured_Integration verifies the
// BLOCKER-1 fix (Pending → Completed upgrade) end-to-end against the real
// InMemoryPaymentRepository and InMemoryInvoiceRepository. If the upgrade
// path regresses — or if the real repos' RecordPayment/Save semantics
// diverge from the mocks used in the unit test — this integration test is
// the canary.
//
// Scenario (one service, one invoice, one payment repo):
//  1. First call: gateway returns `requires_action` → pending payment
//     persisted under the input key, invoice unchanged.
//  2. Second call with the same key: gateway returns `captured` (user
//     finished 3DS on the hosted page and the gateway auto-captured).
//  3. Expected: the existing Pending payment is upgraded to Completed AND
//     the invoice is recorded as paid at the full amount. No duplicate
//     payment is created.
func TestPaymentIdempotency_3DSThenCaptured_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()

	amount := moneyJPY(10000)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	gw := &integrationGateway{
		chargeResponses: []port.ChargeResponse{
			{
				TransactionID: "txn-3ds-K",
				Status:        port.TransactionStatusRequiresAction,
				Amount:        amount,
				CreatedAt:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			},
			{
				TransactionID: "txn-3ds-K",
				Status:        port.TransactionStatusCaptured,
				Amount:        amount,
				CreatedAt:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	svc := service.NewPaymentService(
		gw,
		paymentRepo,
		invoiceRepo,
		nil,
		nil,
		plugin.NewRegistry(),
		clock,
	)

	input := service.ProcessPaymentInput{
		PaymentMethodID: "pm-3ds",
		Amount:          amount,
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "integration-3ds-K",
	}

	// Call 1: expect ErrRequiresAction and a saved pending payment. The
	// real invoice should not be paid yet.
	_, err1 := svc.ProcessPayment(ctx, inv.ID(), input)
	if !errors.Is(err1, service.ErrRequiresAction) {
		t.Fatalf("first call must return ErrRequiresAction, got: %v", err1)
	}
	refreshedAfter1, err := invoiceRepo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID after call 1: %v", err)
	}
	if refreshedAfter1.PaidAmount().Amount().Sign() != 0 {
		t.Errorf("invoice must not be paid after call 1, got %v", refreshedAfter1.PaidAmount().Amount())
	}

	// Mid-flight invariant: exactly ONE Pending payment persisted between
	// the two calls. Without this check, a regression that dropped the
	// pending save on call 1 (or accidentally duplicated it) could pass
	// the "1 completed at the end" check but leak records in between.
	pendingMid, err := paymentRepo.FindByInvoiceID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByInvoiceID after call 1: %v", err)
	}
	if len(pendingMid) != 1 {
		t.Fatalf("expected exactly 1 pending payment after call 1, got %d", len(pendingMid))
	}
	if pendingMid[0].Status() != payment.PaymentStatusPending {
		t.Errorf("expected Pending status after call 1, got %q", pendingMid[0].Status())
	}
	pendingID := pendingMid[0].ID()

	// Call 2: gateway returns Captured. The existing pending record must be
	// upgraded to Completed in the real payment repo, and the real invoice
	// repo must now show the payment recorded at the full amount.
	pmt, err2 := svc.ProcessPayment(ctx, inv.ID(), input)
	if err2 != nil {
		t.Fatalf("second call must succeed, got: %v", err2)
	}
	if pmt == nil || pmt.Status() != payment.PaymentStatusCompleted {
		t.Fatalf("retry payment must be Completed, got %+v", pmt)
	}

	// Verify against the REAL invoice repo (not in-memory test copy).
	refreshedAfter2, err := invoiceRepo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID after call 2: %v", err)
	}
	if refreshedAfter2.PaidAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("invoice must be recorded as paid at %v, got %v", amount.Amount(), refreshedAfter2.PaidAmount().Amount())
	}

	// Exactly one payment in the real repo (no duplicate from the retry).
	persisted, _ := paymentRepo.FindByInvoiceID(ctx, inv.ID())
	if len(persisted) != 1 {
		t.Errorf("expected exactly 1 persisted payment, got %d", len(persisted))
	}
	if len(persisted) > 0 && persisted[0].Status() != payment.PaymentStatusCompleted {
		t.Errorf("persisted payment must be Completed, got %q", persisted[0].Status())
	}
	// The persisted payment must be the SAME record upgraded from
	// Pending — no duplicate created.
	if len(persisted) > 0 && persisted[0].ID() != pendingID {
		t.Errorf("upgrade must reuse the pending payment ID %q, got %q", pendingID, persisted[0].ID())
	}
}

// TestPaymentIdempotency_Concurrent_CompensationRace_Integration verifies
// that two concurrent ProcessPayment calls with the same idempotency key do
// not corrupt state. Both goroutines fail at the local tx layer, both fire
// saga compensation, both attempt MarkCompensated. Safety relies on:
//
//  1. The gateway's idempotency replay returning the SAME transaction id to
//     both goroutines (mocked via integrationGateway's deterministic txn
//     naming).
//  2. The compensation refund's deterministic key (`comp-refund-<txnID>`)
//     letting the gateway dedupe the second refund automatically.
//  3. InMemoryIdempotencyStore.MarkCompensated being first-call-wins, so the
//     store converges on exactly one effective key regardless of which
//     goroutine's marker write landed first.
//
// After both concurrent failures, a SEQUENTIAL retry with the same input
// key must resolve to the stored effective key, issue a fresh charge, and
// succeed exactly once.
func TestPaymentIdempotency_Concurrent_CompensationRace_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()
	store := inmemory.NewInMemoryIdempotencyStore()

	amount := moneyJPY(10000)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	gw := &integrationGateway{}
	// Both concurrent tx calls fail; the third (sequential retry) succeeds.
	txm := &switchableIntegrationTxManager{
		failUntil: 2,
		repos:     tx.Repos{Payments: paymentRepo, Invoices: invoiceRepo},
	}

	svc := service.NewPaymentService(
		gw,
		paymentRepo,
		invoiceRepo,
		nil,
		nil,
		plugin.NewRegistry(),
		clock,
		service.WithPaymentTxManager(txm),
		service.WithIdempotencyStore(store),
	)

	input := service.ProcessPaymentInput{
		PaymentMethodID: "pm-concurrent",
		Amount:          amount,
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "integration-key-concurrent",
	}

	// Fire two ProcessPayment calls concurrently. Both should fail after
	// compensation, without panicking or corrupting shared state.
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = svc.ProcessPayment(ctx, inv.ID(), input)
		}(i)
	}
	wg.Wait()

	if errs[0] == nil || errs[1] == nil {
		t.Fatalf("both concurrent calls must fail; got %v / %v", errs[0], errs[1])
	}

	// The store must have exactly one mapping for the original key.
	eff, ok, err := store.ResolveEffectiveKey(ctx, "integration-key-concurrent")
	if err != nil {
		t.Fatalf("ResolveEffectiveKey error: %v", err)
	}
	if !ok {
		t.Fatal("expected compensation marker after concurrent failures")
	}
	if eff == "" || eff == "integration-key-concurrent" {
		t.Errorf("effective key must be derived, got %q", eff)
	}

	// No completed payments should exist yet in the real repo.
	existing, _ := paymentRepo.FindByInvoiceID(ctx, inv.ID())
	for _, p := range existing {
		if p.Status() == payment.PaymentStatusCompleted {
			t.Errorf("no payment should be Completed after concurrent failures, found %s", p.ID())
		}
	}

	// Sequential retry: must resolve to the stored effective key and succeed.
	pmt, err := svc.ProcessPayment(ctx, inv.ID(), input)
	if err != nil {
		t.Fatalf("sequential retry must succeed: %v", err)
	}
	if pmt == nil || pmt.Status() != payment.PaymentStatusCompleted {
		t.Errorf("retry must produce a completed payment, got %+v", pmt)
	}

	// Exactly one completed payment persisted for this invoice.
	all, _ := paymentRepo.FindByInvoiceID(ctx, inv.ID())
	completed := 0
	for _, p := range all {
		if p.Status() == payment.PaymentStatusCompleted {
			completed++
		}
	}
	if completed != 1 {
		t.Errorf("expected exactly 1 completed payment, got %d", completed)
	}

	// The invoice must record the payment exactly once at the full amount.
	refreshed, _ := invoiceRepo.FindByID(ctx, inv.ID())
	if refreshed.PaidAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("expected invoice paid amount %v, got %v", amount.Amount(), refreshed.PaidAmount().Amount())
	}
}

// TestPaymentIdempotency_ConcurrentSuccess_Race_Integration reproduces the
// race described in issue #97: two ProcessPayment goroutines fire with the
// same IdempotencyKey, both reach the SUCCESS path (tx commits for both
// without any simulated failure), and the PaymentRepository must reject the
// duplicate so that only ONE completed payment is persisted and the invoice
// is recorded as paid exactly once.
//
// Without a unique-key constraint in the repository, the interleaving
//
//	G1: FindByIdempotencyKey(K) → nil
//	G2: FindByIdempotencyKey(K) → nil  (G1 hasn't saved yet)
//	G1: Save(p1) → OK
//	G2: Save(p2) → OK   ← duplicate, #97 regression
//
// is possible because RWMutex is released between find and save.
//
// The InMemoryPaymentRepository simulates a Postgres UNIQUE INDEX on
// idempotency_key. The race-losing goroutine's Save returns a
// [shared.ErrCodeDuplicateRequest] DomainError, which PaymentService
// translates into an idempotent-replay (re-reads the winner's record via
// FindByIdempotencyKey and returns it). Saga compensation MUST NOT fire
// in this path because the shared gateway transaction backs the winner's
// legitimate payment and refunding it would undo a successful charge.
func TestPaymentIdempotency_ConcurrentSuccess_Race_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()

	amount := moneyJPY(10000)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	gw := &integrationGateway{}

	// isolatingInvoiceRepo mirrors a real RDBMS's per-tx snapshot
	// isolation: each FindByID returns a fresh clone so the two
	// goroutines do not accidentally share a single *Invoice pointer
	// whose paidAmount would be mutated by the first RecordPayment call.
	// Without this, the in-memory repo's pointer sharing masks the #97
	// payment-record race behind a spurious invoice invariant error.
	isolatedInvoiceRepo := &isolatingInvoiceRepo{inner: invoiceRepo}

	// rendezvousPaymentRepo blocks every Save until both goroutines
	// have reached it simultaneously. This deterministically reproduces
	// the dangerous interleaving described in issue #97: both goroutines
	// observe "no existing payment" via FindByIdempotencyKey (because
	// neither has saved yet), then both attempt to persist a fresh
	// payment record under the same key.
	racedPaymentRepo := newRendezvousPaymentRepo(paymentRepo, 2)

	svc := service.NewPaymentService(
		gw,
		racedPaymentRepo,
		isolatedInvoiceRepo,
		nil,
		nil,
		plugin.NewRegistry(),
		clock,
		// NoopTxManager: closure runs without DB-level serialization.
		// This mirrors the default configuration a consumer would see if
		// they forget to wire a real TxManager with SELECT FOR UPDATE /
		// UNIQUE INDEX semantics.
	)

	input := service.ProcessPaymentInput{
		PaymentMethodID: "pm-concurrent-success",
		Amount:          amount,
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "integration-concurrent-success",
	}

	var wg sync.WaitGroup
	wg.Add(2)
	results := make([]*payment.Payment, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = svc.ProcessPayment(ctx, inv.ID(), input)
		}(i)
	}
	wg.Wait()

	// Both calls must return the same completed payment. The race loser
	// is re-routed to the winner via the duplicate-key fallback path.
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: unexpected error: %v", i, err)
		}
		if results[i] == nil {
			t.Fatalf("goroutine %d: nil payment", i)
		}
		if results[i].Status() != payment.PaymentStatusCompleted {
			t.Errorf("goroutine %d: expected Completed, got %q", i, results[i].Status())
		}
	}
	if results[0].ID() != results[1].ID() {
		t.Errorf("both goroutines must converge on the same payment record; got %q and %q",
			results[0].ID(), results[1].ID())
	}

	// Exactly one completed payment must be persisted.
	persisted, err := paymentRepo.FindByInvoiceID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByInvoiceID: %v", err)
	}
	completed := 0
	for _, p := range persisted {
		if p.Status() == payment.PaymentStatusCompleted {
			completed++
		}
	}
	if completed != 1 {
		t.Errorf("expected exactly 1 completed payment, got %d (total persisted=%d)",
			completed, len(persisted))
	}

	// Invoice must be recorded as paid exactly once at the full amount
	// (no double-recording from the race loser's RecordPayment call).
	refreshed, err := invoiceRepo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if refreshed.PaidAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("expected invoice paid amount %v, got %v",
			amount.Amount(), refreshed.PaidAmount().Amount())
	}

	// The loser's gateway charge is deduplicated by the gateway itself
	// (idempotency-key replay returns the same txn), so no refund is
	// required — saga compensation MUST NOT have fired.
	if len(gw.refundTxnIDs) != 0 {
		t.Errorf("saga compensation must not fire on concurrent success race, got refunds: %v",
			gw.refundTxnIDs)
	}
}

// TestPaymentIdempotency_NoStore_LegacyPath_Integration verifies that when
// no IdempotencyStore is wired, ProcessPayment behaves exactly as before the
// fix: one Charge call, no marker interactions, input key preserved.
func TestPaymentIdempotency_NoStore_LegacyPath_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()

	amount := moneyJPY(5000)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	gw := &integrationGateway{}

	svc := service.NewPaymentService(
		gw,
		paymentRepo,
		invoiceRepo,
		nil,
		nil,
		plugin.NewRegistry(),
		clock,
		// No WithIdempotencyStore → legacy path.
	)

	_, err := svc.ProcessPayment(ctx, inv.ID(), service.ProcessPaymentInput{
		PaymentMethodID: "pm-legacy",
		Amount:          amount,
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "legacy-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gw.chargeKeys) != 1 || gw.chargeKeys[0] != "legacy-key" {
		t.Errorf("expected single Charge with the original key, got %v", gw.chargeKeys)
	}
	if len(gw.refundTxnIDs) != 0 {
		t.Errorf("expected no refunds in the happy path, got %v", gw.refundTxnIDs)
	}
}
