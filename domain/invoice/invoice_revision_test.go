package invoice

import (
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

// --- VoidWithReason tests ---

func TestVoidWithReason_FromIssued(t *testing.T) {
	inv := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithStatus(InvoiceStatusIssued),
	)

	if err := inv.VoidWithReason("billing error"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.Status() != InvoiceStatusVoided {
		t.Errorf("expected voided, got %s", inv.Status())
	}
	if inv.VoidReason() != "billing error" {
		t.Errorf("expected voidReason 'billing error', got %q", inv.VoidReason())
	}
}

func TestVoidWithReason_FromPaid(t *testing.T) {
	inv := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithStatus(InvoiceStatusPaid),
	)

	if err := inv.VoidWithReason("credit note issued"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.Status() != InvoiceStatusVoided {
		t.Errorf("expected voided, got %s", inv.Status())
	}
}

func TestVoidWithReason_FromOverdue(t *testing.T) {
	inv := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithStatus(InvoiceStatusOverdue),
	)

	if err := inv.VoidWithReason("order cancelled"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.Status() != InvoiceStatusVoided {
		t.Errorf("expected voided, got %s", inv.Status())
	}
}

func TestVoidWithReason_FromDraft(t *testing.T) {
	inv := newDraftInvoice()

	if err := inv.VoidWithReason("not needed"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.Status() != InvoiceStatusVoided {
		t.Errorf("expected voided, got %s", inv.Status())
	}
}

func TestVoidWithReason_FromFinalized(t *testing.T) {
	inv := newDraftInvoice()
	_ = inv.Finalize()

	if err := inv.VoidWithReason("correction needed"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.Status() != InvoiceStatusVoided {
		t.Errorf("expected voided, got %s", inv.Status())
	}
}

func TestVoidWithReason_FromPartialPaid(t *testing.T) {
	inv := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithStatus(InvoiceStatusPartialPaid),
	)

	if err := inv.VoidWithReason("credit note issued for partial refund"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.Status() != InvoiceStatusVoided {
		t.Errorf("expected voided, got %s", inv.Status())
	}
	if inv.VoidReason() != "credit note issued for partial refund" {
		t.Errorf("expected voidReason set, got %q", inv.VoidReason())
	}
}

func TestVoidWithReason_FromVoided_Rejected(t *testing.T) {
	inv := newDraftInvoice()
	_ = inv.Void()

	if err := inv.VoidWithReason("duplicate void"); err == nil {
		t.Fatal("expected error voiding already-voided invoice")
	}
}

func TestVoidWithReason_FromRefunded_Rejected(t *testing.T) {
	inv := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithStatus(InvoiceStatusRefunded),
	)

	if err := inv.VoidWithReason("already refunded"); err == nil {
		t.Fatal("expected error voiding refunded invoice")
	}
}

func TestVoidWithReason_EmptyReason_Rejected(t *testing.T) {
	inv := newDraftInvoice()

	if err := inv.VoidWithReason(""); err == nil {
		t.Fatal("expected error for empty void reason")
	}
}

// --- Revision link tests ---

func TestInvoice_WithRevisionOf(t *testing.T) {
	originalID := shared.NewInvoiceID()
	inv := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithRevisionOf(originalID),
	)

	if inv.RevisionOf() == nil {
		t.Fatal("expected revisionOf to be set")
	}
	if *inv.RevisionOf() != originalID {
		t.Errorf("expected revisionOf %s, got %s", originalID, *inv.RevisionOf())
	}
}

func TestInvoice_WithOriginalInvoiceID(t *testing.T) {
	originalID := shared.NewInvoiceID()
	inv := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithOriginalInvoiceID(originalID),
	)

	if inv.OriginalInvoiceID() == nil {
		t.Fatal("expected originalInvoiceID to be set")
	}
	if *inv.OriginalInvoiceID() != originalID {
		t.Errorf("expected originalInvoiceID %s, got %s", originalID, *inv.OriginalInvoiceID())
	}
}

func TestInvoice_SetRevisionOf(t *testing.T) {
	inv := newDraftInvoice()
	originalID := shared.NewInvoiceID()

	inv.SetRevisionOf(originalID)

	if inv.RevisionOf() == nil {
		t.Fatal("expected revisionOf to be set")
	}
	if *inv.RevisionOf() != originalID {
		t.Errorf("expected revisionOf %s, got %s", originalID, *inv.RevisionOf())
	}
}

func TestInvoice_RevisionFields_DefaultNil(t *testing.T) {
	inv := newDraftInvoice()

	if inv.RevisionOf() != nil {
		t.Error("expected revisionOf to be nil by default")
	}
	if inv.OriginalInvoiceID() != nil {
		t.Error("expected originalInvoiceID to be nil by default")
	}
	if inv.VoidReason() != "" {
		t.Error("expected voidReason to be empty by default")
	}
}
