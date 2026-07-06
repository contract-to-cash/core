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

// Note (issue #152): the in-memory invoice repository now returns an isolated
// snapshot copy from every read on its own, mirroring the per-transaction
// snapshot isolation a real RDBMS provides. The tests below therefore use the
// raw *InMemoryInvoiceRepository directly; the previous isolatingInvoiceRepo
// wrapper that hand-rolled this isolation has been removed as redundant.

// recordingAfterChargePlugin implements plugin.AfterChargeHook and
// captures each invocation's invoice snapshot (status + paidAmount) so
// tests can assert that the hook observes the post-commit invoice
// state — critical for the #97 race-loser path where the loser's
// local invoice clone was mutated but never persisted.
type recordingAfterChargePlugin struct {
	mu          sync.Mutex
	invocations []recordedAfterChargeInvocation
}

type recordedAfterChargeInvocation struct {
	PaymentID     shared.PaymentID
	InvoiceStatus invoice.InvoiceStatus
	InvoicePaid   shared.Money
}

func (p *recordingAfterChargePlugin) Name() string    { return "recording-after-charge" }
func (p *recordingAfterChargePlugin) Version() string { return "1.0.0" }
func (p *recordingAfterChargePlugin) Priority() int   { return 500 }
func (p *recordingAfterChargePlugin) Initialize(_ context.Context, _ plugin.Config) error {
	return nil
}
func (p *recordingAfterChargePlugin) Shutdown(_ context.Context) error { return nil }

func (p *recordingAfterChargePlugin) AfterCharge(pctx *plugin.PaymentContext) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	inv := pctx.Invoice()
	pm := pctx.Payment()
	inv2 := recordedAfterChargeInvocation{}
	if pm != nil {
		inv2.PaymentID = pm.ID()
	}
	if inv != nil {
		inv2.InvoiceStatus = inv.Status()
		inv2.InvoicePaid = inv.PaidAmount()
	}
	p.invocations = append(p.invocations, inv2)
	return nil
}

func (p *recordingAfterChargePlugin) snapshot() []recordedAfterChargeInvocation {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedAfterChargeInvocation, len(p.invocations))
	copy(out, p.invocations)
	return out
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
// idempotency_key. The race-losing goroutine's Save returns an error
// matching errors.Is against [payment.ErrDuplicateIdempotencyKey]
// (concretely a [*payment.DuplicateIdempotencyKeyError]), which
// PaymentService translates into an idempotent-replay (re-reads the
// winner's record via FindByIdempotencyKey and returns it). Saga
// compensation MUST NOT fire in this path because the shared gateway
// transaction backs the winner's legitimate payment and refunding it
// would undo a successful charge.
//
// The test also installs a recording AfterCharge hook to verify the
// post-convergence invoice re-fetch (see PaymentService.ProcessPayment
// phase 4): the hook MUST see the invoice in Paid status with the
// full amount applied, even for the race-loser goroutine whose local
// invoice clone was mutated but never persisted.
func TestPaymentIdempotency_ConcurrentSuccess_Race_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()

	amount := moneyJPY(10000)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	gw := &integrationGateway{}

	// The in-memory invoice repository isolates every read (issue #152),
	// mirroring a real RDBMS's per-tx snapshot isolation: each FindByID
	// returns a fresh clone so the two goroutines do not accidentally share a
	// single *Invoice pointer whose paidAmount would be mutated by the first
	// RecordPayment call. Without that isolation the pointer sharing would mask
	// the #97 payment-record race behind a spurious invoice invariant error.
	isolatedInvoiceRepo := invoiceRepo

	// rendezvousPaymentRepo blocks every Save until both goroutines
	// have reached it simultaneously. This deterministically reproduces
	// the dangerous interleaving described in issue #97: both goroutines
	// observe "no existing payment" via FindByIdempotencyKey (because
	// neither has saved yet), then both attempt to persist a fresh
	// payment record under the same key.
	racedPaymentRepo := newRendezvousPaymentRepo(paymentRepo, 2)

	// Recording AfterCharge hook verifies that the post-convergence
	// invoice re-fetch kicked in: the loser goroutine must see the
	// invoice in Paid status with the full amount, not the stale
	// loser-side clone whose RecordPayment mutation was discarded.
	recorder := &recordingAfterChargePlugin{}
	reg := plugin.NewRegistry()
	if regErr := reg.Register(recorder); regErr != nil {
		t.Fatalf("registry.Register: %v", regErr)
	}

	svc := service.NewPaymentService(
		gw,
		racedPaymentRepo,
		isolatedInvoiceRepo,
		nil,
		nil,
		reg,
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

	// AfterCharge hook MUST have fired for BOTH goroutines, and BOTH
	// invocations MUST have observed the invoice in Paid status at the
	// full amount — i.e. the post-convergence invoice re-fetch must
	// have replaced the race-loser's stale local clone. Without the
	// re-fetch, one invocation would see the loser's mutated-but-
	// unsaved state (paidAmount == amount but paidAt / status possibly
	// inconsistent with the winner's commit, or — if the loser's
	// clone was discarded at a different point — not Paid at all).
	invocations := recorder.snapshot()
	if len(invocations) != 2 {
		t.Fatalf("expected AfterCharge to fire twice, got %d", len(invocations))
	}
	for i, rec := range invocations {
		if rec.InvoiceStatus != invoice.InvoiceStatusPaid {
			t.Errorf("invocation %d: expected invoice status Paid, got %q", i, rec.InvoiceStatus)
		}
		if rec.InvoicePaid.Amount().Cmp(amount.Amount()) != 0 {
			t.Errorf("invocation %d: expected invoice paid=%v, got %v",
				i, amount.Amount(), rec.InvoicePaid.Amount())
		}
		if rec.PaymentID != results[0].ID() {
			t.Errorf("invocation %d: expected PaymentID %s (winner), got %s",
				i, results[0].ID(), rec.PaymentID)
		}
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

// --- Postgres in_failed_sql_transaction (25P02) regression test ---
//
// abortedTxKey is a context-value key used by abortedTxRepoWrapper to
// distinguish "inside an in-flight tx" from "fresh ctx after rollback."
// abortedTxSimulatingTxManager attaches a fresh marker per RunInTx call.
type abortedTxKey struct{}

type abortedTxState struct {
	mu     sync.Mutex
	failed bool
}

func (s *abortedTxState) markFailed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = true
}

func (s *abortedTxState) isFailed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

func abortedStateFromCtx(ctx context.Context) *abortedTxState {
	s, _ := ctx.Value(abortedTxKey{}).(*abortedTxState)
	return s
}

// errAbortedTx mirrors Postgres's behaviour after a unique-violation aborts
// the surrounding tx (SQLSTATE 25P02). Once a Save on a tx-scoped ctx
// returns ErrDuplicateIdempotencyKey, every subsequent call on that ctx —
// including FindByIdempotencyKey — fails with errAbortedTx until the tx
// ends. Calls on a fresh outer ctx remain healthy.
var errAbortedTx = errors.New("simulated 25P02: in_failed_sql_transaction")

// abortedTxRepoWrapper wraps a payment.Repository to simulate Postgres's
// aborted-tx state. The PR #117 review BLOCKER was: the original code
// re-read the winner via FindByIdempotencyKey on the SAME txCtx after a
// duplicate-key Save, which Postgres would reject with 25P02 and route
// the loser through saga.Compensate (refunding the winner's charge).
// The fix exits the closure on a sentinel and re-reads on the outer ctx
// instead. This wrapper exercises the regression deterministically.
type abortedTxRepoWrapper struct {
	inner payment.Repository
}

func (r *abortedTxRepoWrapper) failedOrInner(ctx context.Context, errPrefix string) error {
	if state := abortedStateFromCtx(ctx); state != nil && state.isFailed() {
		return fmt.Errorf("%s: %w", errPrefix, errAbortedTx)
	}
	return nil
}

func (r *abortedTxRepoWrapper) Save(ctx context.Context, p *payment.Payment) error {
	if err := r.failedOrInner(ctx, "save"); err != nil {
		return err
	}
	err := r.inner.Save(ctx, p)
	if err != nil && errors.Is(err, payment.ErrDuplicateIdempotencyKey) {
		if state := abortedStateFromCtx(ctx); state != nil {
			state.markFailed()
		}
	}
	return err
}

func (r *abortedTxRepoWrapper) FindByID(ctx context.Context, id shared.PaymentID) (*payment.Payment, error) {
	if err := r.failedOrInner(ctx, "find by id"); err != nil {
		return nil, err
	}
	return r.inner.FindByID(ctx, id)
}

func (r *abortedTxRepoWrapper) FindByInvoiceID(ctx context.Context, invoiceID shared.InvoiceID) ([]*payment.Payment, error) {
	if err := r.failedOrInner(ctx, "find by invoice id"); err != nil {
		return nil, err
	}
	return r.inner.FindByInvoiceID(ctx, invoiceID)
}

func (r *abortedTxRepoWrapper) FindByIdempotencyKey(ctx context.Context, key string) (*payment.Payment, error) {
	if err := r.failedOrInner(ctx, "find by idempotency key"); err != nil {
		return nil, err
	}
	return r.inner.FindByIdempotencyKey(ctx, key)
}

// abortedTxSimulatingTxManager decorates each txCtx with a fresh
// abortedTxState so the wrapped repo can distinguish in-tx calls from
// outer-ctx calls. RunInTx returns the closure's error verbatim; it does
// not roll back any abortedTxState — by construction each invocation
// gets a brand-new state, so once RunInTx returns, the caller's outer
// ctx no longer carries the marker and is treated as a fresh tx.
type abortedTxSimulatingTxManager struct {
	repos tx.Repos
}

func (m *abortedTxSimulatingTxManager) RunInTx(ctx context.Context, fn func(context.Context, tx.Repos) error) error {
	state := &abortedTxState{}
	txCtx := context.WithValue(ctx, abortedTxKey{}, state)
	return fn(txCtx, m.repos)
}

// TestPaymentIdempotency_ConcurrentSuccess_AbortedTxSimulation_Integration
// reproduces the Postgres in_failed_sql_transaction regression vector
// surfaced in the PR #117 review:
//
//   - Two goroutines reach the in-tx Save with the same effective key.
//   - The race-losing Save returns ErrDuplicateIdempotencyKey.
//   - On Postgres, the unique-violation puts the surrounding tx into
//     in_failed_sql_transaction (SQLSTATE 25P02). Any subsequent query
//     on the same connection — including FindByIdempotencyKey — fails.
//   - The original implementation called FindByIdempotencyKey on txCtx
//     immediately after the failed Save, so on Postgres the loser's
//     find would error, the closure would return the error, and
//     saga.Compensate would refund the winner's legitimate charge.
//
// With the fix, the closure exits on errDuplicateKeyRaceSignal as soon as
// the duplicate-key Save lands, RunInTx unwinds the failed tx, and the
// outer code re-reads the winner via the outer ctx where the wrapper's
// failed-state marker is absent. The race-loser converges, no refund is
// issued, and the invoice is paid exactly once.
func TestPaymentIdempotency_ConcurrentSuccess_AbortedTxSimulation_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()

	amount := moneyJPY(7777)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	gw := &integrationGateway{}

	// The in-memory invoice repository isolates reads natively (issue #152).
	isolatedInvoiceRepo := invoiceRepo
	racedPaymentRepo := newRendezvousPaymentRepo(paymentRepo, 2)
	abortedRepo := &abortedTxRepoWrapper{inner: racedPaymentRepo}

	// The custom TxManager decorates txCtx so that an in-tx Save returning
	// ErrDuplicateIdempotencyKey turns subsequent in-tx calls into
	// errAbortedTx. The outer ctx (used by the post-RunInTx fresh-tx
	// FindByIdempotencyKey) is never marked.
	txm := &abortedTxSimulatingTxManager{
		repos: tx.Repos{Payments: abortedRepo, Invoices: isolatedInvoiceRepo},
	}

	recorder := &recordingAfterChargePlugin{}
	reg := plugin.NewRegistry()
	if regErr := reg.Register(recorder); regErr != nil {
		t.Fatalf("registry.Register: %v", regErr)
	}

	svc := service.NewPaymentService(
		gw,
		abortedRepo,
		isolatedInvoiceRepo,
		nil,
		nil,
		reg,
		clock,
		service.WithPaymentTxManager(txm),
	)

	input := service.ProcessPaymentInput{
		PaymentMethodID: "pm-aborted-tx-sim",
		Amount:          amount,
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "integration-aborted-tx-sim",
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

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: unexpected error under aborted-tx simulation: %v", i, err)
		}
		if results[i] == nil {
			t.Fatalf("goroutine %d: nil payment", i)
		}
		if results[i].Status() != payment.PaymentStatusCompleted {
			t.Errorf("goroutine %d: expected Completed, got %q", i, results[i].Status())
		}
	}
	if results[0].ID() != results[1].ID() {
		t.Errorf("both goroutines must converge on the same payment record (aborted-tx simulation); got %q and %q",
			results[0].ID(), results[1].ID())
	}

	if len(gw.refundTxnIDs) != 0 {
		t.Fatalf("saga compensation must not fire under aborted-tx simulation; got refunds: %v",
			gw.refundTxnIDs)
	}

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
		t.Errorf("expected exactly 1 completed payment under aborted-tx simulation, got %d (total=%d)",
			completed, len(persisted))
	}

	refreshed, err := invoiceRepo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if refreshed.PaidAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("expected invoice paid amount %v, got %v",
			amount.Amount(), refreshed.PaidAmount().Amount())
	}
}

// TestPaymentIdempotency_PendingThenCompleted_3DSUpgrade_Integration
// covers the 3DS Pending → Completed upgrade path that the PR #117
// review flagged as untested. The first ProcessPayment call hits the
// requires_action branch and saves a Pending payment under the
// effective key. The second call with the SAME key sees the gateway
// return Captured (the customer finished 3DS), enters the in-tx
// branch, finds the existing Pending record via the in-tx
// FindByIdempotencyKey, upgrades it to Completed, and records the
// payment on the invoice.
//
// The two calls must converge on a single PaymentID (the Pending
// record's), exactly one row must be persisted, and no
// duplicate-key path or saga compensation must fire.
func TestPaymentIdempotency_PendingThenCompleted_3DSUpgrade_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()

	amount := moneyJPY(4321)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	// First Charge → requires_action, second Charge → captured (3DS done).
	// Both Charge calls share the same gateway-side transaction id, mirroring
	// real Stripe/Adyen idempotency-key replay semantics.
	gw := &integrationGateway{
		chargeResponses: []port.ChargeResponse{
			{
				TransactionID: "txn-3ds-upgrade",
				Status:        port.TransactionStatusRequiresAction,
				Amount:        amount,
				CreatedAt:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			},
			{
				TransactionID: "txn-3ds-upgrade",
				Status:        port.TransactionStatusCaptured,
				Amount:        amount,
				CreatedAt:     time.Date(2026, 3, 1, 0, 0, 1, 0, time.UTC),
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
		IdempotencyKey:  "integration-3ds-upgrade",
	}

	// Call 1: requires_action → Pending record persisted, ErrRequiresAction
	// is returned alongside the payment.
	pending, err := svc.ProcessPayment(ctx, inv.ID(), input)
	if !errors.Is(err, service.ErrRequiresAction) {
		t.Fatalf("expected ErrRequiresAction on first call, got: %v", err)
	}
	if pending == nil {
		t.Fatal("expected pending payment record from first call")
	}
	if pending.Status() != payment.PaymentStatusPending {
		t.Fatalf("expected Pending, got %q", pending.Status())
	}

	// Call 2: gateway now returns Captured. The in-tx FindByIdempotencyKey
	// must find the Pending record from Call 1 and upgrade it to Completed.
	completed, err := svc.ProcessPayment(ctx, inv.ID(), input)
	if err != nil {
		t.Fatalf("expected second call to succeed, got: %v", err)
	}
	if completed == nil {
		t.Fatal("expected completed payment from second call")
	}
	if completed.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected Completed, got %q", completed.Status())
	}
	if completed.ID() != pending.ID() {
		t.Errorf("upgrade path must converge on the Pending record's ID; got pending=%s, completed=%s",
			pending.ID(), completed.ID())
	}

	// Exactly one persisted record, in Completed state.
	persisted, err := paymentRepo.FindByInvoiceID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByInvoiceID: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("expected exactly 1 persisted payment, got %d", len(persisted))
	}
	if persisted[0].Status() != payment.PaymentStatusCompleted {
		t.Errorf("persisted payment must be Completed, got %q", persisted[0].Status())
	}

	// No saga compensation — the second call upgraded the existing record
	// rather than creating a duplicate.
	if len(gw.refundTxnIDs) != 0 {
		t.Errorf("3DS upgrade must not fire saga compensation; got refunds: %v", gw.refundTxnIDs)
	}

	// Invoice reflects the Captured payment.
	refreshed, err := invoiceRepo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if refreshed.PaidAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("expected invoice paid %v, got %v",
			amount.Amount(), refreshed.PaidAmount().Amount())
	}
}
