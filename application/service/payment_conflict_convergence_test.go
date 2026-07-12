package service

// Tests for duplicate-key / terminal-state conflict convergence:
//
//   - issue #233: a duplicate-idempotency-key race inside a CALLER-OWNED
//     (joined) transaction must return a retryable conflict WITHOUT reading the
//     winner and WITHOUT saga compensation; and at the top level, a failed
//     winner read after the duplicate-key violation must NOT compensate either
//     (the violation itself proves a winner owns the charge).
//   - issue #234: an in-tx terminal-state idempotency conflict must converge
//     without compensation (the gateway Charge was an idempotent replay; a
//     compensation Refund would reverse the original transaction a second time).
//   - issue #241 item 2: the zero-amount settlement path must converge on the
//     duplicate-key winner AFTER its transaction has exited, via the same abort
//     sentinel as the gateway path.

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// dupSavePaymentRepo always rejects Save with the duplicate-idempotency-key
// sentinel and counts FindByIdempotencyKey calls. findErrFromCall (when > 0)
// makes FindByIdempotencyKey fail from that call number on; otherwise it
// returns nil (no visible winner).
type dupSavePaymentRepo struct {
	mockPaymentRepo
	mu              sync.Mutex
	findCalls       int
	findErrFromCall int
}

func (r *dupSavePaymentRepo) Save(_ context.Context, _ *payment.Payment) error {
	return payment.ErrDuplicateIdempotencyKey
}

func (r *dupSavePaymentRepo) FindByIdempotencyKey(_ context.Context, _ string) (*payment.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.findCalls++
	if r.findErrFromCall > 0 && r.findCalls >= r.findErrFromCall {
		return nil, fmt.Errorf("simulated read failure (connection reset)")
	}
	return nil, nil
}

func (r *dupSavePaymentRepo) findCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.findCalls
}

func TestProcessPayment_JoinedTx_DuplicateKey_NoWinnerReadNoCompensation(t *testing.T) {
	// Issue #233: when ProcessPayment runs inside a caller's transaction
	// (tx.Run join semantics), a duplicate-key violation has aborted the
	// AMBIENT transaction (Postgres 25P02). The winner re-read would fail
	// spuriously and, before the fix, route the loser through saga.Compensate —
	// refunding the winner's legitimate charge. The service must instead
	// return a retryable ErrCodeConflict WITHOUT reading and WITHOUT
	// compensating.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	repo := &dupSavePaymentRepo{}
	gw := &spyGateway{}

	svc := NewPaymentService(
		gw,
		repo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithoutPaymentTransactions(),
	)

	outerTxm := tx.NewNoopTxManagerExplicit(tx.Repos{Payments: repo, Invoices: invRepo})

	var pmt *payment.Payment
	var procErr error
	_ = tx.Run(context.Background(), outerTxm, func(txCtx context.Context, _ tx.Repos) error {
		pmt, procErr = svc.ProcessPayment(txCtx, inv.ID(), ProcessPaymentInput{
			PaymentMethodID: "pm-001",
			Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
			Currency:        shared.CurrencyJPY,
			IdempotencyKey:  "key-joined-dup",
		})
		return procErr // caller rolls its transaction back
	})

	if procErr == nil {
		t.Fatal("expected retryable conflict error in joined-tx duplicate-key race")
	}
	assertDomainError(t, procErr, shared.ErrCodeConflict)
	if pmt != nil {
		t.Errorf("expected nil payment, got %+v", pmt)
	}
	// The winner must NOT have been re-read: exactly two lookups happened
	// (pre-charge + in-tx). A third would be the forbidden read on the aborted
	// ambient transaction.
	if got := repo.findCallCount(); got != 2 {
		t.Errorf("expected exactly 2 FindByIdempotencyKey calls (pre-charge + in-tx), got %d — the joined-tx path must not re-read the winner", got)
	}
	// Compensation must NOT fire — the winner owns the shared gateway charge.
	if gw.voidCalled || gw.refundCalled {
		t.Errorf("saga compensation must NOT fire in joined-tx duplicate-key race (void=%v refund=%v)", gw.voidCalled, gw.refundCalled)
	}
}

func TestProcessPayment_TopLevel_DuplicateKey_WinnerReadFails_NoCompensation(t *testing.T) {
	// Issue #233 (top-level hardening): the duplicate-key violation proves a
	// concurrent winner owns the gateway charge. When the post-rollback winner
	// read fails (infrastructure hiccup), the service must return a retryable
	// conflict instead of compensating — a Refund here would unwind the
	// winner's legitimate charge.
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	// Calls 1 (pre-charge) and 2 (in-tx) return nil; call 3 (post-tx winner
	// read) fails.
	repo := &dupSavePaymentRepo{findErrFromCall: 3}
	gw := &spyGateway{}

	svc := NewPaymentService(
		gw,
		repo,
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
		IdempotencyKey:  "key-dup-read-fail",
	})

	if err == nil {
		t.Fatal("expected conflict error when winner read fails after duplicate-key violation")
	}
	assertDomainError(t, err, shared.ErrCodeConflict)
	if pmt != nil {
		t.Errorf("expected nil payment, got %+v", pmt)
	}
	if got := repo.findCallCount(); got != 3 {
		t.Errorf("expected 3 FindByIdempotencyKey calls (pre-charge, in-tx, post-tx winner read), got %d", got)
	}
	if gw.voidCalled || gw.refundCalled {
		t.Errorf("saga compensation must NOT fire when the winner read fails (void=%v refund=%v) — the duplicate-key violation proves a winner owns the charge", gw.voidCalled, gw.refundCalled)
	}
}

// hideFirstLookupRepo wraps fakePaymentRepo and hides the next `hidden`
// FindByIdempotencyKey lookups for hideKey (returning nil), simulating the
// pre-charge read racing a concurrent writer whose record only becomes
// visible to the in-tx read.
type hideFirstLookupRepo struct {
	*fakePaymentRepo
	mu      sync.Mutex
	hideKey string
	hidden  int
}

func (r *hideFirstLookupRepo) FindByIdempotencyKey(ctx context.Context, key string) (*payment.Payment, error) {
	r.mu.Lock()
	if key == r.hideKey && r.hidden > 0 {
		r.hidden--
		r.mu.Unlock()
		return nil, nil
	}
	r.mu.Unlock()
	return r.fakePaymentRepo.FindByIdempotencyKey(ctx, key)
}

func TestProcessPayment_ReplayOfRefundedPayment_NeverFiresCompensationRefund(t *testing.T) {
	// Issue #234 end-to-end: a payment is charged with key "K" and later
	// refunded via PaymentService.Refund (which uses its own refund-... keys —
	// the payment record under "K" transitions to Refunded). A stale client
	// then replays ProcessPayment with "K", and the pre-charge lookup misses
	// (simulated race), so the gateway Charge runs (idempotent replay) and the
	// in-tx idempotency switch finds the Refunded record.
	//
	// Before the fix that in-tx ErrCodeConflict fell into the generic
	// compensation path and issued a SECOND real refund under a "comp-" key.
	// After the fix the caller gets the conflict and gateway.Refund is never
	// called with a comp- key.
	clock := newPaymentTestClock()
	// Fixed IDs so the invoice can be replaced with a fresh unpaid instance
	// before the replay (the seed charge marks the first instance paid, which
	// would otherwise fail the replay's up-front payment validation before the
	// in-tx branch under test is reached).
	invoiceID := shared.NewInvoiceID()
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	mkInvoice := func() *invoice.Invoice {
		i, err := invoice.NewInvoice(
			invoiceID, accountID, contractID,
			shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
			shared.Zero(shared.CurrencyJPY),
			shared.Zero(shared.CurrencyJPY),
		)
		if err != nil {
			t.Fatalf("NewInvoice: %v", err)
		}
		_ = i.Finalize()
		return i
	}
	inv := mkInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	inner := newFakePaymentRepo()
	repo := &hideFirstLookupRepo{fakePaymentRepo: inner, hideKey: "key-replay-refunded"}
	gw := &trackingGateway{}

	svc := NewPaymentService(
		gw,
		repo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	amount := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)

	// 1. Charge under "K".
	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          amount,
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-replay-refunded",
	})
	if err != nil {
		t.Fatalf("seed charge failed: %v", err)
	}

	// 2. Refund it in full (a DIFFERENT, refund-derived gateway key).
	if err := svc.Refund(context.Background(), pmt.ID(), RefundInput{
		Reason: port.RefundReasonRequestedByCustomer,
	}); err != nil {
		t.Fatalf("seed refund failed: %v", err)
	}
	legitRefunds := len(gw.refundKeys)
	if legitRefunds != 1 {
		t.Fatalf("expected exactly 1 legitimate refund call, got %d (%v)", legitRefunds, gw.refundKeys)
	}

	// 3. Replay ProcessPayment with "K"; hide the pre-charge lookup so the
	//    in-tx defense-in-depth branch is the one that fires, and reset the
	//    invoice to an unpaid instance so the replay survives the up-front
	//    payment validation.
	invRepo.inv = mkInvoice()
	repo.mu.Lock()
	repo.hidden = 1
	repo.mu.Unlock()

	replayPmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          amount,
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-replay-refunded",
	})
	if err == nil {
		t.Fatal("expected conflict error replaying a refunded payment's key")
	}
	assertDomainError(t, err, shared.ErrCodeConflict)
	if replayPmt != nil {
		t.Errorf("expected nil payment on terminal-state replay, got %+v", replayPmt)
	}

	// The gateway Charge ran twice (seed + replay) but NO additional refund —
	// and in particular no "comp-" compensation key — may have reached the
	// gateway.
	if len(gw.chargeKeys) != 2 {
		t.Errorf("expected 2 Charge calls (seed + replay), got %d", len(gw.chargeKeys))
	}
	if len(gw.refundKeys) != legitRefunds {
		t.Errorf("no additional gateway refund may fire on terminal-state replay; keys: %v", gw.refundKeys)
	}
	for _, k := range gw.refundKeys {
		if strings.HasPrefix(k, "comp-") {
			t.Errorf("gateway.Refund must NEVER be called with a comp- key on terminal-state replay, got %q", k)
		}
	}
}

// --- issue #241 item 2: zero-amount duplicate-key convergence after tx exit ---

// txPhaseRecordingTxManager wraps static repos and records whether a
// transaction closure is currently executing, so repos can observe WHEN they
// are called relative to the transaction boundary.
type txPhaseRecordingTxManager struct {
	repos tx.Repos
	mu    sync.Mutex
	inTx  bool
}

func (m *txPhaseRecordingTxManager) RunInTx(ctx context.Context, fn func(context.Context, tx.Repos) error) error {
	m.mu.Lock()
	m.inTx = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.inTx = false
		m.mu.Unlock()
	}()
	return fn(ctx, m.repos)
}

func (m *txPhaseRecordingTxManager) isInTx() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inTx
}

// zeroDupPaymentRepo rejects Save with the duplicate-key sentinel and returns
// the winner from FindByIdempotencyKey only after Save was attempted. Every
// lookup records whether it ran inside the transaction closure.
type zeroDupPaymentRepo struct {
	mockPaymentRepo
	txm           *txPhaseRecordingTxManager
	winner        *payment.Payment
	mu            sync.Mutex
	saveAttempted bool
	findInTx      []bool
}

func (r *zeroDupPaymentRepo) Save(_ context.Context, _ *payment.Payment) error {
	r.mu.Lock()
	r.saveAttempted = true
	r.mu.Unlock()
	return payment.ErrDuplicateIdempotencyKey
}

func (r *zeroDupPaymentRepo) FindByIdempotencyKey(_ context.Context, _ string) (*payment.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.findInTx = append(r.findInTx, r.txm.isInTx())
	if r.saveAttempted {
		return r.winner, nil
	}
	return nil, nil
}

func TestProcessPayment_ZeroAmount_DuplicateKey_ConvergesAfterTxExit(t *testing.T) {
	// Issue #241 item 2: the zero-amount settlement loser must return the
	// abort sentinel from inside the transaction and converge on the winner
	// only AFTER the tx has exited — converging (or committing) inside a
	// Postgres transaction aborted by the unique violation is undefined.
	clock := newPaymentTestClock()
	inv := newZeroAmountFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}

	winner, err := payment.NewPayment(
		shared.NewPaymentID(), inv.ID(), shared.Zero(shared.CurrencyJPY),
		payment.PaymentMethodCreditCard, "", clock.Now(),
	)
	if err != nil {
		t.Fatalf("NewPayment: %v", err)
	}
	if err := winner.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	winner.SetIdempotencyKey("idem-zero-post-tx")

	txm := &txPhaseRecordingTxManager{}
	repo := &zeroDupPaymentRepo{txm: txm, winner: winner}
	txm.repos = tx.Repos{Payments: repo, Invoices: invRepo}

	// failCharge gateway proves the zero settlement never touches the gateway.
	svc := NewPaymentService(
		&mockGateway{failCharge: true},
		repo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(txm),
	)

	p, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		Currency:       shared.CurrencyJPY,
		IdempotencyKey: "idem-zero-post-tx",
	})
	if err != nil {
		t.Fatalf("zero-amount raced loser must converge on the winner, got: %v", err)
	}
	if p == nil || p.ID() != winner.ID() {
		t.Fatalf("expected convergence on winner payment %s, got %+v", winner.ID(), p)
	}

	// Lookups: pre-charge (outside tx), in-tx idempotency check (inside tx),
	// winner convergence read (MUST be outside the tx — that is the fix).
	repo.mu.Lock()
	finds := append([]bool(nil), repo.findInTx...)
	repo.mu.Unlock()
	if len(finds) != 3 {
		t.Fatalf("expected 3 FindByIdempotencyKey calls (pre-charge, in-tx, post-tx convergence), got %d", len(finds))
	}
	if finds[1] != true {
		t.Errorf("second lookup (idempotency check) should run inside the tx")
	}
	if finds[2] != false {
		t.Errorf("winner convergence read must happen AFTER the transaction exits (issue #241 item 2), but ran inside the tx")
	}
}

func TestProcessPayment_ZeroAmount_DuplicateKey_WinnerNotVisible_ReturnsConflict(t *testing.T) {
	// When the winner is not yet visible on the fresh read, the zero-amount
	// loser must surface a retryable conflict — mirroring the gateway path.
	clock := newPaymentTestClock()
	inv := newZeroAmountFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}

	txm := &txPhaseRecordingTxManager{}
	repo := &zeroDupPaymentRepo{txm: txm, winner: nil} // winner never visible
	txm.repos = tx.Repos{Payments: repo, Invoices: invRepo}

	svc := NewPaymentService(
		&mockGateway{failCharge: true},
		repo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentTxManager(txm),
	)

	p, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		Currency:       shared.CurrencyJPY,
		IdempotencyKey: "idem-zero-no-winner",
	})
	if err == nil {
		t.Fatal("expected conflict when the winner is not visible")
	}
	assertDomainError(t, err, shared.ErrCodeConflict)
	if p != nil {
		t.Errorf("expected nil payment, got %+v", p)
	}
}
