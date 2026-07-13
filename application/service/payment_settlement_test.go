package service

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// --- Counting hook spies (settlement tests need call COUNTS, not booleans,
// to assert that idempotent replays do not re-fire hooks) ---

type countingPaymentHooksPlugin struct {
	beforeCharge     int
	afterCharge      int
	paymentProcessed int
	paymentFailed    int
	lastFailedErr    error
}

func (p *countingPaymentHooksPlugin) Name() string                                        { return "counting-payment-hooks" }
func (p *countingPaymentHooksPlugin) Version() string                                     { return "1.0.0" }
func (p *countingPaymentHooksPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *countingPaymentHooksPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *countingPaymentHooksPlugin) Priority() int                                       { return 500 }
func (p *countingPaymentHooksPlugin) BeforeCharge(_ *plugin.PaymentContext, _ shared.Money) error {
	p.beforeCharge++
	return nil
}
func (p *countingPaymentHooksPlugin) AfterCharge(_ *plugin.PaymentContext) error {
	p.afterCharge++
	return nil
}
func (p *countingPaymentHooksPlugin) OnPaymentProcessed(_ *plugin.PaymentContext) error {
	p.paymentProcessed++
	return nil
}
func (p *countingPaymentHooksPlugin) OnPaymentFailed(_ *plugin.PaymentContext, err error) error {
	p.paymentFailed++
	p.lastFailedErr = err
	return nil
}

// settlementFixture wires a PaymentService onto in-memory repositories with a
// seeded finalized invoice, so settlement tests exercise real FindByID /
// optimistic-locking semantics instead of the single-slot mocks.
type settlementFixture struct {
	svc     *PaymentService
	payRepo *inmemory.InMemoryPaymentRepository
	invRepo *inmemory.InMemoryInvoiceRepository
	writer  *inmemory.InMemoryOutboxWriter
	hooks   *countingPaymentHooksPlugin
	inv     *invoice.Invoice
	clock   shared.FixedClock
}

func newSettlementFixture(t *testing.T, gw port.PaymentGateway) *settlementFixture {
	t.Helper()
	clock := newPaymentTestClock()
	payRepo := inmemory.NewInMemoryPaymentRepository()
	invRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	writer := inmemory.NewInMemoryOutboxWriter()
	hooks := &countingPaymentHooksPlugin{}

	registry := plugin.NewRegistry()
	if err := registry.Register(hooks); err != nil {
		t.Fatalf("register hooks: %v", err)
	}

	inv := newSimpleFinalizedInvoice()
	if err := invRepo.Save(context.Background(), inv); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	svc := NewPaymentService(
		gw, payRepo, invRepo, nil, &mockEventStore{}, registry, clock,
		WithPaymentOutboxWriter(writer),
		WithoutPaymentTransactions(),
	)
	return &settlementFixture{
		svc: svc, payRepo: payRepo, invRepo: invRepo,
		writer: writer, hooks: hooks, inv: inv, clock: clock,
	}
}

// processPending drives ProcessPayment against an async-pending gateway and
// returns the persisted Pending payment.
func (f *settlementFixture) processPending(t *testing.T, key string) *payment.Payment {
	t.Helper()
	pmt, err := f.svc.ProcessPayment(context.Background(), f.inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  key,
	})
	if !errors.Is(err, ErrPaymentPending) {
		t.Fatalf("expected ErrPaymentPending, got: %v", err)
	}
	if pmt == nil {
		t.Fatal("expected pending payment, got nil")
	}
	return pmt
}

// --- ProcessPayment: async pending outcome ---

func TestProcessPayment_PendingStatus_ReturnsErrPaymentPending(t *testing.T) {
	gw := &mockGateway{
		chargeStatus:            port.TransactionStatusPending,
		chargePaymentMethodType: port.PaymentMethodTypeBankTransfer,
	}
	f := newSettlementFixture(t, gw)

	pmt := f.processPending(t, "key-async-pending")

	if pmt.Status() != payment.PaymentStatusPending {
		t.Errorf("expected pending status, got %q", pmt.Status())
	}
	if pmt.Method() != payment.PaymentMethodBankTransfer {
		t.Errorf("expected bank_transfer method resolved from ChargeResponse, got %q", pmt.Method())
	}
	if pmt.GatewayTransactionID() == "" {
		t.Error("expected gateway transaction ID to be recorded on the pending payment")
	}
	if pmt.IdempotencyKey() != "key-async-pending" {
		t.Errorf("expected idempotency key on pending payment, got %q", pmt.IdempotencyKey())
	}

	// The pending payment must be persisted for the later settlement.
	stored, err := f.payRepo.FindByIdempotencyKey(context.Background(), "key-async-pending")
	if err != nil || stored == nil {
		t.Fatalf("expected pending payment persisted, got (%v, %v)", stored, err)
	}
	if stored.Status() != payment.PaymentStatusPending {
		t.Errorf("persisted payment should be pending, got %q", stored.Status())
	}

	// The invoice must NOT be marked paid.
	reloaded, err := f.invRepo.FindByID(context.Background(), f.inv.ID())
	if err != nil {
		t.Fatalf("reload invoice: %v", err)
	}
	if !reloaded.PaidAmount().IsZero() {
		t.Errorf("invoice must not record payment on pending outcome, paidAmount=%v", reloaded.PaidAmount().Amount())
	}

	// BeforeCharge fired (the gateway WAS charged); AfterCharge/OnPaymentProcessed
	// did not (nothing settled); no outbox row (no new completed state persisted
	// under the payment+invoice pair contract — the settlement fires it later).
	if f.hooks.beforeCharge != 1 {
		t.Errorf("expected BeforeCharge to fire once, got %d", f.hooks.beforeCharge)
	}
	if f.hooks.afterCharge != 0 || f.hooks.paymentProcessed != 0 {
		t.Errorf("AfterCharge/OnPaymentProcessed must not fire on pending outcome (got %d/%d)",
			f.hooks.afterCharge, f.hooks.paymentProcessed)
	}
	if f.writer.PaymentCount() != 0 {
		t.Errorf("outbox writer must not fire on pending outcome, got %d", f.writer.PaymentCount())
	}
}

func TestProcessPayment_PendingStatus_IdempotentReplay_ReturnsExistingPayment(t *testing.T) {
	gw := &mockGateway{chargeStatus: port.TransactionStatusPending}
	f := newSettlementFixture(t, gw)

	first := f.processPending(t, "key-async-replay")
	second := f.processPending(t, "key-async-replay")

	if first.ID() != second.ID() {
		t.Errorf("replay must return the existing pending payment: %s vs %s", first.ID(), second.ID())
	}
}

func TestProcessPayment_SynchronousCardCharge_Unaffected(t *testing.T) {
	// Regression: the async-pending branch must not disturb the plain
	// captured-card success path.
	f := newSettlementFixture(t, &mockGateway{}) // default: Captured

	pmt, err := f.svc.ProcessPayment(context.Background(), f.inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-sync-card",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pmt.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected completed, got %q", pmt.Status())
	}
	reloaded, _ := f.invRepo.FindByID(context.Background(), f.inv.ID())
	if reloaded.Status() != invoice.InvoiceStatusPaid {
		t.Errorf("expected invoice paid, got %q", reloaded.Status())
	}
	if f.hooks.afterCharge != 1 || f.hooks.paymentProcessed != 1 {
		t.Errorf("expected success hooks once each, got AfterCharge=%d OnPaymentProcessed=%d",
			f.hooks.afterCharge, f.hooks.paymentProcessed)
	}
	if f.writer.PaymentCount() != 1 {
		t.Errorf("expected one outbox row, got %d", f.writer.PaymentCount())
	}
}

// --- SettlePayment ---

func TestSettlePayment_CompletesPendingAndRecordsInvoice(t *testing.T) {
	gw := &mockGateway{
		chargeStatus:            port.TransactionStatusPending,
		chargePaymentMethodType: port.PaymentMethodTypeConvenienceStore,
	}
	f := newSettlementFixture(t, gw)
	pending := f.processPending(t, "key-settle-happy")

	settled, err := f.svc.SettlePayment(context.Background(), pending.ID())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if settled.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected completed, got %q", settled.Status())
	}

	// Payment persisted as completed.
	stored, err := f.payRepo.FindByID(context.Background(), pending.ID())
	if err != nil {
		t.Fatalf("reload payment: %v", err)
	}
	if stored.Status() != payment.PaymentStatusCompleted {
		t.Errorf("persisted payment should be completed, got %q", stored.Status())
	}

	// Invoice marked paid with the full amount.
	reloaded, err := f.invRepo.FindByID(context.Background(), f.inv.ID())
	if err != nil {
		t.Fatalf("reload invoice: %v", err)
	}
	if reloaded.PaidAmount().Amount().Cmp(big.NewRat(10000, 1)) != 0 {
		t.Errorf("expected paid amount 10000, got %s", reloaded.PaidAmount().Amount().RatString())
	}
	if reloaded.Status() != invoice.InvoiceStatusPaid {
		t.Errorf("expected invoice status paid, got %q", reloaded.Status())
	}

	// Post-commit hooks fired once; outbox row written inside the tx.
	if f.hooks.afterCharge != 1 {
		t.Errorf("expected AfterCharge once, got %d", f.hooks.afterCharge)
	}
	if f.hooks.paymentProcessed != 1 {
		t.Errorf("expected OnPaymentProcessed once, got %d", f.hooks.paymentProcessed)
	}
	if f.writer.PaymentCount() != 1 {
		t.Fatalf("expected one outbox row from settlement, got %d", f.writer.PaymentCount())
	}
	entry := f.writer.PaymentEntries()[0]
	if entry.Payment == nil || entry.Payment.ID() != pending.ID() {
		t.Errorf("outbox entry has wrong payment: %+v", entry.Payment)
	}
	if entry.Payment.Status() != payment.PaymentStatusCompleted {
		t.Errorf("outbox entry payment should be completed, got %q", entry.Payment.Status())
	}
	if entry.Invoice == nil || entry.Invoice.ID() != f.inv.ID() {
		t.Errorf("outbox entry has wrong invoice: %+v", entry.Invoice)
	}
}

func TestSettlePayment_IdempotentReplay_IsNoOp(t *testing.T) {
	gw := &mockGateway{chargeStatus: port.TransactionStatusPending}
	f := newSettlementFixture(t, gw)
	pending := f.processPending(t, "key-settle-replay")

	if _, err := f.svc.SettlePayment(context.Background(), pending.ID()); err != nil {
		t.Fatalf("first settle: %v", err)
	}
	replayed, err := f.svc.SettlePayment(context.Background(), pending.ID())
	if err != nil {
		t.Fatalf("replay settle must be a no-op success, got: %v", err)
	}
	if replayed.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected completed on replay, got %q", replayed.Status())
	}

	// Exactly one settlement: invoice paid once, hooks/outbox once.
	reloaded, _ := f.invRepo.FindByID(context.Background(), f.inv.ID())
	if reloaded.PaidAmount().Amount().Cmp(big.NewRat(10000, 1)) != 0 {
		t.Errorf("replay must not double-record; paid=%s", reloaded.PaidAmount().Amount().RatString())
	}
	if f.hooks.afterCharge != 1 || f.hooks.paymentProcessed != 1 {
		t.Errorf("replay must not re-fire hooks (AfterCharge=%d OnPaymentProcessed=%d)",
			f.hooks.afterCharge, f.hooks.paymentProcessed)
	}
	if f.writer.PaymentCount() != 1 {
		t.Errorf("replay must not write another outbox row, got %d", f.writer.PaymentCount())
	}
}

func TestSettlePayment_TerminalState_ReturnsInvalidStateTransition(t *testing.T) {
	gw := &mockGateway{chargeStatus: port.TransactionStatusPending}
	f := newSettlementFixture(t, gw)
	pending := f.processPending(t, "key-settle-terminal")

	// Move the payment to a terminal failed state first.
	if _, err := f.svc.MarkPaymentFailed(context.Background(), pending.ID(), "instruction expired"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	_, err := f.svc.SettlePayment(context.Background(), pending.ID())
	if err == nil {
		t.Fatal("expected error settling a failed payment")
	}
	var de *shared.DomainError
	if !errors.As(err, &de) || de.Code != shared.ErrCodeInvalidStateTransition {
		t.Errorf("expected invalid_state_transition domain error, got: %v", err)
	}

	// The invoice stays untouched.
	reloaded, _ := f.invRepo.FindByID(context.Background(), f.inv.ID())
	if !reloaded.PaidAmount().IsZero() {
		t.Errorf("invoice must remain unpaid, paid=%s", reloaded.PaidAmount().Amount().RatString())
	}
}

func TestSettlePayment_NotFound_ReturnsError(t *testing.T) {
	f := newSettlementFixture(t, &mockGateway{})
	_, err := f.svc.SettlePayment(context.Background(), shared.NewPaymentID())
	if err == nil {
		t.Fatal("expected error for unknown payment ID")
	}
}

// --- MarkPaymentFailed ---

func TestMarkPaymentFailed_TransitionsPendingToFailed(t *testing.T) {
	gw := &mockGateway{chargeStatus: port.TransactionStatusPending}
	f := newSettlementFixture(t, gw)
	pending := f.processPending(t, "key-fail-pending")

	failed, err := f.svc.MarkPaymentFailed(context.Background(), pending.ID(), "konbini slip expired")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if failed.Status() != payment.PaymentStatusFailed {
		t.Errorf("expected failed, got %q", failed.Status())
	}
	if reason := failed.FailureReason(); reason == nil || *reason != "konbini slip expired" {
		t.Errorf("expected failure reason recorded, got %v", reason)
	}

	// Persisted as failed.
	stored, err := f.payRepo.FindByID(context.Background(), pending.ID())
	if err != nil {
		t.Fatalf("reload payment: %v", err)
	}
	if stored.Status() != payment.PaymentStatusFailed {
		t.Errorf("persisted payment should be failed, got %q", stored.Status())
	}

	// The invoice is untouched.
	reloaded, _ := f.invRepo.FindByID(context.Background(), f.inv.ID())
	if !reloaded.PaidAmount().IsZero() {
		t.Errorf("invoice must remain unpaid, paid=%s", reloaded.PaidAmount().Amount().RatString())
	}
	if reloaded.Status() != invoice.InvoiceStatusFinalized {
		t.Errorf("invoice status must stay finalized, got %q", reloaded.Status())
	}

	// OnPaymentFailed fired once with the reason; no success hooks, no outbox.
	if f.hooks.paymentFailed != 1 {
		t.Errorf("expected OnPaymentFailed once, got %d", f.hooks.paymentFailed)
	}
	if f.hooks.lastFailedErr == nil || !strings.Contains(f.hooks.lastFailedErr.Error(), "konbini slip expired") {
		t.Errorf("expected hook error to carry the reason, got: %v", f.hooks.lastFailedErr)
	}
	if f.hooks.afterCharge != 0 || f.hooks.paymentProcessed != 0 {
		t.Errorf("success hooks must not fire on failure marking (AfterCharge=%d OnPaymentProcessed=%d)",
			f.hooks.afterCharge, f.hooks.paymentProcessed)
	}
	if f.writer.PaymentCount() != 0 {
		t.Errorf("outbox writer must not fire on failure marking, got %d", f.writer.PaymentCount())
	}
}

func TestMarkPaymentFailed_IdempotentReplay_IsNoOp(t *testing.T) {
	gw := &mockGateway{chargeStatus: port.TransactionStatusPending}
	f := newSettlementFixture(t, gw)
	pending := f.processPending(t, "key-fail-replay")

	if _, err := f.svc.MarkPaymentFailed(context.Background(), pending.ID(), "expired"); err != nil {
		t.Fatalf("first mark: %v", err)
	}
	replayed, err := f.svc.MarkPaymentFailed(context.Background(), pending.ID(), "expired again")
	if err != nil {
		t.Fatalf("replay must be a no-op success, got: %v", err)
	}
	if replayed.Status() != payment.PaymentStatusFailed {
		t.Errorf("expected failed on replay, got %q", replayed.Status())
	}
	// Original reason kept; hooks fired only once.
	if reason := replayed.FailureReason(); reason == nil || *reason != "expired" {
		t.Errorf("replay must not overwrite the stored failure reason, got %v", reason)
	}
	if f.hooks.paymentFailed != 1 {
		t.Errorf("replay must not re-fire OnPaymentFailed, got %d", f.hooks.paymentFailed)
	}
}

func TestMarkPaymentFailed_CompletedPayment_ReturnsInvalidStateTransition(t *testing.T) {
	gw := &mockGateway{chargeStatus: port.TransactionStatusPending}
	f := newSettlementFixture(t, gw)
	pending := f.processPending(t, "key-fail-completed")

	if _, err := f.svc.SettlePayment(context.Background(), pending.ID()); err != nil {
		t.Fatalf("settle: %v", err)
	}

	// A late expiry notification must not knock a settled payment back to failed.
	_, err := f.svc.MarkPaymentFailed(context.Background(), pending.ID(), "late expiry webhook")
	if err == nil {
		t.Fatal("expected error failing a completed payment")
	}
	var de *shared.DomainError
	if !errors.As(err, &de) || de.Code != shared.ErrCodeInvalidStateTransition {
		t.Errorf("expected invalid_state_transition domain error, got: %v", err)
	}

	stored, _ := f.payRepo.FindByID(context.Background(), pending.ID())
	if stored.Status() != payment.PaymentStatusCompleted {
		t.Errorf("payment must remain completed, got %q", stored.Status())
	}
}

// TestSettlePayment_AfterPendingProcessRetryCompletes verifies the interplay
// between the async pending outcome and the pre-existing in-tx Pending→Completed
// upgrade path: a ProcessPayment retry with the same key whose gateway charge
// now replays as Captured upgrades the SAME pending record — after which a
// (redundant) webhook-driven SettlePayment converges on the idempotent no-op.
func TestSettlePayment_AfterPendingProcessRetryCompletes(t *testing.T) {
	gw := &mockGateway{chargeStatus: port.TransactionStatusPending}
	f := newSettlementFixture(t, gw)
	pending := f.processPending(t, "key-retry-upgrade")

	// The gateway now reports the transaction captured on the retry.
	gw.chargeStatus = port.TransactionStatusCaptured
	upgraded, err := f.svc.ProcessPayment(context.Background(), f.inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-retry-upgrade",
	})
	if err != nil {
		t.Fatalf("retry should upgrade the pending payment, got: %v", err)
	}
	if upgraded.ID() != pending.ID() {
		t.Errorf("retry must upgrade the SAME pending record: %s vs %s", upgraded.ID(), pending.ID())
	}
	if upgraded.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected completed after upgrade, got %q", upgraded.Status())
	}

	// A racing webhook settlement is now an idempotent no-op.
	settled, err := f.svc.SettlePayment(context.Background(), pending.ID())
	if err != nil {
		t.Fatalf("redundant settle must be a no-op success, got: %v", err)
	}
	if settled.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected completed, got %q", settled.Status())
	}
	reloaded, _ := f.invRepo.FindByID(context.Background(), f.inv.ID())
	if reloaded.PaidAmount().Amount().Cmp(big.NewRat(10000, 1)) != 0 {
		t.Errorf("invoice must be paid exactly once, paid=%s", reloaded.PaidAmount().Amount().RatString())
	}
}
