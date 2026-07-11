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
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// --- Refund-aware test gateway (issue #150) ---
//
// refundTrackingGateway embeds integrationGateway for the Charge path (used to
// seed a completed payment) and the not-implemented methods, and overrides
// Refund to model a real gateway's idempotency semantics:
//
//   - Every Refund call's IdempotencyKey is recorded in refundKeys.
//   - The FIRST successful call for a given key moves money and is remembered;
//     subsequent calls with the SAME key are idempotent replays that return the
//     cached response WITHOUT moving money again. len(realRefunds) is therefore
//     the number of real refunds that actually happened.
//   - failKeys lets a test make the first N calls for a key fail (simulating a
//     network timeout) before the call would otherwise succeed.
type refundTrackingGateway struct {
	*integrationGateway

	rmu         sync.Mutex
	refundKeys  []string
	realRefunds map[string]*port.RefundResponse
	failKeys    map[string]int
}

func newRefundTrackingGateway() *refundTrackingGateway {
	return &refundTrackingGateway{
		integrationGateway: &integrationGateway{},
		realRefunds:        make(map[string]*port.RefundResponse),
		failKeys:           make(map[string]int),
	}
}

func (g *refundTrackingGateway) Refund(_ context.Context, req *port.RefundRequest) (*port.RefundResponse, error) {
	g.rmu.Lock()
	defer g.rmu.Unlock()

	g.refundKeys = append(g.refundKeys, req.IdempotencyKey)

	if n := g.failKeys[req.IdempotencyKey]; n > 0 {
		g.failKeys[req.IdempotencyKey] = n - 1
		return nil, fmt.Errorf("simulated gateway timeout for refund key %q", req.IdempotencyKey)
	}

	if cached, ok := g.realRefunds[req.IdempotencyKey]; ok {
		// Idempotent replay — no new money movement.
		return cached, nil
	}

	resp := &port.RefundResponse{
		RefundID:      "refund-" + req.IdempotencyKey,
		TransactionID: req.TransactionID,
		Status:        port.RefundStatusSucceeded,
		RefundedAt:    time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	g.realRefunds[req.IdempotencyKey] = resp
	return resp, nil
}

func (g *refundTrackingGateway) distinctRealRefunds() int {
	g.rmu.Lock()
	defer g.rmu.Unlock()
	return len(g.realRefunds)
}

func (g *refundTrackingGateway) recordedKeys() []string {
	g.rmu.Lock()
	defer g.rmu.Unlock()
	out := make([]string, len(g.refundKeys))
	copy(out, g.refundKeys)
	return out
}

// --- isolatingPaymentRepo mirrors a real RDBMS's per-tx snapshot isolation ---
//
// Every FindBy* returns a fresh snapshot-round-tripped clone and Save stores an
// independent copy, so concurrent PaymentService callers never share a single
// *Payment pointer whose refundedAmount would be mutated out from under them.
// Without this, the in-memory repo's pointer sharing both masks the race and
// trips the -race detector.
type isolatingPaymentRepo struct {
	inner payment.Repository
}

func clonePayment(p *payment.Payment) (*payment.Payment, error) {
	return payment.FromSnapshot(p.ToSnapshot())
}

func (r *isolatingPaymentRepo) Save(ctx context.Context, p *payment.Payment) error {
	// Delegate directly WITHOUT pre-cloning: the inner in-memory repository
	// already stores an isolated snapshot copy (issue #152), so a wrapper clone
	// here is redundant. It would also be harmful now that Payment carries an
	// optimistic-locking version (issue #190): cloning via FromSnapshot collapses
	// loadedVersion into version, so the inner repo's LoadedVersion-vs-stored
	// check could never pass — even for the race winner. Passing the caller's
	// pointer through preserves the (version, loadedVersion) pair the inner check
	// relies on.
	return r.inner.Save(ctx, p)
}

func (r *isolatingPaymentRepo) FindByID(ctx context.Context, id shared.PaymentID) (*payment.Payment, error) {
	p, err := r.inner.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return clonePayment(p)
}

func (r *isolatingPaymentRepo) FindByInvoiceID(ctx context.Context, invoiceID shared.InvoiceID) ([]*payment.Payment, error) {
	ps, err := r.inner.FindByInvoiceID(ctx, invoiceID)
	if err != nil {
		return nil, err
	}
	out := make([]*payment.Payment, 0, len(ps))
	for _, p := range ps {
		clone, cErr := clonePayment(p)
		if cErr != nil {
			return nil, cErr
		}
		out = append(out, clone)
	}
	return out, nil
}

func (r *isolatingPaymentRepo) FindByIdempotencyKey(ctx context.Context, key string) (*payment.Payment, error) {
	p, err := r.inner.FindByIdempotencyKey(ctx, key)
	if err != nil || p == nil {
		return nil, err
	}
	return clonePayment(p)
}

// seedCompletedPayment charges an invoice through the service so a completed
// payment exists to refund.
func seedCompletedPayment(t *testing.T, ctx context.Context, svc *service.PaymentService, invoiceID shared.InvoiceID, amount shared.Money, key string) *payment.Payment {
	t.Helper()
	p, err := svc.ProcessPayment(ctx, invoiceID, service.ProcessPaymentInput{
		PaymentMethodID: "pm-refund",
		Amount:          amount,
		Currency:        amount.Currency(),
		IdempotencyKey:  key,
	})
	if err != nil {
		t.Fatalf("failed to seed completed payment: %v", err)
	}
	if p.Status() != payment.PaymentStatusCompleted {
		t.Fatalf("seeded payment must be completed, got %q", p.Status())
	}
	return p
}

// TestRefund_RetryAfterGatewayTimeout_ReusesIdempotencyKey (issue #150 case a)
// verifies that a refund retried after a gateway timeout re-sends the SAME
// deterministic idempotency key, so a real gateway would collapse the two calls
// into a single money movement instead of refunding twice.
func TestRefund_RetryAfterGatewayTimeout_ReusesIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()

	amount := moneyJPY(10000)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	gw := newRefundTrackingGateway()

	svc := service.NewPaymentService(
		gw,
		paymentRepo,
		invoiceRepo,
		nil,
		nil,
		plugin.NewRegistry(),
		clock,
	)

	seedCompletedPayment(t, ctx, svc, inv.ID(), amount, "seed-key-timeout")

	pmts, _ := paymentRepo.FindByInvoiceID(ctx, inv.ID())
	if len(pmts) != 1 {
		t.Fatalf("expected 1 seeded payment, got %d", len(pmts))
	}
	paymentID := pmts[0].ID()

	// Make the first Refund call for the derived key fail (gateway timeout).
	expectedKey := "refund-" + string(paymentID) + "-JPY-0"
	gw.failKeys[expectedKey] = 1

	// First refund attempt: gateway "times out" → error, nothing recorded.
	err := svc.Refund(ctx, paymentID, service.RefundInput{Reason: port.RefundReasonRequestedByCustomer})
	if err == nil {
		t.Fatal("expected first refund to fail with a gateway timeout")
	}

	// Payment must be unchanged (still completed, nothing refunded).
	afterFail, _ := paymentRepo.FindByID(ctx, paymentID)
	if afterFail.Status() != payment.PaymentStatusCompleted {
		t.Fatalf("payment must remain completed after gateway timeout, got %q", afterFail.Status())
	}
	if afterFail.RefundedAmount().Amount().Sign() != 0 {
		t.Fatalf("no amount must be recorded as refunded after timeout, got %v", afterFail.RefundedAmount().Amount())
	}

	// Retry: must reuse the SAME idempotency key and succeed this time.
	if err := svc.Refund(ctx, paymentID, service.RefundInput{Reason: port.RefundReasonRequestedByCustomer}); err != nil {
		t.Fatalf("retry must succeed: %v", err)
	}

	keys := gw.recordedKeys()
	if len(keys) != 2 {
		t.Fatalf("expected exactly 2 gateway Refund calls, got %d: %v", len(keys), keys)
	}
	if keys[0] != keys[1] {
		t.Errorf("retry must reuse the same idempotency key; got %q then %q", keys[0], keys[1])
	}
	if keys[0] != expectedKey {
		t.Errorf("derived key mismatch: want %q, got %q", expectedKey, keys[0])
	}

	// Exactly one real refund happened at the gateway.
	if n := gw.distinctRealRefunds(); n != 1 {
		t.Errorf("expected exactly 1 real refund at the gateway, got %d", n)
	}

	refunded, _ := paymentRepo.FindByID(ctx, paymentID)
	if refunded.Status() != payment.PaymentStatusRefunded {
		t.Errorf("payment must be fully refunded, got %q", refunded.Status())
	}
	if refunded.RefundedAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("expected refunded amount %v, got %v", amount.Amount(), refunded.RefundedAmount().Amount())
	}
}

// TestRefund_ConcurrentRefunds_SingleRealRefund (issue #150 case b) verifies
// that two concurrent Refund calls for the same payment result in EXACTLY ONE
// real gateway refund and exactly one recorded refund: both derive the same
// deterministic idempotency key (so the gateway dedupes), and the in-tx
// re-validation makes the loser fail with a clean domain error instead of
// double-recording.
func TestRefund_ConcurrentRefunds_SingleRealRefund(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	innerPaymentRepo := inmemory.NewInMemoryPaymentRepository()
	// Snapshot-isolating repo: each FindByID returns a fresh committed clone,
	// mirroring a real DB's per-tx snapshot (and keeping -race clean).
	paymentRepo := &isolatingPaymentRepo{inner: innerPaymentRepo}

	amount := moneyJPY(10000)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	gw := newRefundTrackingGateway()

	// serializingTxManager simulates row-lock/SERIALIZABLE serialization so the
	// loser's in-tx re-load observes the winner's committed refund.
	txm := &serializingTxManager{
		inner: tx.NewNoopTxManager(tx.Repos{Payments: paymentRepo, Invoices: invoiceRepo}),
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
	)

	seedCompletedPayment(t, ctx, svc, inv.ID(), amount, "seed-key-concurrent")

	pmts, _ := paymentRepo.FindByInvoiceID(ctx, inv.ID())
	if len(pmts) != 1 {
		t.Fatalf("expected 1 seeded payment, got %d", len(pmts))
	}
	paymentID := pmts[0].ID()

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = svc.Refund(ctx, paymentID, service.RefundInput{Reason: port.RefundReasonRequestedByCustomer})
		}(i)
	}
	wg.Wait()

	// Exactly one goroutine must succeed; the other must get a clean domain error.
	successes, failures := 0, 0
	var loserErr error
	for _, err := range errs {
		if err == nil {
			successes++
		} else {
			failures++
			loserErr = err
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("expected exactly one success and one failure, got successes=%d failures=%d (errs=%v)",
			successes, failures, errs)
	}

	// The loser's error must be a domain error (invalid state / over-refund),
	// NOT a manual-reconciliation persistence failure.
	var domainErr *shared.DomainError
	if !errors.As(loserErr, &domainErr) {
		t.Fatalf("loser error must be a domain error, got %T: %v", loserErr, loserErr)
	}
	if domainErr.Code != shared.ErrCodeInvalidStateTransition && domainErr.Code != shared.ErrCodeBusinessRule {
		t.Errorf("unexpected domain error code %q: %v", domainErr.Code, loserErr)
	}

	// Exactly one real refund at the gateway (both calls shared one key).
	if n := gw.distinctRealRefunds(); n != 1 {
		t.Errorf("expected exactly 1 real gateway refund, got %d (keys=%v)", n, gw.recordedKeys())
	}

	// The payment is fully refunded exactly once.
	refunded, _ := paymentRepo.FindByID(ctx, paymentID)
	if refunded.Status() != payment.PaymentStatusRefunded {
		t.Errorf("payment must be fully refunded, got %q", refunded.Status())
	}
	if refunded.RefundedAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("expected refunded amount %v (no double-recording), got %v",
			amount.Amount(), refunded.RefundedAmount().Amount())
	}
}

// TestRefund_SequentialPartialRefunds_UseDistinctKeys (issue #150) verifies
// that two DISTINCT partial refunds of the same payment each derive a DIFFERENT
// deterministic idempotency key (keyed on the pre-refund cumulative total), so
// the gateway does not incorrectly dedupe two legitimately different refunds.
func TestRefund_SequentialPartialRefunds_UseDistinctKeys(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()

	amount := moneyJPY(5000)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	gw := newRefundTrackingGateway()

	svc := service.NewPaymentService(
		gw,
		paymentRepo,
		invoiceRepo,
		nil,
		nil,
		plugin.NewRegistry(),
		clock,
	)

	seedCompletedPayment(t, ctx, svc, inv.ID(), amount, "seed-key-partial")

	pmts, _ := paymentRepo.FindByInvoiceID(ctx, inv.ID())
	paymentID := pmts[0].ID()

	first := moneyJPY(3000)
	if err := svc.Refund(ctx, paymentID, service.RefundInput{Amount: &first, Reason: port.RefundReasonRequestedByCustomer}); err != nil {
		t.Fatalf("first partial refund failed: %v", err)
	}

	afterFirst, _ := paymentRepo.FindByID(ctx, paymentID)
	if afterFirst.Status() != payment.PaymentStatusPartiallyRefunded {
		t.Fatalf("expected partially_refunded after first refund, got %q", afterFirst.Status())
	}

	second := moneyJPY(2000)
	if err := svc.Refund(ctx, paymentID, service.RefundInput{Amount: &second, Reason: port.RefundReasonRequestedByCustomer}); err != nil {
		t.Fatalf("second partial refund failed: %v", err)
	}

	keys := gw.recordedKeys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 gateway Refund calls, got %d: %v", len(keys), keys)
	}
	if keys[0] == keys[1] {
		t.Errorf("distinct partial refunds must use DIFFERENT idempotency keys; both were %q", keys[0])
	}
	wantFirst := "refund-" + string(paymentID) + "-JPY-0"
	wantSecond := "refund-" + string(paymentID) + "-JPY-3000"
	if keys[0] != wantFirst {
		t.Errorf("first key: want %q, got %q", wantFirst, keys[0])
	}
	if keys[1] != wantSecond {
		t.Errorf("second key: want %q, got %q", wantSecond, keys[1])
	}

	// Both were real refunds (two distinct keys).
	if n := gw.distinctRealRefunds(); n != 2 {
		t.Errorf("expected 2 real gateway refunds, got %d", n)
	}

	final, _ := paymentRepo.FindByID(ctx, paymentID)
	if final.Status() != payment.PaymentStatusRefunded {
		t.Errorf("payment must be fully refunded after both partials, got %q", final.Status())
	}
	if final.RefundedAmount().Amount().Cmp(amount.Amount()) != 0 {
		t.Errorf("expected cumulative refunded %v, got %v", amount.Amount(), final.RefundedAmount().Amount())
	}
}

// TestRefund_ExplicitIdempotencyKey_IsHonored verifies that a caller-supplied
// RefundInput.IdempotencyKey overrides the derived key and is forwarded to the
// gateway verbatim.
func TestRefund_ExplicitIdempotencyKey_IsHonored(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()

	amount := moneyJPY(8000)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	gw := newRefundTrackingGateway()

	svc := service.NewPaymentService(
		gw,
		paymentRepo,
		invoiceRepo,
		nil,
		nil,
		plugin.NewRegistry(),
		clock,
	)

	seedCompletedPayment(t, ctx, svc, inv.ID(), amount, "seed-key-explicit")
	pmts, _ := paymentRepo.FindByInvoiceID(ctx, inv.ID())
	paymentID := pmts[0].ID()

	if err := svc.Refund(ctx, paymentID, service.RefundInput{
		Reason:         port.RefundReasonRequestedByCustomer,
		IdempotencyKey: "my-explicit-refund-key",
	}); err != nil {
		t.Fatalf("refund with explicit key failed: %v", err)
	}

	keys := gw.recordedKeys()
	if len(keys) != 1 || keys[0] != "my-explicit-refund-key" {
		t.Errorf("gateway must receive the explicit key; got %v", keys)
	}
}
