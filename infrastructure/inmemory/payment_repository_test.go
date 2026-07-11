package inmemory

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
)

func newTestPayment(t *testing.T, invoiceID shared.InvoiceID) *payment.Payment {
	t.Helper()
	p, err := payment.NewPayment(
		shared.NewPaymentID(),
		invoiceID,
		shared.NewMoney(new(big.Rat).SetInt64(5000), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"gw_txn_123",
		time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("newTestPayment: %v", err)
	}
	return p
}

func TestInMemoryPaymentRepository_SaveAndFindByID(t *testing.T) {
	repo := NewInMemoryPaymentRepository()
	ctx := context.Background()

	invoiceID := shared.NewInvoiceID()
	p := newTestPayment(t, invoiceID)

	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	found, err := repo.FindByID(ctx, p.ID())
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if found.ID() != p.ID() {
		t.Errorf("expected ID %s, got %s", p.ID(), found.ID())
	}
	if found.InvoiceID() != invoiceID {
		t.Errorf("expected invoiceID %s, got %s", invoiceID, found.InvoiceID())
	}
	if found.Status() != payment.PaymentStatusPending {
		t.Errorf("expected status pending, got %s", found.Status())
	}
}

func TestInMemoryPaymentRepository_FindByID_NotFound(t *testing.T) {
	repo := NewInMemoryPaymentRepository()
	ctx := context.Background()

	_, err := repo.FindByID(ctx, shared.PaymentID("nonexistent"))
	if err == nil {
		t.Error("expected error for non-existent payment")
	}
}

func TestInMemoryPaymentRepository_FindByInvoiceID(t *testing.T) {
	repo := NewInMemoryPaymentRepository()
	ctx := context.Background()

	invoiceID1 := shared.NewInvoiceID()
	invoiceID2 := shared.NewInvoiceID()

	p1 := newTestPayment(t, invoiceID1)
	p2 := newTestPayment(t, invoiceID1)
	p3 := newTestPayment(t, invoiceID2)

	for _, p := range []*payment.Payment{p1, p2, p3} {
		if err := repo.Save(ctx, p); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	results, err := repo.FindByInvoiceID(ctx, invoiceID1)
	if err != nil {
		t.Fatalf("FindByInvoiceID failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 payments for invoiceID1, got %d", len(results))
	}

	// invoiceID2 should have 1 payment.
	results2, err := repo.FindByInvoiceID(ctx, invoiceID2)
	if err != nil {
		t.Fatalf("FindByInvoiceID failed: %v", err)
	}
	if len(results2) != 1 {
		t.Fatalf("expected 1 payment for invoiceID2, got %d", len(results2))
	}

	// Non-existent invoice should return empty slice.
	empty, err := repo.FindByInvoiceID(ctx, shared.NewInvoiceID())
	if err != nil {
		t.Fatalf("FindByInvoiceID failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 payments for unknown invoice, got %d", len(empty))
	}
}

func TestInMemoryPaymentRepository_FindByIdempotencyKey(t *testing.T) {
	repo := NewInMemoryPaymentRepository()
	ctx := context.Background()

	p := newTestPayment(t, shared.NewInvoiceID())
	p.SetIdempotencyKey("idem-key-001")

	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Found case.
	found, err := repo.FindByIdempotencyKey(ctx, "idem-key-001")
	if err != nil {
		t.Fatalf("FindByIdempotencyKey failed: %v", err)
	}
	if found == nil {
		t.Fatal("expected non-nil payment")
	}
	if found.ID() != p.ID() {
		t.Errorf("expected ID %s, got %s", p.ID(), found.ID())
	}

	// Not-found case.
	notFound, err := repo.FindByIdempotencyKey(ctx, "nonexistent-key")
	if err != nil {
		t.Fatalf("FindByIdempotencyKey failed: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for non-existent idempotency key")
	}

	// Empty key returns nil.
	emptyKey, err := repo.FindByIdempotencyKey(ctx, "")
	if err != nil {
		t.Fatalf("FindByIdempotencyKey with empty key failed: %v", err)
	}
	if emptyKey != nil {
		t.Error("expected nil for empty idempotency key")
	}
}

func TestInMemoryPaymentRepository_OverwriteOnDuplicateSave(t *testing.T) {
	repo := NewInMemoryPaymentRepository()
	ctx := context.Background()

	p := newTestPayment(t, shared.NewInvoiceID())

	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Complete the payment and save again.
	if err := p.Complete(); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("second Save failed: %v", err)
	}

	found, err := repo.FindByID(ctx, p.ID())
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if found.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected status completed after overwrite, got %s", found.Status())
	}
}

// TestInMemoryPaymentRepository_Save_DuplicateIdempotencyKey verifies the
// #97 concurrent-success-race contract: Save rejects a second payment
// record with the same idempotency_key but a different PaymentID, and
// the returned error satisfies errors.Is against
// payment.ErrDuplicateIdempotencyKey and errors.As to a
// *payment.DuplicateIdempotencyKeyError with populated fields.
// PaymentService relies on this sentinel to route the race loser to
// the winner's record instead of firing saga compensation.
func TestInMemoryPaymentRepository_Save_DuplicateIdempotencyKey(t *testing.T) {
	repo := NewInMemoryPaymentRepository()
	ctx := context.Background()

	invoiceID := shared.NewInvoiceID()
	p1 := newTestPayment(t, invoiceID)
	p1.SetIdempotencyKey("idem-unique-001")
	if err := repo.Save(ctx, p1); err != nil {
		t.Fatalf("first Save failed: %v", err)
	}

	// Second payment with a DIFFERENT ID but the SAME idempotency key
	// must be rejected via the payment-scoped sentinel.
	p2 := newTestPayment(t, invoiceID)
	p2.SetIdempotencyKey("idem-unique-001")
	err := repo.Save(ctx, p2)
	if err == nil {
		t.Fatal("expected duplicate-key error on second Save, got nil")
	}
	if !errors.Is(err, payment.ErrDuplicateIdempotencyKey) {
		t.Errorf("errors.Is(err, ErrDuplicateIdempotencyKey) must be true, got: %v", err)
	}
	var dupErr *payment.DuplicateIdempotencyKeyError
	if !errors.As(err, &dupErr) {
		t.Fatalf("expected *DuplicateIdempotencyKeyError, got %T: %v", err, err)
	}
	if dupErr.Key != "idem-unique-001" {
		t.Errorf("expected Key=%q, got %q", "idem-unique-001", dupErr.Key)
	}
	if dupErr.ExistingID != p1.ID() {
		t.Errorf("expected ExistingID=%s, got %s", p1.ID(), dupErr.ExistingID)
	}
	if dupErr.AttemptedID != p2.ID() {
		t.Errorf("expected AttemptedID=%s, got %s", p2.ID(), dupErr.AttemptedID)
	}

	// Exactly one payment must be persisted for this idempotency key.
	found, err := repo.FindByIdempotencyKey(ctx, "idem-unique-001")
	if err != nil {
		t.Fatalf("FindByIdempotencyKey: %v", err)
	}
	if found == nil || found.ID() != p1.ID() {
		t.Errorf("expected to find winner %s, got %+v", p1.ID(), found)
	}
}

// TestInMemoryPaymentRepository_Save_SameIDReSaveAllowed ensures the
// same-ID re-save path (e.g. the 3DS Pending → Completed upgrade) is
// not blocked by the duplicate-key guard.
func TestInMemoryPaymentRepository_Save_SameIDReSaveAllowed(t *testing.T) {
	repo := NewInMemoryPaymentRepository()
	ctx := context.Background()

	p := newTestPayment(t, shared.NewInvoiceID())
	p.SetIdempotencyKey("idem-reuse-001")
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("first Save failed: %v", err)
	}

	// Upgrade the same instance to Completed and re-save.
	if err := p.Complete(); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if err := repo.Save(ctx, p); err != nil {
		t.Errorf("same-ID re-save must succeed, got: %v", err)
	}

	found, err := repo.FindByID(ctx, p.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.Status() != payment.PaymentStatusCompleted {
		t.Errorf("expected Completed after upgrade, got %q", found.Status())
	}
}

// TestInMemoryPaymentRepository_Save_EmptyKeyNotEnforced verifies that the
// unique guard only applies to NON-empty idempotency keys. Legacy
// payments without a key must not be reported as duplicates of one
// another — there is no race to protect against there.
func TestInMemoryPaymentRepository_Save_EmptyKeyNotEnforced(t *testing.T) {
	repo := NewInMemoryPaymentRepository()
	ctx := context.Background()

	invoiceID := shared.NewInvoiceID()
	p1 := newTestPayment(t, invoiceID)
	p2 := newTestPayment(t, invoiceID)
	if p1.IdempotencyKey() != "" || p2.IdempotencyKey() != "" {
		t.Fatalf("precondition: fresh payments must have empty idempotency key; got %q / %q",
			p1.IdempotencyKey(), p2.IdempotencyKey())
	}

	if err := repo.Save(ctx, p1); err != nil {
		t.Fatalf("first Save failed: %v", err)
	}
	if err := repo.Save(ctx, p2); err != nil {
		t.Errorf("empty-key Save must be allowed, got: %v", err)
	}
}

// TestInMemoryPaymentRepository_Save_VersionConflict verifies the #190
// optimistic-locking contract: two callers that both FindByID the same
// completed payment and each RecordRefund + Save must not both persist. The
// second Save (whose LoadedVersion is now stale) is rejected with an error that
// tx.IsVersionConflict recognizes.
func TestInMemoryPaymentRepository_Save_VersionConflict(t *testing.T) {
	repo := NewInMemoryPaymentRepository()
	ctx := context.Background()

	base := newTestPayment(t, shared.NewInvoiceID())
	if err := base.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := repo.Save(ctx, base); err != nil {
		t.Fatalf("seed Save failed: %v", err)
	}

	// Two independent loads observe the same stored version.
	loadA, err := repo.FindByID(ctx, base.ID())
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	loadB, err := repo.FindByID(ctx, base.ID())
	if err != nil {
		t.Fatalf("load B: %v", err)
	}

	amount := shared.NewMoney(new(big.Rat).SetInt64(3000), shared.CurrencyJPY)
	if err := loadA.RecordRefund(amount); err != nil {
		t.Fatalf("A RecordRefund: %v", err)
	}
	if err := repo.Save(ctx, loadA); err != nil {
		t.Fatalf("A Save must win, got: %v", err)
	}

	// B loaded the pre-refund version; its Save must be rejected.
	if err := loadB.RecordRefund(amount); err != nil {
		t.Fatalf("B RecordRefund: %v", err)
	}
	err = repo.Save(ctx, loadB)
	if err == nil {
		t.Fatal("expected version conflict on stale Save, got nil")
	}
	if !tx.IsVersionConflict(err) {
		t.Errorf("expected a version conflict error, got: %v", err)
	}

	// The winner's refund is the only one recorded.
	stored, err := repo.FindByID(ctx, base.ID())
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	if stored.RefundedAmount().Amount().Cmp(big.NewRat(3000, 1)) != 0 {
		t.Errorf("expected refunded 3000 (one refund), got %s",
			stored.RefundedAmount().Amount().RatString())
	}
	if stored.Status() != payment.PaymentStatusPartiallyRefunded {
		t.Errorf("expected partially_refunded, got %s", stored.Status())
	}
}

// TestInMemoryPaymentRepository_ConcurrentRecordRefund exercises the race under
// -race: many goroutines concurrently load the same completed payment, each
// RecordRefund(6000) + Save. Exactly one must win; every loser must observe a
// version conflict. Without optimistic locking both would succeed
// last-writer-wins and the books would show one 6000 refund while the gateway
// moved 12000 (issue #190).
func TestInMemoryPaymentRepository_ConcurrentRecordRefund(t *testing.T) {
	repo := NewInMemoryPaymentRepository()
	ctx := context.Background()

	base, err := payment.NewPayment(
		shared.NewPaymentID(),
		shared.NewInvoiceID(),
		shared.NewMoney(new(big.Rat).SetInt64(10000), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"gw_txn",
		time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewPayment: %v", err)
	}
	if err := base.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := repo.Save(ctx, base); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	const goroutines = 8
	// Pre-load one isolated copy per goroutine BEFORE any Save runs, so every
	// copy observes the same stored version. Loading inside the goroutines would
	// let a late loader read the winner's already-bumped version and win too,
	// making the "exactly one winner" assertion flaky. The real-world race this
	// models is exactly this: multiple operators who all loaded the payment
	// before any of them committed a refund.
	loaded := make([]*payment.Payment, goroutines)
	for i := range loaded {
		lp, findErr := repo.FindByID(ctx, base.ID())
		if findErr != nil {
			t.Fatalf("preload FindByID: %v", findErr)
		}
		if refundErr := lp.RecordRefund(
			shared.NewMoney(new(big.Rat).SetInt64(6000), shared.CurrencyJPY)); refundErr != nil {
			t.Fatalf("preload RecordRefund: %v", refundErr)
		}
		loaded[i] = lp
	}

	var wins, conflicts int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(lp *payment.Payment) {
			defer wg.Done()
			<-start
			saveErr := repo.Save(ctx, lp)
			switch {
			case saveErr == nil:
				atomic.AddInt64(&wins, 1)
			case tx.IsVersionConflict(saveErr):
				atomic.AddInt64(&conflicts, 1)
			default:
				t.Errorf("unexpected Save error: %v", saveErr)
			}
		}(loaded[i])
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Errorf("expected exactly 1 winning Save, got %d", wins)
	}
	if conflicts != goroutines-1 {
		t.Errorf("expected %d version conflicts, got %d", goroutines-1, conflicts)
	}

	stored, err := repo.FindByID(ctx, base.ID())
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	// Only the winner's single 6000 refund is booked — never 12000.
	if stored.RefundedAmount().Amount().Cmp(big.NewRat(6000, 1)) != 0 {
		t.Errorf("expected refunded 6000 (single winner), got %s",
			stored.RefundedAmount().Amount().RatString())
	}
}
