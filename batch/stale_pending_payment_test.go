package batch

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// --- fixtures ---

var stalePendingTestNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// fakePendingReconciler is a scripted port.PendingPaymentReconciler.
type fakePendingReconciler struct {
	mu    sync.Mutex
	calls int
	// byID scripts a per-payment disposition; payments not present fall back
	// to defaultDisposition.
	byID               map[shared.PaymentID]port.PendingPaymentDisposition
	defaultDisposition port.PendingPaymentDisposition
	// errByID scripts a per-payment error.
	errByID map[shared.PaymentID]error
	// sawInvoice records, per call, whether a non-nil invoice was passed.
	sawInvoice []bool
}

func (f *fakePendingReconciler) ReconcilePendingPayment(_ context.Context, p *payment.Payment, inv *invoice.Invoice) (port.PendingPaymentDisposition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.sawInvoice = append(f.sawInvoice, inv != nil)
	if err, ok := f.errByID[p.ID()]; ok {
		return "", err
	}
	if d, ok := f.byID[p.ID()]; ok {
		return d, nil
	}
	return f.defaultDisposition, nil
}

func (f *fakePendingReconciler) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// paymentFailedSpyPlugin records OnPaymentFailed invocations.
type paymentFailedSpyPlugin struct {
	mu      sync.Mutex
	calls   int
	reasons []string
}

func (p *paymentFailedSpyPlugin) Name() string                                        { return "payment-failed-spy" }
func (p *paymentFailedSpyPlugin) Version() string                                     { return "1.0.0" }
func (p *paymentFailedSpyPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *paymentFailedSpyPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *paymentFailedSpyPlugin) Priority() int                                       { return 500 }
func (p *paymentFailedSpyPlugin) OnPaymentFailed(_ *plugin.PaymentContext, err error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.reasons = append(p.reasons, err.Error())
	return nil
}

func (p *paymentFailedSpyPlugin) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// lastReason returns the error message from the most recent OnPaymentFailed
// invocation ("" if none yet).
func (p *paymentFailedSpyPlugin) lastReason() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.reasons) == 0 {
		return ""
	}
	return p.reasons[len(p.reasons)-1]
}

// stalePendingEnv wires the processor against real in-memory repos and a real
// PaymentService, so the MarkFailed disposition exercises the genuine
// MarkPaymentFailed semantics (transition + post-commit OnPaymentFailed hooks).
type stalePendingEnv struct {
	clock       shared.FixedClock
	paymentRepo *inmemory.InMemoryPaymentRepository
	invoiceRepo *inmemory.InMemoryInvoiceRepository
	reconciler  *fakePendingReconciler
	failedSpy   *paymentFailedSpyPlugin
	svc         *service.PaymentService
}

func newStalePendingEnv(t *testing.T) *stalePendingEnv {
	t.Helper()
	clock := shared.FixedClock{FixedTime: stalePendingTestNow}
	paymentRepo := inmemory.NewInMemoryPaymentRepository()
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	failedSpy := &paymentFailedSpyPlugin{}
	registry := plugin.NewRegistry()
	if err := registry.Register(failedSpy); err != nil {
		t.Fatalf("register failedSpy: %v", err)
	}
	// The gateway and event store are never touched by MarkPaymentFailed.
	svc := service.NewPaymentService(
		nil, // gateway
		paymentRepo,
		invoiceRepo,
		nil, // contract repo
		nil, // event store
		registry,
		clock,
		service.WithoutPaymentTransactions(),
	)
	return &stalePendingEnv{
		clock:       clock,
		paymentRepo: paymentRepo,
		invoiceRepo: invoiceRepo,
		reconciler:  &fakePendingReconciler{defaultDisposition: port.PendingPaymentKeep},
		failedSpy:   failedSpy,
		svc:         svc,
	}
}

func (e *stalePendingEnv) newProcessor(t *testing.T, staleAfter time.Duration) *StalePendingPaymentProcessor {
	t.Helper()
	return NewStalePendingPaymentProcessor(
		e.paymentRepo, e.invoiceRepo, e.reconciler, e.svc, staleAfter, e.clock, nil,
	)
}

// seedPendingPayment persists a Pending payment (with a backing invoice)
// whose ProcessedAt is `age` before the fixed now.
func (e *stalePendingEnv) seedPendingPayment(t *testing.T, age time.Duration) *payment.Payment {
	t.Helper()
	amount := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
		amount, shared.Zero(shared.CurrencyJPY), shared.Zero(shared.CurrencyJPY),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}
	if err := inv.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := e.invoiceRepo.Save(context.Background(), inv); err != nil {
		t.Fatalf("save invoice: %v", err)
	}
	p, err := payment.NewPayment(
		shared.NewPaymentID(), inv.ID(), amount,
		payment.PaymentMethodCreditCard, "gw-txn-stale", e.clock.Now().Add(-age),
	)
	if err != nil {
		t.Fatalf("NewPayment: %v", err)
	}
	if err := e.paymentRepo.Save(context.Background(), p); err != nil {
		t.Fatalf("save payment: %v", err)
	}
	return p
}

func (e *stalePendingEnv) paymentStatus(t *testing.T, id shared.PaymentID) payment.PaymentStatus {
	t.Helper()
	p, err := e.paymentRepo.FindByID(context.Background(), id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	return p.Status()
}

// --- tests ---

func TestStalePendingPaymentProcessor_Orphan_MarkedFailedViaService(t *testing.T) {
	env := newStalePendingEnv(t)
	orphan := env.seedPendingPayment(t, 48*time.Hour)
	fresh := env.seedPendingPayment(t, time.Hour) // younger than staleAfter → never scanned
	env.reconciler.byID = map[shared.PaymentID]port.PendingPaymentDisposition{
		orphan.ID(): port.PendingPaymentMarkFailed,
	}

	proc := env.newProcessor(t, 24*time.Hour)
	result, err := proc.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result.Total != 1 || result.Succeeded != 1 || result.Failed != 0 || result.Skipped != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}

	// The orphan transitioned through PaymentService.MarkPaymentFailed.
	if got := env.paymentStatus(t, orphan.ID()); got != payment.PaymentStatusFailed {
		t.Errorf("orphan status = %s, want failed", got)
	}
	loaded, err := env.paymentRepo.FindByID(context.Background(), orphan.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.FailureReason() == nil || !strings.Contains(*loaded.FailureReason(), StalePendingFailureReason) {
		t.Errorf("failure reason must equal the exported StalePendingFailureReason, got %v", loaded.FailureReason())
	}
	// The fresh pending payment was never scanned or touched.
	if got := env.paymentStatus(t, fresh.ID()); got != payment.PaymentStatusPending {
		t.Errorf("fresh payment status = %s, want pending", got)
	}
	// OnPaymentFailed fired exactly once — via the service's real-transition
	// path, not from the batch.
	if got := env.failedSpy.callCount(); got != 1 {
		t.Errorf("OnPaymentFailed calls = %d, want 1", got)
	}
	// The reason string reaching the OnPaymentFailedHook (via
	// PaymentService.MarkPaymentFailed's error message) must contain the
	// exported StalePendingFailureReason constant, so integrator hook
	// implementations can filter batch-cleanup failures out of dunning/paging
	// without substring-matching an unexported sentence.
	if got := env.failedSpy.lastReason(); !strings.Contains(got, StalePendingFailureReason) {
		t.Errorf("OnPaymentFailed reason must contain StalePendingFailureReason, got %q", got)
	}
	// The reconciler received the cheaply-loaded invoice.
	if len(env.reconciler.sawInvoice) != 1 || !env.reconciler.sawInvoice[0] {
		t.Errorf("reconciler must receive the payment's invoice when invoiceRepo is wired, saw %v", env.reconciler.sawInvoice)
	}
}

func TestStalePendingPaymentProcessor_Keep_CountsAsSkipped(t *testing.T) {
	env := newStalePendingEnv(t)
	inFlight := env.seedPendingPayment(t, 48*time.Hour)
	env.reconciler.defaultDisposition = port.PendingPaymentKeep

	proc := env.newProcessor(t, 24*time.Hour)
	result, err := proc.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result.Total != 1 || result.Succeeded != 0 || result.Failed != 0 || result.Skipped != 1 {
		t.Fatalf("Keep must count as Skipped, got %+v", result)
	}
	if got := env.paymentStatus(t, inFlight.ID()); got != payment.PaymentStatusPending {
		t.Errorf("kept payment status = %s, want pending", got)
	}
	if got := env.failedSpy.callCount(); got != 0 {
		t.Errorf("no hook may fire for a kept payment, got %d calls", got)
	}
}

func TestStalePendingPaymentProcessor_NilInvoiceRepo_ReconcilerGetsNilInvoice(t *testing.T) {
	env := newStalePendingEnv(t)
	env.seedPendingPayment(t, 48*time.Hour)

	proc := NewStalePendingPaymentProcessor(
		env.paymentRepo, nil /* no invoice repo */, env.reconciler, env.svc, 24*time.Hour, env.clock, nil,
	)
	if _, err := proc.Process(context.Background(), BatchOptions{}); err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if len(env.reconciler.sawInvoice) != 1 || env.reconciler.sawInvoice[0] {
		t.Errorf("reconciler must receive a nil invoice without an invoice repo, saw %v", env.reconciler.sawInvoice)
	}
}

func TestStalePendingPaymentProcessor_ReconcilerError_FailsItem(t *testing.T) {
	env := newStalePendingEnv(t)
	// Two stale payments; the OLDER one errors in the reconciler. FindStalePending
	// returns oldest-first, so with ContinueOnError=true the second is still
	// processed (Keep → Skipped).
	older := env.seedPendingPayment(t, 72*time.Hour)
	env.seedPendingPayment(t, 48*time.Hour)
	env.reconciler.errByID = map[shared.PaymentID]error{
		older.ID(): errors.New("gateway reconciliation API unavailable"),
	}

	proc := env.newProcessor(t, 24*time.Hour)
	result, err := proc.Process(context.Background(), BatchOptions{ContinueOnError: true})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result.Total != 2 || result.Succeeded != 0 || result.Failed != 1 || result.Skipped != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Error(), "reconciliation failed") {
		t.Errorf("expected one reconciliation error, got %v", result.Errors)
	}
	// The errored payment was not touched.
	if got := env.paymentStatus(t, older.ID()); got != payment.PaymentStatusPending {
		t.Errorf("errored payment status = %s, want pending", got)
	}
}

func TestStalePendingPaymentProcessor_ReconcilerError_EarlyStopSkipsRemainder(t *testing.T) {
	env := newStalePendingEnv(t)
	older := env.seedPendingPayment(t, 72*time.Hour)
	env.seedPendingPayment(t, 48*time.Hour)
	env.reconciler.errByID = map[shared.PaymentID]error{
		older.ID(): errors.New("boom"),
	}

	proc := env.newProcessor(t, 24*time.Hour)
	result, err := proc.Process(context.Background(), BatchOptions{ContinueOnError: false})
	if err != nil {
		t.Fatalf("Process must not surface an item error as the run error, got: %v", err)
	}
	// Accounting invariant: Total == Succeeded + Failed + Skipped (issue #242).
	if result.Total != 2 || result.Failed != 1 || result.Skipped != 1 || result.Succeeded != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestStalePendingPaymentProcessor_UnknownDisposition_FailsItem(t *testing.T) {
	env := newStalePendingEnv(t)
	p := env.seedPendingPayment(t, 48*time.Hour)
	env.reconciler.byID = map[shared.PaymentID]port.PendingPaymentDisposition{
		p.ID(): port.PendingPaymentDisposition("retry_later"),
	}

	proc := env.newProcessor(t, 24*time.Hour)
	result, err := proc.Process(context.Background(), BatchOptions{ContinueOnError: true})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result.Failed != 1 {
		t.Fatalf("unknown disposition must fail the item, got %+v", result)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Error(), "unknown disposition") {
		t.Errorf("expected unknown-disposition error, got %v", result.Errors)
	}
}

func TestStalePendingPaymentProcessor_DryRun_NoWritesReportsActions(t *testing.T) {
	env := newStalePendingEnv(t)
	orphan := env.seedPendingPayment(t, 48*time.Hour)
	kept := env.seedPendingPayment(t, 36*time.Hour)
	env.reconciler.byID = map[shared.PaymentID]port.PendingPaymentDisposition{
		orphan.ID(): port.PendingPaymentMarkFailed,
		kept.ID():   port.PendingPaymentKeep,
	}

	proc := env.newProcessor(t, 24*time.Hour)
	result, err := proc.Process(context.Background(), BatchOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result.Total != 2 || result.Succeeded != 1 || result.Skipped != 1 || result.Failed != 0 {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}
	if len(result.DryRunActions) != 1 {
		t.Fatalf("expected 1 dry-run action, got %v", result.DryRunActions)
	}
	if result.DryRunActions[0].ItemID != string(orphan.ID()) || result.DryRunActions[0].Action != StalePendingActionMarkFailed {
		t.Errorf("unexpected dry-run action: %+v", result.DryRunActions[0])
	}
	// No writes and no hooks in a dry run.
	if got := env.paymentStatus(t, orphan.ID()); got != payment.PaymentStatusPending {
		t.Errorf("dry run must not transition the payment, got status %s", got)
	}
	if got := env.failedSpy.callCount(); got != 0 {
		t.Errorf("dry run must not fire hooks, got %d calls", got)
	}
	// The reconciler IS consulted in a dry run (it is read-only by contract).
	if got := env.reconciler.callCount(); got != 2 {
		t.Errorf("reconciler calls = %d, want 2", got)
	}
}

func TestStalePendingPaymentProcessor_RerunIsIdempotent(t *testing.T) {
	env := newStalePendingEnv(t)
	orphan := env.seedPendingPayment(t, 48*time.Hour)
	env.reconciler.byID = map[shared.PaymentID]port.PendingPaymentDisposition{
		orphan.ID(): port.PendingPaymentMarkFailed,
	}
	proc := env.newProcessor(t, 24*time.Hour)

	first, err := proc.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	if first.Succeeded != 1 {
		t.Fatalf("first run: %+v", first)
	}

	// A failed record is no longer Pending, so the second run selects nothing
	// and fires nothing.
	second, err := proc.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if second.Total != 0 || second.Succeeded != 0 || second.Failed != 0 || second.Skipped != 0 {
		t.Fatalf("re-run must be a no-op, got %+v", second)
	}
	if got := env.failedSpy.callCount(); got != 1 {
		t.Errorf("OnPaymentFailed must fire exactly once across re-runs, got %d", got)
	}
}

// invalidStateFailer simulates a payment that left Pending between the scan
// and the transition (e.g. a late settlement webhook completed it):
// MarkPaymentFailed rejects with invalid_state_transition.
type invalidStateFailer struct{ calls int }

func (f *invalidStateFailer) MarkPaymentFailed(_ context.Context, id shared.PaymentID, _ string) (*payment.Payment, error) {
	f.calls++
	return nil, shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
		fmt.Sprintf("cannot mark payment %s as failed: current status is completed", id))
}

func TestStalePendingPaymentProcessor_RacedSettlement_SkippedNotFailed(t *testing.T) {
	env := newStalePendingEnv(t)
	p := env.seedPendingPayment(t, 48*time.Hour)
	env.reconciler.byID = map[shared.PaymentID]port.PendingPaymentDisposition{
		p.ID(): port.PendingPaymentMarkFailed,
	}
	failer := &invalidStateFailer{}
	proc := NewStalePendingPaymentProcessor(
		env.paymentRepo, env.invoiceRepo, env.reconciler, failer, 24*time.Hour, env.clock, nil,
	)

	result, err := proc.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	// invalid_state_transition is the terminal-state guard doing its job (the
	// reconciler's verdict went stale), not a batch failure.
	if result.Total != 1 || result.Skipped != 1 || result.Failed != 0 || result.Succeeded != 0 {
		t.Fatalf("raced settlement must be Skipped, got %+v", result)
	}
	if failer.calls != 1 {
		t.Errorf("MarkPaymentFailed calls = %d, want 1", failer.calls)
	}
}

func TestStalePendingPaymentProcessor_Concurrent_MatchesSequential(t *testing.T) {
	env := newStalePendingEnv(t)
	var orphans []shared.PaymentID
	for i := 0; i < 5; i++ {
		p := env.seedPendingPayment(t, 48*time.Hour+time.Duration(i)*time.Hour)
		orphans = append(orphans, p.ID())
	}
	kept := env.seedPendingPayment(t, 30*time.Hour)
	env.reconciler.byID = map[shared.PaymentID]port.PendingPaymentDisposition{}
	for _, id := range orphans {
		env.reconciler.byID[id] = port.PendingPaymentMarkFailed
	}
	env.reconciler.byID[kept.ID()] = port.PendingPaymentKeep

	proc := env.newProcessor(t, 24*time.Hour)
	result, err := proc.Process(context.Background(), BatchOptions{Concurrency: 3, ContinueOnError: true})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result.Total != 6 || result.Succeeded != 5 || result.Skipped != 1 || result.Failed != 0 {
		t.Fatalf("unexpected concurrent result: %+v", result)
	}
	for _, id := range orphans {
		if got := env.paymentStatus(t, id); got != payment.PaymentStatusFailed {
			t.Errorf("orphan %s status = %s, want failed", id, got)
		}
	}
	if got := env.paymentStatus(t, kept.ID()); got != payment.PaymentStatusPending {
		t.Errorf("kept payment status = %s, want pending", got)
	}
	if got := env.failedSpy.callCount(); got != 5 {
		t.Errorf("OnPaymentFailed calls = %d, want 5", got)
	}
}

func TestStalePendingPaymentProcessor_LimitBoundsScan(t *testing.T) {
	env := newStalePendingEnv(t)
	oldest := env.seedPendingPayment(t, 72*time.Hour)
	env.seedPendingPayment(t, 48*time.Hour)
	env.reconciler.defaultDisposition = port.PendingPaymentMarkFailed

	proc := env.newProcessor(t, 24*time.Hour)
	result, err := proc.Process(context.Background(), BatchOptions{Limit: 1})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result.Total != 1 || result.Succeeded != 1 {
		t.Fatalf("Limit must bound the scan, got %+v", result)
	}
	// Oldest-first draining: the oldest orphan is the one processed.
	if got := env.paymentStatus(t, oldest.ID()); got != payment.PaymentStatusFailed {
		t.Errorf("oldest orphan status = %s, want failed", got)
	}
}
