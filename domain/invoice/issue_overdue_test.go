package invoice

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Tests for MarkIssued / MarkOverdue (issue #159): the issued and overdue
// statuses were previously reachable only via InvoiceFromSnapshot (persistence
// adapters) — the same defect class fixed for refunded in issue #99.

func newInvoiceWithStatusAndDueDate(t *testing.T, status InvoiceStatus, due time.Time) *Invoice {
	t.Helper()
	return mustNewInvoice(t,
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithStatus(status),
		WithDueDate(due),
	)
}

// --- MarkIssued ---

func TestMarkIssued_FromFinalized(t *testing.T) {
	inv := newInvoiceWithStatus(t, InvoiceStatusFinalized)
	before := inv.Version()

	if err := inv.MarkIssued(); err != nil {
		t.Fatalf("unexpected error issuing finalized invoice: %v", err)
	}
	if inv.Status() != InvoiceStatusIssued {
		t.Errorf("expected issued, got %s", inv.Status())
	}
	// Like Finalize, MarkIssued bumps the optimistic-locking version (#147).
	if inv.Version() != before+1 {
		t.Errorf("expected version %d, got %d", before+1, inv.Version())
	}
}

func TestMarkIssued_InvalidSourceStates_Rejected(t *testing.T) {
	invalid := []InvoiceStatus{
		InvoiceStatusDraft,
		InvoiceStatusIssued, // already issued — guards against double issue
		InvoiceStatusPaid,
		InvoiceStatusPartialPaid,
		InvoiceStatusOverdue,
		InvoiceStatusVoided,
		InvoiceStatusRefunded,
	}
	for _, status := range invalid {
		inv := newInvoiceWithStatus(t, status)
		before := inv.Version()

		err := inv.MarkIssued()
		if err == nil {
			t.Errorf("expected error issuing invoice in status %s, got nil", status)
			continue
		}
		assertDomainCode(t, err, shared.ErrCodeInvalidStateTransition)
		if inv.Status() != status {
			t.Errorf("status must not change on rejected issue: was %s, got %s", status, inv.Status())
		}
		if inv.Version() != before {
			t.Errorf("version must not change on rejected issue: was %d, got %d", before, inv.Version())
		}
	}
}

func TestMarkIssued_ThenPaymentAndVoidStillWork(t *testing.T) {
	// The issued status must stay compatible with the existing payment and
	// void flows (ValidatePayment / VoidWithReason accept issued).
	inv := newInvoiceWithStatus(t, InvoiceStatusFinalized)
	if err := inv.MarkIssued(); err != nil {
		t.Fatalf("MarkIssued: %v", err)
	}
	paidAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if err := inv.RecordPayment(inv.AmountDue(), paidAt); err != nil {
		t.Fatalf("RecordPayment on issued invoice: %v", err)
	}
	if inv.Status() != InvoiceStatusPaid {
		t.Errorf("expected paid after full payment, got %s", inv.Status())
	}

	inv2 := newInvoiceWithStatus(t, InvoiceStatusFinalized)
	if err := inv2.MarkIssued(); err != nil {
		t.Fatalf("MarkIssued: %v", err)
	}
	if err := inv2.VoidWithReason("credit note issued"); err != nil {
		t.Fatalf("VoidWithReason on issued invoice: %v", err)
	}
	if inv2.Status() != InvoiceStatusVoided {
		t.Errorf("expected voided, got %s", inv2.Status())
	}
}

// --- MarkOverdue ---

func TestMarkOverdue_FromFinalizedAndIssued(t *testing.T) {
	due := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	now := due.Add(24 * time.Hour)

	for _, status := range []InvoiceStatus{InvoiceStatusFinalized, InvoiceStatusIssued} {
		inv := newInvoiceWithStatusAndDueDate(t, status, due)
		before := inv.Version()

		if err := inv.MarkOverdue(now); err != nil {
			t.Fatalf("unexpected error marking %s invoice overdue: %v", status, err)
		}
		if inv.Status() != InvoiceStatusOverdue {
			t.Errorf("expected overdue from %s, got %s", status, inv.Status())
		}
		if inv.Version() != before+1 {
			t.Errorf("expected version %d, got %d", before+1, inv.Version())
		}
	}
}

func TestMarkOverdue_DueDateNotPassed_Rejected(t *testing.T) {
	due := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)

	// Exactly at the due date is NOT overdue (now must be strictly after).
	for _, now := range []time.Time{due, due.Add(-time.Hour)} {
		inv := newInvoiceWithStatusAndDueDate(t, InvoiceStatusFinalized, due)
		err := inv.MarkOverdue(now)
		if err == nil {
			t.Fatalf("expected error marking overdue at now=%s (due %s), got nil", now, due)
		}
		assertDomainCode(t, err, shared.ErrCodeBusinessRule)
		if inv.Status() != InvoiceStatusFinalized {
			t.Errorf("status must not change on rejected overdue, got %s", inv.Status())
		}
	}
}

func TestMarkOverdue_NoDueDate_Rejected(t *testing.T) {
	inv := newInvoiceWithStatus(t, InvoiceStatusFinalized) // zero due date
	err := inv.MarkOverdue(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected error marking overdue with no due date, got nil")
	}
	assertDomainCode(t, err, shared.ErrCodeBusinessRule)
}

func TestMarkOverdue_InvalidSourceStates_Rejected(t *testing.T) {
	due := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	now := due.Add(24 * time.Hour)

	// partial_paid is deliberately excluded from overdue: it already encodes
	// collected money, and RecordPayment resolves it directly.
	invalid := []InvoiceStatus{
		InvoiceStatusDraft,
		InvoiceStatusPaid,
		InvoiceStatusPartialPaid,
		InvoiceStatusOverdue, // already overdue
		InvoiceStatusVoided,
		InvoiceStatusRefunded,
	}
	for _, status := range invalid {
		inv := newInvoiceWithStatusAndDueDate(t, status, due)
		before := inv.Version()

		err := inv.MarkOverdue(now)
		if err == nil {
			t.Errorf("expected error marking invoice overdue in status %s, got nil", status)
			continue
		}
		assertDomainCode(t, err, shared.ErrCodeInvalidStateTransition)
		if inv.Status() != status {
			t.Errorf("status must not change on rejected overdue: was %s, got %s", status, inv.Status())
		}
		if inv.Version() != before {
			t.Errorf("version must not change on rejected overdue: was %d, got %d", before, inv.Version())
		}
	}
}

func TestMarkOverdue_ThenPaymentResolves(t *testing.T) {
	// An overdue invoice can still be settled: RecordPayment accepts overdue
	// and resolves it to paid.
	due := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	inv := newInvoiceWithStatusAndDueDate(t, InvoiceStatusIssued, due)
	if err := inv.MarkOverdue(due.Add(time.Hour)); err != nil {
		t.Fatalf("MarkOverdue: %v", err)
	}
	if err := inv.RecordPayment(inv.AmountDue(), due.Add(48*time.Hour)); err != nil {
		t.Fatalf("RecordPayment on overdue invoice: %v", err)
	}
	if inv.Status() != InvoiceStatusPaid {
		t.Errorf("expected paid after settling overdue invoice, got %s", inv.Status())
	}
}
