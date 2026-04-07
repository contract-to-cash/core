package invoice

import (
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

func newDraftInvoice() *Invoice {
	inv, err := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
	)
	if err != nil {
		panic("newDraftInvoice: " + err.Error())
	}
	return inv
}

func TestVoid_FromDraft(t *testing.T) {
	inv := newDraftInvoice()
	if err := inv.Void(); err != nil {
		t.Fatalf("unexpected error voiding draft invoice: %v", err)
	}
	if inv.Status() != InvoiceStatusVoided {
		t.Errorf("expected voided, got %s", inv.Status())
	}
}

func TestVoid_FromFinalized(t *testing.T) {
	inv := newDraftInvoice()
	if err := inv.Finalize(); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	if err := inv.Void(); err != nil {
		t.Fatalf("unexpected error voiding finalized invoice: %v", err)
	}
	if inv.Status() != InvoiceStatusVoided {
		t.Errorf("expected voided, got %s", inv.Status())
	}
}

func TestVoid_FromPaid_Rejected(t *testing.T) {
	inv := mustNewInvoice(t,
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithStatus(InvoiceStatusPaid),
	)
	if err := inv.Void(); err == nil {
		t.Fatal("expected error voiding paid invoice, got nil")
	}
}

func TestVoid_FromPartialPaid_Rejected(t *testing.T) {
	inv := mustNewInvoice(t,
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithStatus(InvoiceStatusPartialPaid),
	)
	if err := inv.Void(); err == nil {
		t.Fatal("expected error voiding partial_paid invoice, got nil")
	}
}

func TestVoid_FromOverdue_Rejected(t *testing.T) {
	inv := mustNewInvoice(t,
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithStatus(InvoiceStatusOverdue),
	)
	if err := inv.Void(); err == nil {
		t.Fatal("expected error voiding overdue invoice, got nil")
	}
}

func TestVoid_FromVoided_Rejected(t *testing.T) {
	inv := newDraftInvoice()
	_ = inv.Void()
	if err := inv.Void(); err == nil {
		t.Fatal("expected error voiding already-voided invoice, got nil")
	}
}
