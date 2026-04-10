package invoice

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestCreditNote_Snapshot_RoundTrip(t *testing.T) {
	t.Parallel()

	taxRate := big.NewRat(10, 100)
	item := NewCreditNoteItem(
		"li-1", "refund item",
		shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		taxRate,
		shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
	)

	createdAt := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	cn, err := NewCreditNote(
		shared.CreditNoteID("cn-1"),
		shared.InvoiceID("inv-1"),
		shared.AccountID("acc-1"),
		shared.ContractID("ctr-1"),
		CreditNoteReasonDuplicate,
		[]CreditNoteItem{item},
		createdAt,
		WithCreditNoteMemo("memo"),
		WithCreditNoteNumber("CN-001"),
	)
	if err != nil {
		t.Fatalf("NewCreditNote: %v", err)
	}

	snap := cn.ToSnapshot()
	snap.Status = CreditNoteStatusRefunded
	snap.RefundAmount = shared.NewMoney(big.NewRat(1100, 1), shared.CurrencyJPY)
	issuedAt := time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC)
	snap.IssuedAt = &issuedAt

	restored, err := CreditNoteFromSnapshot(snap)
	if err != nil {
		t.Fatalf("CreditNoteFromSnapshot: %v", err)
	}

	if restored.ID() != cn.ID() {
		t.Errorf("ID mismatch")
	}
	if restored.Number() != "CN-001" {
		t.Errorf("Number: %s", restored.Number())
	}
	if restored.Status() != CreditNoteStatusRefunded {
		t.Errorf("Status: %s", restored.Status())
	}
	if restored.Reason() != CreditNoteReasonDuplicate {
		t.Errorf("Reason: %s", restored.Reason())
	}
	if restored.Memo() != "memo" {
		t.Errorf("Memo: %s", restored.Memo())
	}
	if restored.IssuedAt() == nil || !restored.IssuedAt().Equal(issuedAt) {
		t.Errorf("IssuedAt: %v", restored.IssuedAt())
	}
	if !restored.CreatedAt().Equal(createdAt) {
		t.Errorf("CreatedAt: %v", restored.CreatedAt())
	}
	if restored.RefundAmount().Amount().Cmp(big.NewRat(1100, 1)) != 0 {
		t.Errorf("RefundAmount: %s", restored.RefundAmount().Amount().RatString())
	}
	items := restored.Items()
	if len(items) != 1 || items[0].InvoiceLineItemID() != "li-1" {
		t.Errorf("Items not restored: %+v", items)
	}
	if items[0].TaxRate().Cmp(taxRate) != 0 {
		t.Errorf("TaxRate mismatch")
	}
}

// TestCreditNote_FromSnapshot_AllowsEmptyItems verifies that reconstitution
// does NOT re-run the "must have at least one item" invariant from NewCreditNote.
// A DB row may have items stored in a separate table that the adapter loads
// incrementally; we must trust the stored state.
func TestCreditNote_FromSnapshot_AllowsEmptyItems(t *testing.T) {
	t.Parallel()

	snap := CreditNoteSnapshot{
		ID:         shared.CreditNoteID("cn-1"),
		InvoiceID:  shared.InvoiceID("inv-1"),
		AccountID:  shared.AccountID("acc-1"),
		ContractID: shared.ContractID("ctr-1"),
		Status:     CreditNoteStatusIssued,
		Reason:     CreditNoteReasonDuplicate,
		Items:      nil,
		Subtotal:   shared.Zero(shared.CurrencyJPY),
		TaxAmount:  shared.Zero(shared.CurrencyJPY),
		Total:      shared.Zero(shared.CurrencyJPY),
	}
	if _, err := CreditNoteFromSnapshot(snap); err != nil {
		t.Errorf("FromSnapshot should allow empty items: %v", err)
	}
}

func TestCreditNote_FromSnapshot_ValidatesID(t *testing.T) {
	t.Parallel()

	if _, err := CreditNoteFromSnapshot(CreditNoteSnapshot{}); err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestCreditNote_ToSnapshot_IsIndependentCopy(t *testing.T) {
	t.Parallel()

	item := NewCreditNoteItem(
		"li-1", "x",
		shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		big.NewRat(10, 100),
		shared.NewMoney(big.NewRat(10, 1), shared.CurrencyJPY),
	)
	cn, _ := NewCreditNote(
		shared.CreditNoteID("cn-1"),
		shared.InvoiceID("inv-1"),
		shared.AccountID("acc-1"),
		shared.ContractID("ctr-1"),
		CreditNoteReasonDuplicate,
		[]CreditNoteItem{item},
		time.Now(),
	)

	snap := cn.ToSnapshot()
	snap.Items[0] = CreditNoteItemSnapshot{InvoiceLineItemID: "mutated"}

	if cn.Items()[0].InvoiceLineItemID() != "li-1" {
		t.Error("ToSnapshot leaked items reference")
	}
}

// TestCreditNote_PointerIndependence verifies that nested pointer fields
// (CreditNoteItem.TaxRate *big.Rat, CreditNote.issuedAt *time.Time) are
// isolated at the Snapshot boundary.
func TestCreditNote_PointerIndependence(t *testing.T) {
	t.Parallel()

	taxRate := big.NewRat(10, 100)
	item := NewCreditNoteItem(
		"li-1", "x",
		shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		taxRate,
		shared.NewMoney(big.NewRat(10, 1), shared.CurrencyJPY),
	)
	cn, _ := NewCreditNote(
		shared.CreditNoteID("cn-1"),
		shared.InvoiceID("inv-1"),
		shared.AccountID("acc-1"),
		shared.ContractID("ctr-1"),
		CreditNoteReasonDuplicate,
		[]CreditNoteItem{item},
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	// Set issuedAt via snapshot round-trip.
	tmp := cn.ToSnapshot()
	issued := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	tmp.IssuedAt = &issued
	cn, err := CreditNoteFromSnapshot(tmp)
	if err != nil {
		t.Fatalf("CreditNoteFromSnapshot: %v", err)
	}

	// ToSnapshot: mutate nested pointers, entity must be unaffected.
	snap := cn.ToSnapshot()
	snap.Items[0].TaxRate.SetInt64(999)
	if got := cn.Items()[0].TaxRate().RatString(); got == "999" {
		t.Errorf("CreditNoteItem.TaxRate pointer was shared: %s", got)
	}

	snap2 := cn.ToSnapshot()
	*snap2.IssuedAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	if cn.IssuedAt() != nil && cn.IssuedAt().Year() == 2099 {
		t.Error("CreditNote.IssuedAt pointer was shared")
	}

	// FromSnapshot: mutate original snapshot, reconstructed entity must be unaffected.
	origTaxRate := big.NewRat(5, 100)
	origIssued := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	srcSnap := CreditNoteSnapshot{
		ID:         shared.CreditNoteID("cn-2"),
		InvoiceID:  shared.InvoiceID("inv-1"),
		AccountID:  shared.AccountID("acc-1"),
		ContractID: shared.ContractID("ctr-1"),
		Status:     CreditNoteStatusIssued,
		Reason:     CreditNoteReasonDuplicate,
		Items: []CreditNoteItemSnapshot{
			{
				InvoiceLineItemID: "li-1",
				Amount:            shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
				TaxRate:           origTaxRate,
				TaxAmount:         shared.NewMoney(big.NewRat(5, 1), shared.CurrencyJPY),
			},
		},
		Subtotal:  shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		TaxAmount: shared.NewMoney(big.NewRat(5, 1), shared.CurrencyJPY),
		Total:     shared.NewMoney(big.NewRat(105, 1), shared.CurrencyJPY),
		IssuedAt:  &origIssued,
	}
	restored, err := CreditNoteFromSnapshot(srcSnap)
	if err != nil {
		t.Fatalf("CreditNoteFromSnapshot: %v", err)
	}
	srcSnap.Items[0].TaxRate.SetInt64(999)
	*srcSnap.IssuedAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

	if got := restored.Items()[0].TaxRate().RatString(); got == "999" {
		t.Errorf("FromSnapshot: CreditNoteItem.TaxRate pointer was shared: %s", got)
	}
	if restored.IssuedAt() == nil || restored.IssuedAt().Year() != 2026 {
		t.Errorf("FromSnapshot: CreditNote.IssuedAt pointer was shared")
	}
}
