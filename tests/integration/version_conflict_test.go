package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
)

// TestInvoice_ConcurrentRecordPayment_SecondSaveConflicts is the #147
// regression test for the invoice payment path. Two copies of the same invoice
// are loaded at the same version (modeling two transactions that each read the
// row), both record a payment, and the second Save must be rejected with
// tx.ErrVersionConflict. Before #147, RecordPayment did not bump the version,
// so a compliant optimistic-locking repository could not detect the lost update
// and both partial payments silently under-reported paidAmount.
func TestInvoice_ConcurrentRecordPayment_SecondSaveConflicts(t *testing.T) {
	ctx := context.Background()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	repo := inmemory.NewInMemoryInvoiceRepository(clock)

	// Seed a finalized invoice (payment-eligible) and persist it so the repo
	// records a stored version.
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
		moneyJPY(10000), moneyJPY(0), moneyJPY(0),
		invoice.WithAllowPartialPayment(true),
	)
	if err != nil {
		t.Fatalf("NewInvoice failed: %v", err)
	}
	if err := inv.Finalize(); err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}
	if err := repo.Save(ctx, inv); err != nil {
		t.Fatalf("seeding invoice failed: %v", err)
	}

	// Load two independent copies at the same (stored) version, as two
	// concurrent transactions would each obtain their own row snapshot.
	copyA := loadInvoiceClone(t, repo, inv.ID())
	copyB := loadInvoiceClone(t, repo, inv.ID())
	if copyA.LoadedVersion() != copyB.LoadedVersion() {
		t.Fatalf("precondition: copies loaded at different versions (%d vs %d)",
			copyA.LoadedVersion(), copyB.LoadedVersion())
	}

	// Both record a partial payment; each bumps its own in-memory version.
	if err := copyA.RecordPayment(moneyJPY(4000), clock.Now()); err != nil {
		t.Fatalf("copyA.RecordPayment failed: %v", err)
	}
	if err := copyB.RecordPayment(moneyJPY(3000), clock.Now()); err != nil {
		t.Fatalf("copyB.RecordPayment failed: %v", err)
	}

	// Winner commits.
	if err := repo.Save(ctx, copyA); err != nil {
		t.Fatalf("copyA.Save (winner) should succeed, got: %v", err)
	}
	// Loser was loaded at the now-stale version and must be rejected.
	if err := repo.Save(ctx, copyB); !errors.Is(err, tx.ErrVersionConflict) {
		t.Fatalf("copyB.Save (loser) = %v, want tx.ErrVersionConflict", err)
	}

	// Only the winner's payment is persisted — no silent under-reporting.
	persisted, err := repo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if got := persisted.PaidAmount(); got.Amount().Cmp(moneyJPY(4000).Amount()) != 0 {
		t.Errorf("persisted paidAmount = %s, want 4000 (winner only)", got.Amount().RatString())
	}
}

// TestCreditNote_ConcurrentApplyRefund_SecondSaveConflicts is the #147
// regression test for the credit note path — the failure scenario in the issue
// where an issued credit note is Apply()'d by one caller and Refund()'d by
// another, both succeed, and the account is credited AND the gateway refunded
// while only one outcome is recorded. With version machinery on CreditNote, the
// second Save is rejected with tx.ErrVersionConflict.
func TestCreditNote_ConcurrentApplyRefund_SecondSaveConflicts(t *testing.T) {
	ctx := context.Background()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	repo := inmemory.NewInMemoryCreditNoteRepository()

	// Seed an issued credit note (Apply/Refund eligible) and persist it.
	cn := newIssuedCreditNote(t, clock.Now())
	if err := repo.Save(ctx, cn); err != nil {
		t.Fatalf("seeding credit note failed: %v", err)
	}

	// Two independent copies loaded at the same stored version.
	copyA := loadCreditNoteClone(t, repo, cn.ID())
	copyB := loadCreditNoteClone(t, repo, cn.ID())
	if copyA.LoadedVersion() != copyB.LoadedVersion() {
		t.Fatalf("precondition: copies loaded at different versions (%d vs %d)",
			copyA.LoadedVersion(), copyB.LoadedVersion())
	}

	// One applies the credit to the account; the other refunds via the gateway.
	if err := copyA.Apply(moneyJPY(500)); err != nil {
		t.Fatalf("copyA.Apply failed: %v", err)
	}
	if err := copyB.Refund(moneyJPY(500)); err != nil {
		t.Fatalf("copyB.Refund failed: %v", err)
	}

	// Winner commits; loser is rejected.
	if err := repo.Save(ctx, copyA); err != nil {
		t.Fatalf("copyA.Save (winner) should succeed, got: %v", err)
	}
	if err := repo.Save(ctx, copyB); !errors.Is(err, tx.ErrVersionConflict) {
		t.Fatalf("copyB.Save (loser) = %v, want tx.ErrVersionConflict", err)
	}

	// Only the applied outcome is persisted — not both applied and refunded.
	persisted, err := repo.FindByID(ctx, cn.ID())
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if persisted.Status() != invoice.CreditNoteStatusApplied {
		t.Errorf("persisted status = %s, want applied (winner only)", persisted.Status())
	}
}

// loadInvoiceClone loads an invoice and returns an independent copy (via a
// snapshot round trip) that shares the stored version but not the pointer, as a
// real RDBMS hands each transaction its own row snapshot.
func loadInvoiceClone(t *testing.T, repo invoice.Repository, id shared.InvoiceID) *invoice.Invoice {
	t.Helper()
	loaded, err := repo.FindByID(context.Background(), id)
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	clone, err := invoice.InvoiceFromSnapshot(loaded.ToSnapshot())
	if err != nil {
		t.Fatalf("InvoiceFromSnapshot failed: %v", err)
	}
	return clone
}

// loadCreditNoteClone is the credit-note analogue of loadInvoiceClone.
func loadCreditNoteClone(t *testing.T, repo invoice.CreditNoteRepository, id shared.CreditNoteID) *invoice.CreditNote {
	t.Helper()
	loaded, err := repo.FindByID(context.Background(), id)
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	clone, err := invoice.CreditNoteFromSnapshot(loaded.ToSnapshot())
	if err != nil {
		t.Fatalf("CreditNoteFromSnapshot failed: %v", err)
	}
	return clone
}

// newIssuedCreditNote builds a credit note (total 550 JPY) and issues it.
func newIssuedCreditNote(t *testing.T, issuedAt time.Time) *invoice.CreditNote {
	t.Helper()
	item := invoice.NewCreditNoteItem("li-1", "adjustment", moneyJPY(500), nil, moneyJPY(50))
	cn, err := invoice.NewCreditNote(
		shared.NewCreditNoteID(), shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
		invoice.CreditNoteReasonOrderChange,
		[]invoice.CreditNoteItem{item},
		issuedAt,
	)
	if err != nil {
		t.Fatalf("NewCreditNote failed: %v", err)
	}
	if err := cn.Issue(issuedAt); err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	return cn
}
