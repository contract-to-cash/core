package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// passthroughTxManager delegates every RunInTx call to the closure with a
// fixed repo set (no rollback semantics — sufficient here because the test
// asserts on returned errors and hook/gateway side effects, not persistence).
type passthroughTxManager struct {
	repos tx.Repos
}

func (m *passthroughTxManager) RunInTx(ctx context.Context, fn func(context.Context, tx.Repos) error) error {
	return fn(ctx, m.repos)
}

// unknownStatusDupKeyRepo simulates the duplicate-key race whose winner
// carries a PaymentStatus this binary does not recognize. It embeds a real
// payment.Repository (the in-memory implementation) so it stays
// forward-compatible when the Repository interface grows, and overrides only
// the two methods the convergence path exercises:
//
//   - Save always returns ErrDuplicateIdempotencyKey (the loser's unique
//     violation);
//   - FindByIdempotencyKey returns nil until Save was attempted (so the
//     pre-charge and in-tx idempotency checks see no winner) and the
//     unknown-status winner afterwards (the post-tx convergence read).
type unknownStatusDupKeyRepo struct {
	payment.Repository
	winner *payment.Payment

	mu            sync.Mutex
	saveAttempted bool
}

func (r *unknownStatusDupKeyRepo) Save(_ context.Context, _ *payment.Payment) error {
	r.mu.Lock()
	r.saveAttempted = true
	r.mu.Unlock()
	return payment.ErrDuplicateIdempotencyKey
}

func (r *unknownStatusDupKeyRepo) FindByIdempotencyKey(_ context.Context, _ string) (*payment.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveAttempted {
		return r.winner, nil
	}
	return nil, nil
}

// TestProcessPayment_DuplicateKey_UnknownStatusWinner_FailsClosed_Integration
// pins the fail-closed fallback of convergeOnDuplicateKeyWinner: when the
// duplicate-key race loser converges on a winner whose persisted status is a
// value this binary does not recognize (e.g. a snapshot written by a newer
// version during a rolling upgrade), the loser must NOT converge it as
// success. It must return (nil, ErrCodeConflict), fire no success hooks, and
// fire no saga compensation.
//
// The unknown-status winner is rehydrated via payment.FromSnapshot, which is
// the only construction path that can carry an out-of-vocabulary status (the
// domain constructors and state transitions cannot mint one) — exactly the
// persistence-adapter scenario the fallback defends against. FromSnapshot is
// permitted in tests/integration/ by the forbidigo policy (issue #100).
func TestProcessPayment_DuplicateKey_UnknownStatusWinner_FailsClosed_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	amount := moneyJPY(6600)
	inv := newIntegrationInvoice(t, ctx, invoiceRepo, amount)

	const key = "integration-unknown-status-winner"

	winner, err := payment.FromSnapshot(payment.PaymentSnapshot{
		ID:                   shared.NewPaymentID(),
		InvoiceID:            inv.ID(),
		Amount:               amount,
		RefundedAmount:       shared.Zero(shared.CurrencyJPY),
		Method:               payment.PaymentMethodCreditCard,
		Status:               payment.PaymentStatus("disputed_v99"), // future status unknown to this binary
		GatewayTransactionID: "txn-unknown-status-winner",
		IdempotencyKey:       key,
		ProcessedAt:          time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		Version:              1,
	})
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}

	repo := &unknownStatusDupKeyRepo{
		Repository: inmemory.NewInMemoryPaymentRepository(),
		winner:     winner,
	}
	txm := &passthroughTxManager{repos: tx.Repos{Payments: repo, Invoices: invoiceRepo}}

	gw := &integrationGateway{} // Charge captures; Refund calls are recorded
	recorder := &recordingAfterChargePlugin{}
	reg := plugin.NewRegistry()
	if regErr := reg.Register(recorder); regErr != nil {
		t.Fatalf("registry.Register: %v", regErr)
	}

	svc := service.NewPaymentService(
		gw,
		repo,
		invoiceRepo,
		nil,
		nil,
		reg,
		clock,
		service.WithPaymentTxManager(txm),
	)

	p, err := svc.ProcessPayment(ctx, inv.ID(), service.ProcessPaymentInput{
		PaymentMethodID: "pm-unknown-status",
		Amount:          amount,
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  key,
	})

	if err == nil {
		t.Fatal("converging on an unknown-status winner must fail closed, got nil error")
	}
	var domainErr *shared.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected *shared.DomainError, got %T: %v", err, err)
	}
	if domainErr.Code != shared.ErrCodeConflict {
		t.Errorf("expected code %q, got %q (err: %v)", shared.ErrCodeConflict, domainErr.Code, err)
	}
	if p != nil {
		t.Errorf("no payment must be returned on the fail-closed branch, got %+v", p)
	}
	if len(recorder.snapshot()) != 0 {
		t.Errorf("AfterCharge must NOT fire for an unknown-status winner, got %d invocations", len(recorder.snapshot()))
	}
	if len(gw.refundTxnIDs) != 0 {
		t.Errorf("saga compensation must NOT fire on duplicate-key convergence, got refunds: %v", gw.refundTxnIDs)
	}
}
