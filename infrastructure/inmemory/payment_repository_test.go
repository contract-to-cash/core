package inmemory

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
)

func newTestPayment(t *testing.T, invoiceID shared.InvoiceID) *payment.Payment {
	t.Helper()
	return payment.NewPayment(
		shared.NewPaymentID(),
		invoiceID,
		shared.NewMoney(new(big.Rat).SetInt64(5000), shared.CurrencyJPY),
		payment.PaymentMethodCreditCard,
		"gw_txn_123",
		time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
	)
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
