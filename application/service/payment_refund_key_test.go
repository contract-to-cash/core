package service

// Tests for the refund idempotency key amount-binding and the in-attempt key
// re-derivation (issue #235), plus the in-tx reload nil-guard (issue #241
// item 1). The gateway fake models a real provider's idempotency semantics:
// the first call under a key moves money; subsequent calls under the SAME key
// are replays that move nothing. The invariant under test: the ledger's
// cumulative refunded total always equals the gateway-moved total.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// dedupingRefundGateway is a key-deduping refund gateway fake: the first call
// for a key executes a real refund of the requested amount; repeat calls for
// the same key are idempotent replays that move no money.
type dedupingRefundGateway struct {
	mockGateway
	mu    sync.Mutex
	keys  []string
	moved map[string]*big.Rat // key -> amount actually moved (first call only)
}

func newDedupingRefundGateway() *dedupingRefundGateway {
	return &dedupingRefundGateway{moved: make(map[string]*big.Rat)}
}

func (g *dedupingRefundGateway) Refund(_ context.Context, req *port.RefundRequest) (*port.RefundResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.keys = append(g.keys, req.IdempotencyKey)
	if _, replay := g.moved[req.IdempotencyKey]; !replay {
		g.moved[req.IdempotencyKey] = new(big.Rat).Set(req.Amount.Amount())
	}
	return &port.RefundResponse{TransactionID: "rf-" + req.IdempotencyKey}, nil
}

// totalMoved returns the sum of all REAL refunds the gateway executed.
func (g *dedupingRefundGateway) totalMoved() *big.Rat {
	g.mu.Lock()
	defer g.mu.Unlock()
	total := new(big.Rat)
	for _, amt := range g.moved {
		total.Add(total, amt)
	}
	return total
}

func (g *dedupingRefundGateway) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.keys)
}

// staleReadPaymentRepo delegates to an inner repository but serves a canned
// STALE snapshot for the next `remaining` FindByID calls after arming. This
// deterministically reproduces a concurrent Refund invocation that loaded the
// payment before another refund committed.
type staleReadPaymentRepo struct {
	inner payment.Repository
	mu    sync.Mutex
	stale *payment.Payment
	// remaining counts how many upcoming FindByID calls return the stale snapshot.
	remaining int
}

func (r *staleReadPaymentRepo) arm(stale *payment.Payment, reads int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stale = stale
	r.remaining = reads
}

func (r *staleReadPaymentRepo) Save(ctx context.Context, p *payment.Payment) error {
	return r.inner.Save(ctx, p)
}

func (r *staleReadPaymentRepo) FindByID(ctx context.Context, id shared.PaymentID) (*payment.Payment, error) {
	r.mu.Lock()
	if r.remaining > 0 {
		r.remaining--
		stale := r.stale
		r.mu.Unlock()
		return stale, nil
	}
	r.mu.Unlock()
	return r.inner.FindByID(ctx, id)
}

func (r *staleReadPaymentRepo) FindByInvoiceID(ctx context.Context, id shared.InvoiceID) ([]*payment.Payment, error) {
	return r.inner.FindByInvoiceID(ctx, id)
}

func (r *staleReadPaymentRepo) FindByIdempotencyKey(ctx context.Context, key string) (*payment.Payment, error) {
	return r.inner.FindByIdempotencyKey(ctx, key)
}

func (r *staleReadPaymentRepo) FindStalePending(ctx context.Context, olderThan time.Time, limit int) ([]*payment.Payment, error) {
	return r.inner.FindStalePending(ctx, olderThan, limit)
}

// seedRefundablePayment stores a completed 10000 JPY payment and returns it
// together with a factory for stale (pre-refund) snapshots of the same payment.
func seedRefundablePayment(t *testing.T, ctx context.Context, inner payment.Repository, clock shared.Clock) (*payment.Payment, func() *payment.Payment) {
	t.Helper()
	id := shared.NewPaymentID()
	invoiceID := shared.NewInvoiceID()
	mk := func() *payment.Payment {
		p, err := payment.NewPayment(
			id, invoiceID,
			shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
			payment.PaymentMethodCreditCard,
			"gw_txn_235",
			clock.Now(),
		)
		if err != nil {
			t.Fatalf("NewPayment: %v", err)
		}
		if err := p.Complete(); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		return p
	}
	seed := mk()
	if err := inner.Save(ctx, seed); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	return seed, mk
}

// TestRefund_ConcurrentDistinctAmounts_LedgerMatchesGatewayMovedTotal is the
// issue #235 acceptance scenario: a 3000 refund and a 2000 refund race (the
// 2000 one loaded the payment before the 3000 one committed). With the old
// amount-independent key both derived the same key, the gateway deduped the
// second call (moving only 3000) and the ledger still recorded 5000 — booking
// a 2000 refund the gateway never executed. With the amount bound into the
// key, both are real gateway movements and the ledger matches the gateway.
func TestRefund_ConcurrentDistinctAmounts_LedgerMatchesGatewayMovedTotal(t *testing.T) {
	clock := newPaymentTestClock()
	ctx := context.Background()

	inner := inmemory.NewInMemoryPaymentRepository()
	seed, mkStale := seedRefundablePayment(t, ctx, inner, clock)
	repo := &staleReadPaymentRepo{inner: inner}
	gw := newDedupingRefundGateway()

	svc := NewPaymentService(
		gw,
		repo,
		&mockInvoiceRepoForPayment{},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	// First refund: 3000, normal path.
	first := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	if err := svc.Refund(ctx, seed.ID(), RefundInput{Amount: &first, Reason: port.RefundReasonRequestedByCustomer}); err != nil {
		t.Fatalf("first refund failed: %v", err)
	}

	// Second refund: 2000, but its pre-flight load observes the PRE-first
	// snapshot (refunded=0) — the concurrent-race interleaving.
	repo.arm(mkStale(), 1)
	second := shared.NewMoney(big.NewRat(2000, 1), shared.CurrencyJPY)
	if err := svc.Refund(ctx, seed.ID(), RefundInput{Amount: &second, Reason: port.RefundReasonRequestedByCustomer}); err != nil {
		t.Fatalf("second refund must converge and succeed, got: %v", err)
	}

	// Ledger total must equal the gateway-moved total (the issue #235 invariant).
	stored, err := inner.FindByID(ctx, seed.ID())
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	wantLedger := big.NewRat(5000, 1)
	if stored.RefundedAmount().Amount().Cmp(wantLedger) != 0 {
		t.Errorf("ledger refunded total = %s, want 5000", stored.RefundedAmount().Amount().RatString())
	}
	if moved := gw.totalMoved(); moved.Cmp(stored.RefundedAmount().Amount()) != 0 {
		t.Errorf("gateway-moved total (%s) must equal ledger total (%s) — issue #235",
			moved.RatString(), stored.RefundedAmount().Amount().RatString())
	}
	// Distinct amounts ⇒ distinct keys ⇒ two real movements, and the second
	// invocation's recording (against the advanced total) must NOT have hit
	// the gateway a second time (one invocation = at most one movement).
	if got := gw.callCount(); got != 2 {
		t.Errorf("expected exactly 2 gateway Refund calls (both real, one per invocation), got %d: %v", got, gw.keys)
	}
	if len(gw.moved) != 2 {
		t.Errorf("expected 2 REAL gateway refunds (distinct keys), got %d: %v", len(gw.moved), gw.keys)
	}
}

// TestRefund_ConcurrentSameAmount_LoserGetsConflict_SingleMovement pins the
// single-movement convergence policy for concurrent IDENTICAL refunds (issue
// #235 review): two racing refunds of the same amount from the same prior
// total share a derived key, so the gateway executes exactly ONE movement and
// replays the loser's call. The loser must NOT record (that would book a
// phantom refund) and must NOT re-hit the gateway under a fresh key (one
// Refund invocation = at most one gateway movement) — it returns
// ErrCodeConflict and the ledger stays equal to the gateway-moved total.
func TestRefund_ConcurrentSameAmount_LoserGetsConflict_SingleMovement(t *testing.T) {
	clock := newPaymentTestClock()
	ctx := context.Background()

	inner := inmemory.NewInMemoryPaymentRepository()
	seed, mkStale := seedRefundablePayment(t, ctx, inner, clock)
	repo := &staleReadPaymentRepo{inner: inner}
	gw := newDedupingRefundGateway()

	svc := NewPaymentService(
		gw,
		repo,
		&mockInvoiceRepoForPayment{},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	amount := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)

	// First refund: 3000, normal path — key derived from prior=0. Real movement.
	if err := svc.Refund(ctx, seed.ID(), RefundInput{Amount: &amount, Reason: port.RefundReasonRequestedByCustomer}); err != nil {
		t.Fatalf("first refund failed: %v", err)
	}

	// Concurrent duplicate: another 3000 refund whose pre-flight load is stale
	// (prior=0). It derives the SAME key, the gateway replays it (no
	// movement), and the in-tx classification detects the consumed key slot
	// (advance == own amount) → ErrCodeConflict, nothing recorded, no re-hit.
	repo.arm(mkStale(), 1)
	err := svc.Refund(ctx, seed.ID(), RefundInput{Amount: &amount, Reason: port.RefundReasonRequestedByCustomer})
	if err == nil {
		t.Fatal("concurrent identical refund loser must get a conflict, got nil")
	}
	assertDomainError(t, err, shared.ErrCodeConflict)

	stored, ferr := inner.FindByID(ctx, seed.ID())
	if ferr != nil {
		t.Fatalf("final load: %v", ferr)
	}
	// Exactly ONE movement: ledger 3000 == gateway-moved 3000.
	if stored.RefundedAmount().Amount().Cmp(big.NewRat(3000, 1)) != 0 {
		t.Errorf("ledger refunded total = %s, want 3000 (single movement)", stored.RefundedAmount().Amount().RatString())
	}
	if moved := gw.totalMoved(); moved.Cmp(stored.RefundedAmount().Amount()) != 0 {
		t.Errorf("gateway-moved total (%s) must equal ledger total (%s)",
			moved.RatString(), stored.RefundedAmount().Amount().RatString())
	}
	// Two gateway calls (one real, one replay under the shared key); only one
	// key ever moved money, and the loser never re-hit under a fresh key.
	if got := gw.callCount(); got != 2 {
		t.Errorf("expected 2 gateway Refund calls (real + replay, no fresh-key re-hit), got %d: %v", got, gw.keys)
	}
	if len(gw.moved) != 1 {
		t.Errorf("expected exactly 1 REAL gateway refund, got %d: %v", len(gw.moved), gw.keys)
	}
}

// failFirstSavePaymentRepo delegates to an inner repository but fails the
// first `failures` Save calls with a generic (non-version-conflict) error.
type failFirstSavePaymentRepo struct {
	inner    payment.Repository
	mu       sync.Mutex
	failures int
}

func (r *failFirstSavePaymentRepo) Save(ctx context.Context, p *payment.Payment) error {
	r.mu.Lock()
	if r.failures > 0 {
		r.failures--
		r.mu.Unlock()
		return fmt.Errorf("simulated save outage")
	}
	r.mu.Unlock()
	return r.inner.Save(ctx, p)
}

func (r *failFirstSavePaymentRepo) FindByID(ctx context.Context, id shared.PaymentID) (*payment.Payment, error) {
	return r.inner.FindByID(ctx, id)
}

func (r *failFirstSavePaymentRepo) FindByInvoiceID(ctx context.Context, id shared.InvoiceID) ([]*payment.Payment, error) {
	return r.inner.FindByInvoiceID(ctx, id)
}

func (r *failFirstSavePaymentRepo) FindByIdempotencyKey(ctx context.Context, key string) (*payment.Payment, error) {
	return r.inner.FindByIdempotencyKey(ctx, key)
}

func (r *failFirstSavePaymentRepo) FindStalePending(ctx context.Context, olderThan time.Time, limit int) ([]*payment.Payment, error) {
	return r.inner.FindStalePending(ctx, olderThan, limit)
}

// TestRefund_ExplicitKey_SequentialRetry_RecordsOnce verifies the legitimate
// explicit-key retry contract survives the concurrent-advance guard: gateway
// moved + recording failed → the caller retries with the SAME key → the prior
// total is unchanged (advance == 0), the gateway replays without moving money,
// and the retry records exactly once. Ledger == gateway-moved.
func TestRefund_ExplicitKey_SequentialRetry_RecordsOnce(t *testing.T) {
	clock := newPaymentTestClock()
	ctx := context.Background()

	inner := inmemory.NewInMemoryPaymentRepository()
	seed, _ := seedRefundablePayment(t, ctx, inner, clock)
	repo := &failFirstSavePaymentRepo{inner: inner, failures: 1}
	gw := newDedupingRefundGateway()

	svc := NewPaymentService(
		gw,
		repo,
		&mockInvoiceRepoForPayment{},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	amount := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	in := RefundInput{
		Amount:         &amount,
		Reason:         port.RefundReasonRequestedByCustomer,
		IdempotencyKey: "edge-refund-token-77",
	}

	// Invocation 1: gateway moves 3000, local Save fails → reconciliation error.
	if err := svc.Refund(ctx, seed.ID(), in); err == nil {
		t.Fatal("expected error when local save fails after gateway refund")
	}

	// Invocation 2, SAME explicit key: prior total unchanged (advance == 0) →
	// the gateway replays (no new movement) and the recording succeeds.
	if err := svc.Refund(ctx, seed.ID(), in); err != nil {
		t.Fatalf("sequential retry with the same explicit key must succeed, got: %v", err)
	}

	stored, ferr := inner.FindByID(ctx, seed.ID())
	if ferr != nil {
		t.Fatalf("final load: %v", ferr)
	}
	if stored.RefundedAmount().Amount().Cmp(big.NewRat(3000, 1)) != 0 {
		t.Errorf("ledger refunded total = %s, want 3000 (recorded once)", stored.RefundedAmount().Amount().RatString())
	}
	if moved := gw.totalMoved(); moved.Cmp(stored.RefundedAmount().Amount()) != 0 {
		t.Errorf("gateway-moved total (%s) must equal ledger total (%s)",
			moved.RatString(), stored.RefundedAmount().Amount().RatString())
	}
	if len(gw.moved) != 1 {
		t.Errorf("expected exactly 1 REAL gateway refund across both invocations, got %d: %v", len(gw.moved), gw.keys)
	}
}

// TestRefund_ExplicitKey_ConcurrentAdvance_ConflictNoRecord pins the
// explicit-key half of the concurrent-advance guard (issue #235 review): when
// a concurrent refund records against the payment while an explicit-key
// refund is in flight, replay-vs-real cannot be decided for a caller-owned
// key, so the service must return ErrCodeConflict WITHOUT recording, never
// re-hit the gateway, and log a MANUAL RECONCILIATION error.
func TestRefund_ExplicitKey_ConcurrentAdvance_ConflictNoRecord(t *testing.T) {
	clock := newPaymentTestClock()
	ctx := context.Background()

	inner := inmemory.NewInMemoryPaymentRepository()
	seed, mkStale := seedRefundablePayment(t, ctx, inner, clock)
	repo := &staleReadPaymentRepo{inner: inner}
	gw := newDedupingRefundGateway()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	svc := NewPaymentService(
		gw,
		repo,
		&mockInvoiceRepoForPayment{},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentLogger(logger),
	)

	amount := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)

	// Winner: records 3000 under explicit key "EXPL" (real movement).
	if err := svc.Refund(ctx, seed.ID(), RefundInput{
		Amount:         &amount,
		Reason:         port.RefundReasonRequestedByCustomer,
		IdempotencyKey: "EXPL",
	}); err != nil {
		t.Fatalf("winner refund failed: %v", err)
	}

	// Loser: same explicit key, stale pre-flight load (prior=0). The gateway
	// replays "EXPL" (no movement); the in-tx reload sees the total advanced →
	// conflict, nothing recorded, no fresh-key re-hit.
	repo.arm(mkStale(), 1)
	err := svc.Refund(ctx, seed.ID(), RefundInput{
		Amount:         &amount,
		Reason:         port.RefundReasonRequestedByCustomer,
		IdempotencyKey: "EXPL",
	})
	if err == nil {
		t.Fatal("explicit-key loser must get a conflict on concurrent advance, got nil")
	}
	assertDomainError(t, err, shared.ErrCodeConflict)

	stored, ferr := inner.FindByID(ctx, seed.ID())
	if ferr != nil {
		t.Fatalf("final load: %v", ferr)
	}
	// Nothing double-recorded: ledger 3000 == gateway-moved 3000.
	if stored.RefundedAmount().Amount().Cmp(big.NewRat(3000, 1)) != 0 {
		t.Errorf("ledger refunded total = %s, want 3000", stored.RefundedAmount().Amount().RatString())
	}
	if moved := gw.totalMoved(); moved.Cmp(stored.RefundedAmount().Amount()) != 0 {
		t.Errorf("gateway-moved total (%s) must equal ledger total (%s)",
			moved.RatString(), stored.RefundedAmount().Amount().RatString())
	}
	if len(gw.moved) != 1 {
		t.Errorf("expected exactly 1 REAL gateway refund, got %d: %v", len(gw.moved), gw.keys)
	}
	// The ambiguity must be escalated for manual reconciliation.
	if !containsAll(logBuf.String(), "MANUAL RECONCILIATION", "EXPL") {
		t.Errorf("expected MANUAL RECONCILIATION error log for explicit-key concurrent advance, got:\n%s", logBuf.String())
	}
}

// nilOnReloadPaymentRepo returns the payment for the first FindByID (the
// pre-flight load) and (nil, nil) afterwards — a BYO-DB adapter violating the
// FindByID error convention on the in-tx reload.
type nilOnReloadPaymentRepo struct {
	mockPaymentRepo
	mu    sync.Mutex
	p     *payment.Payment
	calls int
}

func (r *nilOnReloadPaymentRepo) FindByID(_ context.Context, _ shared.PaymentID) (*payment.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls == 1 {
		return r.p, nil
	}
	return nil, nil
}

// TestRefund_InTxReloadNil_ReturnsNotFound_NoPanic pins the issue #241 item 1
// nil-guard: a (nil, nil) result from the in-tx reload must surface as a clean
// not-found domain error instead of a nil-pointer panic on RecordRefund.
func TestRefund_InTxReloadNil_ReturnsNotFound_NoPanic(t *testing.T) {
	clock := newPaymentTestClock()
	ctx := context.Background()

	p, err := payment.NewPayment(
		shared.NewPaymentID(), shared.NewInvoiceID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard, "gw_txn_241", clock.Now(),
	)
	if err != nil {
		t.Fatalf("NewPayment: %v", err)
	}
	if err := p.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	repo := &nilOnReloadPaymentRepo{p: p}

	svc := NewPaymentService(
		&mockGateway{},
		repo,
		&mockInvoiceRepoForPayment{},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	err = svc.Refund(ctx, p.ID(), RefundInput{Reason: port.RefundReasonRequestedByCustomer})
	if err == nil {
		t.Fatal("expected not-found error for nil in-tx reload")
	}
	assertDomainError(t, err, shared.ErrCodeNotFound)
}

// TestRefund_ThreeWriters_CommitOrderACB_NoPhantomRecord is the phantom-record
// regression scenario (issue #235 review): three refunds A(3000), B(3000) and
// C(2000) all load the payment at priorRefunded=0. A and B derive the SAME key
// (0,3000) — the gateway executes A and replays B without moving money — while
// C derives its own key (0,2000) and is a real movement. Commit order A → C →
// B: when B's recording attempt reloads, the cumulative total has advanced by
// 5000 (A's 3000 + C's 2000), which under the old cumulative-advance heuristic
// fell into "movement was real" and recorded a 3000 the gateway never moved
// (ledger 8000 vs gateway 5000, silently). The per-refund key ledger detects
// B's key on A's recorded refund and converges WITHOUT recording.
func TestRefund_ThreeWriters_CommitOrderACB_NoPhantomRecord(t *testing.T) {
	clock := newPaymentTestClock()
	ctx := context.Background()

	inner := inmemory.NewInMemoryPaymentRepository()
	seed, mkStale := seedRefundablePayment(t, ctx, inner, clock)
	repo := &staleReadPaymentRepo{inner: inner}
	gw := newDedupingRefundGateway()

	svc := NewPaymentService(
		gw,
		repo,
		&mockInvoiceRepoForPayment{},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	a := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	b := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	c := shared.NewMoney(big.NewRat(2000, 1), shared.CurrencyJPY)

	// A: 3000, normal path (prior=0, key (0,3000)) — real movement, recorded.
	if err := svc.Refund(ctx, seed.ID(), RefundInput{Amount: &a, Reason: port.RefundReasonRequestedByCustomer}); err != nil {
		t.Fatalf("refund A failed: %v", err)
	}

	// C: 2000, stale pre-flight load (prior=0, key (0,2000)) — distinct key,
	// real movement; the in-tx classification sees A's advance, finds C's key
	// absent from a COMPLETE key ledger, and records.
	repo.arm(mkStale(), 1)
	if err := svc.Refund(ctx, seed.ID(), RefundInput{Amount: &c, Reason: port.RefundReasonRequestedByCustomer}); err != nil {
		t.Fatalf("refund C must converge and record its real movement, got: %v", err)
	}

	// B: 3000, stale pre-flight load (prior=0) — derives A's key (0,3000); the
	// gateway replays it without moving money. B's reload sees advance=5000
	// (neither 0 nor 3000); the recorded-refund key match must classify it as
	// a replay and NOT record.
	repo.arm(mkStale(), 1)
	err := svc.Refund(ctx, seed.ID(), RefundInput{Amount: &b, Reason: port.RefundReasonRequestedByCustomer})
	if err == nil {
		t.Fatal("refund B (replayed key) must get a conflict, got nil")
	}
	assertDomainError(t, err, shared.ErrCodeConflict)

	stored, ferr := inner.FindByID(ctx, seed.ID())
	if ferr != nil {
		t.Fatalf("final load: %v", ferr)
	}
	// THE invariant: ledger total == gateway-moved total (5000). Before the
	// fix the ledger recorded 8000 against 5000 moved.
	wantTotal := big.NewRat(5000, 1)
	if stored.RefundedAmount().Amount().Cmp(wantTotal) != 0 {
		t.Errorf("ledger refunded total = %s, want 5000 (no phantom record)", stored.RefundedAmount().Amount().RatString())
	}
	if moved := gw.totalMoved(); moved.Cmp(stored.RefundedAmount().Amount()) != 0 {
		t.Errorf("gateway-moved total (%s) must equal ledger total (%s) — no path may record an amount the gateway did not move",
			moved.RatString(), stored.RefundedAmount().Amount().RatString())
	}
	// Exactly two REAL movements (A's 3000 and C's 2000); B's call was a replay.
	if len(gw.moved) != 2 {
		t.Errorf("expected exactly 2 REAL gateway refunds, got %d: %v", len(gw.moved), gw.keys)
	}
	if got := gw.callCount(); got != 3 {
		t.Errorf("expected 3 gateway Refund calls (A real, C real, B replay), got %d: %v", got, gw.keys)
	}
}

// TestRefund_ThreeWriters_CommitOrderABC_Converges pins that the A → B → C
// commit order keeps converging exactly as before the fix: A records its real
// 3000, B (same key as A) converges with a conflict and no record, and C's
// real 2000 — whose key is absent from the complete key ledger — is recorded.
func TestRefund_ThreeWriters_CommitOrderABC_Converges(t *testing.T) {
	clock := newPaymentTestClock()
	ctx := context.Background()

	inner := inmemory.NewInMemoryPaymentRepository()
	seed, mkStale := seedRefundablePayment(t, ctx, inner, clock)
	repo := &staleReadPaymentRepo{inner: inner}
	gw := newDedupingRefundGateway()

	svc := NewPaymentService(
		gw,
		repo,
		&mockInvoiceRepoForPayment{},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	a := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	b := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	c := shared.NewMoney(big.NewRat(2000, 1), shared.CurrencyJPY)

	// A: real movement, recorded.
	if err := svc.Refund(ctx, seed.ID(), RefundInput{Amount: &a, Reason: port.RefundReasonRequestedByCustomer}); err != nil {
		t.Fatalf("refund A failed: %v", err)
	}
	// B: stale load → same key as A → gateway replay → conflict, no record.
	repo.arm(mkStale(), 1)
	if err := svc.Refund(ctx, seed.ID(), RefundInput{Amount: &b, Reason: port.RefundReasonRequestedByCustomer}); err == nil {
		t.Fatal("refund B (replayed key) must get a conflict, got nil")
	} else {
		assertDomainError(t, err, shared.ErrCodeConflict)
	}
	// C: stale load → own key (0,2000) → real movement → recorded.
	repo.arm(mkStale(), 1)
	if err := svc.Refund(ctx, seed.ID(), RefundInput{Amount: &c, Reason: port.RefundReasonRequestedByCustomer}); err != nil {
		t.Fatalf("refund C must converge and record its real movement, got: %v", err)
	}

	stored, ferr := inner.FindByID(ctx, seed.ID())
	if ferr != nil {
		t.Fatalf("final load: %v", ferr)
	}
	wantTotal := big.NewRat(5000, 1)
	if stored.RefundedAmount().Amount().Cmp(wantTotal) != 0 {
		t.Errorf("ledger refunded total = %s, want 5000", stored.RefundedAmount().Amount().RatString())
	}
	if moved := gw.totalMoved(); moved.Cmp(stored.RefundedAmount().Amount()) != 0 {
		t.Errorf("gateway-moved total (%s) must equal ledger total (%s)",
			moved.RatString(), stored.RefundedAmount().Amount().RatString())
	}
	if len(gw.moved) != 2 {
		t.Errorf("expected exactly 2 REAL gateway refunds, got %d: %v", len(gw.moved), gw.keys)
	}
}

// TestRefund_UntrackedRefundHistory_ConcurrentAdvance_ConservativeConflict
// pins the fallback for payments whose refund history does not carry complete
// idempotency keys (refunds recorded through the legacy keyless RecordRefund,
// or persisted before key tracking existed): a concurrent advance can then
// neither be proven a replay nor a real movement, so the derived-key path must
// return a conservative ErrCodeConflict with an Error-level log instructing
// gateway-state verification — and record NOTHING.
func TestRefund_UntrackedRefundHistory_ConcurrentAdvance_ConservativeConflict(t *testing.T) {
	clock := newPaymentTestClock()
	ctx := context.Background()

	inner := inmemory.NewInMemoryPaymentRepository()
	seed, mkStale := seedRefundablePayment(t, ctx, inner, clock)
	repo := &staleReadPaymentRepo{inner: inner}
	gw := newDedupingRefundGateway()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	svc := NewPaymentService(
		gw,
		repo,
		&mockInvoiceRepoForPayment{},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentLogger(logger),
	)

	// Concurrent winner: an integrator records a KEYLESS 3000 refund directly
	// on the entity (its own gateway bookkeeping) and saves — the key ledger
	// is now incomplete.
	loaded, err := inner.FindByID(ctx, seed.ID())
	if err != nil {
		t.Fatalf("load for keyless refund: %v", err)
	}
	if err := loaded.RecordRefund(shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)); err != nil {
		t.Fatalf("keyless RecordRefund: %v", err)
	}
	if err := inner.Save(ctx, loaded); err != nil {
		t.Fatalf("save keyless refund: %v", err)
	}

	// Loser: derived-key 2000 refund with a stale pre-flight load (prior=0).
	// Its in-tx reload sees an advance it cannot classify (incomplete key
	// ledger) → conservative conflict, nothing recorded.
	repo.arm(mkStale(), 1)
	amt := shared.NewMoney(big.NewRat(2000, 1), shared.CurrencyJPY)
	err = svc.Refund(ctx, seed.ID(), RefundInput{Amount: &amt, Reason: port.RefundReasonRequestedByCustomer})
	if err == nil {
		t.Fatal("expected conservative conflict on untracked refund history, got nil")
	}
	assertDomainError(t, err, shared.ErrCodeConflict)

	stored, ferr := inner.FindByID(ctx, seed.ID())
	if ferr != nil {
		t.Fatalf("final load: %v", ferr)
	}
	// Nothing recorded by the loser: the ledger still shows only the keyless 3000.
	if stored.RefundedAmount().Amount().Cmp(big.NewRat(3000, 1)) != 0 {
		t.Errorf("ledger refunded total = %s, want 3000 (loser must not record)", stored.RefundedAmount().Amount().RatString())
	}
	// The undecidable state must be escalated at Error level with a
	// gateway-verification instruction.
	if !containsAll(logBuf.String(), "MANUAL RECONCILIATION", "verify gateway state") {
		t.Errorf("expected Error-level MANUAL RECONCILIATION log instructing gateway verification, got:\n%s", logBuf.String())
	}
}
