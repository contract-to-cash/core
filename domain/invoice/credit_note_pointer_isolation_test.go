// credit_note_pointer_isolation_test.go — see issue #96.
package invoice

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// --- CreditNoteItem.TaxRate ---

func TestCreditNoteItem_TaxRate_GetterIsDefensivelyCopied(t *testing.T) {
	taxRate := big.NewRat(10, 100)
	item := NewCreditNoteItem("li-1", "refund", jpy(1000), taxRate, jpy(100))

	item.TaxRate().SetInt64(999)

	got := item.TaxRate()
	if got == nil || got.Cmp(big.NewRat(10, 100)) != 0 {
		t.Errorf("CreditNoteItem.TaxRate() leaks internal pointer: got %v, want 10/100", got)
	}
}

func TestCreditNoteItem_TaxRate_GetterNilSafe(t *testing.T) {
	item := NewCreditNoteItem("li-1", "refund", jpy(1000), nil, jpy(0))
	if item.TaxRate() != nil {
		t.Errorf("expected nil taxRate, got %v", item.TaxRate())
	}
}

// TestNewCreditNoteItem_TaxRate_IntakeIsDefensivelyCopied verifies that
// mutating the caller's taxRate after passing it to NewCreditNoteItem does
// NOT alter the item's internal state. Pattern C.
func TestNewCreditNoteItem_TaxRate_IntakeIsDefensivelyCopied(t *testing.T) {
	taxRate := big.NewRat(10, 100)
	item := NewCreditNoteItem("li-1", "refund", jpy(1000), taxRate, jpy(100))

	taxRate.SetInt64(999)

	got := item.TaxRate()
	if got == nil || got.Cmp(big.NewRat(10, 100)) != 0 {
		t.Errorf("NewCreditNoteItem does not defend taxRate at intake: got %v, want 10/100", got)
	}
}

// --- CreditNote.IssuedAt ---

func TestCreditNote_IssuedAt_GetterIsDefensivelyCopied(t *testing.T) {
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	item := NewCreditNoteItem("li-1", "refund", jpy(1000), big.NewRat(10, 100), jpy(100))
	cn, err := NewCreditNote(
		shared.NewCreditNoteID(),
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		CreditNoteReasonOrderChange,
		[]CreditNoteItem{item},
		createdAt,
	)
	if err != nil {
		t.Fatalf("NewCreditNote: %v", err)
	}

	issuedAt := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if err := cn.Issue(issuedAt); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	got := cn.IssuedAt()
	if got == nil {
		t.Fatal("IssuedAt must not be nil")
	}
	*got = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

	again := cn.IssuedAt()
	if again == nil || !again.Equal(issuedAt) {
		t.Errorf("CreditNote.IssuedAt() leaks internal pointer: got %v, want %v", again, issuedAt)
	}
}

// TestCreditNote_IssuedAt_NilBeforeIssue verifies that IssuedAt is nil on
// a newly created (draft) credit note and the getter does not panic.
func TestCreditNote_IssuedAt_NilBeforeIssue(t *testing.T) {
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	item := NewCreditNoteItem("li-1", "refund", jpy(1000), big.NewRat(10, 100), jpy(100))
	cn, err := NewCreditNote(
		shared.NewCreditNoteID(),
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		CreditNoteReasonOrderChange,
		[]CreditNoteItem{item},
		createdAt,
	)
	if err != nil {
		t.Fatalf("NewCreditNote: %v", err)
	}
	if got := cn.IssuedAt(); got != nil {
		t.Errorf("expected nil IssuedAt for draft credit note, got %v", got)
	}
}
