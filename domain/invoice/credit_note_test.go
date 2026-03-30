package invoice

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func newTestCreditNote() *CreditNote {
	return NewCreditNote(
		shared.NewCreditNoteID(),
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		CreditNoteReasonOrderChange,
		[]CreditNoteItem{
			NewCreditNoteItem("li-1", "Plan adjustment", jpy(5000), big.NewRat(10, 100), jpy(500)),
		},
		time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
	)
}

// --- Constructor tests ---

func TestNewCreditNote_CreatedAtIsSetFromParameter(t *testing.T) {
	fixedTime := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	cn := NewCreditNote(
		shared.NewCreditNoteID(),
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		CreditNoteReasonOrderChange,
		[]CreditNoteItem{
			NewCreditNoteItem("li-1", "Adjustment", jpy(3000), big.NewRat(10, 100), jpy(300)),
		},
		fixedTime,
	)

	if !cn.CreatedAt().Equal(fixedTime) {
		t.Errorf("expected createdAt %v, got %v", fixedTime, cn.CreatedAt())
	}
}

func TestNewCreditNote_Defaults(t *testing.T) {
	id := shared.NewCreditNoteID()
	invoiceID := shared.NewInvoiceID()
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	createdAt := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	items := []CreditNoteItem{
		NewCreditNoteItem("li-1", "Adjustment", jpy(3000), big.NewRat(10, 100), jpy(300)),
		NewCreditNoteItem("li-2", "Proration", jpy(2000), big.NewRat(10, 100), jpy(200)),
	}

	cn := NewCreditNote(id, invoiceID, accountID, contractID, CreditNoteReasonOrderChange, items, createdAt)

	if cn.ID() != id {
		t.Errorf("expected id %s, got %s", id, cn.ID())
	}
	if cn.InvoiceID() != invoiceID {
		t.Errorf("expected invoiceID %s, got %s", invoiceID, cn.InvoiceID())
	}
	if cn.AccountID() != accountID {
		t.Errorf("expected accountID %s, got %s", accountID, cn.AccountID())
	}
	if cn.ContractID() != contractID {
		t.Errorf("expected contractID %s, got %s", contractID, cn.ContractID())
	}
	if cn.Status() != CreditNoteStatusDraft {
		t.Errorf("expected status draft, got %s", cn.Status())
	}
	if cn.Reason() != CreditNoteReasonOrderChange {
		t.Errorf("expected reason order_change, got %s", cn.Reason())
	}

	// subtotal = 3000 + 2000 = 5000
	if cn.Subtotal().Amount().Cmp(big.NewRat(5000, 1)) != 0 {
		t.Errorf("expected subtotal 5000, got %s", cn.Subtotal().Amount().RatString())
	}
	// taxAmount = 300 + 200 = 500
	if cn.TaxAmount().Amount().Cmp(big.NewRat(500, 1)) != 0 {
		t.Errorf("expected taxAmount 500, got %s", cn.TaxAmount().Amount().RatString())
	}
	// total = 5000 + 500 = 5500
	if cn.Total().Amount().Cmp(big.NewRat(5500, 1)) != 0 {
		t.Errorf("expected total 5500, got %s", cn.Total().Amount().RatString())
	}
	if cn.CreditAmount().Amount().Cmp(big.NewRat(0, 1)) != 0 {
		t.Errorf("expected creditAmount 0, got %s", cn.CreditAmount().Amount().RatString())
	}
	if cn.RefundAmount().Amount().Cmp(big.NewRat(0, 1)) != 0 {
		t.Errorf("expected refundAmount 0, got %s", cn.RefundAmount().Amount().RatString())
	}
	if len(cn.Items()) != 2 {
		t.Errorf("expected 2 items, got %d", len(cn.Items()))
	}
}

func TestNewCreditNote_WithOptions(t *testing.T) {
	cn := NewCreditNote(
		shared.NewCreditNoteID(),
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		CreditNoteReasonDuplicate,
		[]CreditNoteItem{
			NewCreditNoteItem("li-1", "Full refund", jpy(10000), big.NewRat(10, 100), jpy(1000)),
		},
		time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
		WithCreditNoteMemo("Duplicate charge"),
		WithCreditNoteNumber("CN-00001"),
	)

	if cn.Memo() != "Duplicate charge" {
		t.Errorf("expected memo 'Duplicate charge', got %q", cn.Memo())
	}
	if cn.Number() != "CN-00001" {
		t.Errorf("expected number 'CN-00001', got %q", cn.Number())
	}
}

func TestNewCreditNote_EmptyItems_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for empty items, got nil")
		}
	}()

	NewCreditNote(
		shared.NewCreditNoteID(),
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		CreditNoteReasonOther,
		[]CreditNoteItem{},
		time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
	)
}

// --- CreditNoteItem tests ---

func TestNewCreditNoteItem(t *testing.T) {
	item := NewCreditNoteItem("li-1", "Plan downgrade", jpy(5000), big.NewRat(10, 100), jpy(500))

	if item.InvoiceLineItemID() != "li-1" {
		t.Errorf("expected lineItemID 'li-1', got %q", item.InvoiceLineItemID())
	}
	if item.Description() != "Plan downgrade" {
		t.Errorf("expected description 'Plan downgrade', got %q", item.Description())
	}
	if item.Amount().Amount().Cmp(big.NewRat(5000, 1)) != 0 {
		t.Errorf("expected amount 5000, got %s", item.Amount().Amount().RatString())
	}
	if item.TaxRate().Cmp(big.NewRat(10, 100)) != 0 {
		t.Errorf("expected taxRate 10/100, got %s", item.TaxRate().RatString())
	}
	if item.TaxAmount().Amount().Cmp(big.NewRat(500, 1)) != 0 {
		t.Errorf("expected taxAmount 500, got %s", item.TaxAmount().Amount().RatString())
	}
}

// --- State transition tests ---

func TestCreditNote_Issue_FromDraft(t *testing.T) {
	cn := newTestCreditNote()
	now := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)

	if err := cn.Issue(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cn.Status() != CreditNoteStatusIssued {
		t.Errorf("expected status issued, got %s", cn.Status())
	}
	if cn.IssuedAt() == nil || !cn.IssuedAt().Equal(now) {
		t.Errorf("expected issuedAt %v, got %v", now, cn.IssuedAt())
	}
}

func TestCreditNote_Issue_FromNonDraft_Rejected(t *testing.T) {
	cn := newTestCreditNote()
	now := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	_ = cn.Issue(now)

	// Try to issue again
	if err := cn.Issue(now); err == nil {
		t.Error("expected error when issuing non-draft credit note")
	}
}

func TestCreditNote_Apply_FromIssued(t *testing.T) {
	cn := newTestCreditNote()
	now := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	_ = cn.Issue(now)

	if err := cn.Apply(cn.Total()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cn.Status() != CreditNoteStatusApplied {
		t.Errorf("expected status applied, got %s", cn.Status())
	}
	if cn.CreditAmount().Amount().Cmp(cn.Total().Amount()) != 0 {
		t.Errorf("expected creditAmount %s, got %s", cn.Total().Amount().RatString(), cn.CreditAmount().Amount().RatString())
	}
}

func TestCreditNote_Apply_FromDraft_Rejected(t *testing.T) {
	cn := newTestCreditNote()

	if err := cn.Apply(cn.Total()); err == nil {
		t.Error("expected error when applying draft credit note")
	}
}

func TestCreditNote_Apply_ExceedsTotal_Rejected(t *testing.T) {
	cn := newTestCreditNote()
	now := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	_ = cn.Issue(now)

	overAmount := jpy(999999)
	if err := cn.Apply(overAmount); err == nil {
		t.Error("expected error when credit amount exceeds total")
	}
}

func TestCreditNote_Apply_PartialAmount(t *testing.T) {
	cn := newTestCreditNote()
	now := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	_ = cn.Issue(now)

	partial := jpy(1000)
	if err := cn.Apply(partial); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cn.CreditAmount().Amount().Cmp(big.NewRat(1000, 1)) != 0 {
		t.Errorf("expected creditAmount 1000, got %s", cn.CreditAmount().Amount().RatString())
	}
	if cn.Status() != CreditNoteStatusApplied {
		t.Errorf("expected status applied, got %s", cn.Status())
	}
}

func TestCreditNote_Refund_PartialAmount(t *testing.T) {
	cn := newTestCreditNote()
	now := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	_ = cn.Issue(now)

	partial := jpy(2000)
	if err := cn.Refund(partial); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cn.RefundAmount().Amount().Cmp(big.NewRat(2000, 1)) != 0 {
		t.Errorf("expected refundAmount 2000, got %s", cn.RefundAmount().Amount().RatString())
	}
	if cn.Status() != CreditNoteStatusRefunded {
		t.Errorf("expected status refunded, got %s", cn.Status())
	}
}

func TestCreditNote_Refund_FromIssued(t *testing.T) {
	cn := newTestCreditNote()
	now := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	_ = cn.Issue(now)

	if err := cn.Refund(cn.Total()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cn.Status() != CreditNoteStatusRefunded {
		t.Errorf("expected status refunded, got %s", cn.Status())
	}
	if cn.RefundAmount().Amount().Cmp(cn.Total().Amount()) != 0 {
		t.Errorf("expected refundAmount %s, got %s", cn.Total().Amount().RatString(), cn.RefundAmount().Amount().RatString())
	}
}

func TestCreditNote_Refund_FromDraft_Rejected(t *testing.T) {
	cn := newTestCreditNote()

	if err := cn.Refund(cn.Total()); err == nil {
		t.Error("expected error when refunding draft credit note")
	}
}

func TestCreditNote_Refund_ExceedsTotal_Rejected(t *testing.T) {
	cn := newTestCreditNote()
	now := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	_ = cn.Issue(now)

	overAmount := jpy(999999)
	if err := cn.Refund(overAmount); err == nil {
		t.Error("expected error when refund amount exceeds total")
	}
}

func TestCreditNote_Void_FromDraft(t *testing.T) {
	cn := newTestCreditNote()

	if err := cn.Void(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cn.Status() != CreditNoteStatusVoided {
		t.Errorf("expected status voided, got %s", cn.Status())
	}
}

func TestCreditNote_Void_FromIssued(t *testing.T) {
	cn := newTestCreditNote()
	now := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	_ = cn.Issue(now)

	if err := cn.Void(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cn.Status() != CreditNoteStatusVoided {
		t.Errorf("expected status voided, got %s", cn.Status())
	}
}

func TestCreditNote_Void_FromApplied_Rejected(t *testing.T) {
	cn := newTestCreditNote()
	now := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	_ = cn.Issue(now)
	_ = cn.Apply(cn.Total())

	if err := cn.Void(); err == nil {
		t.Error("expected error when voiding applied credit note")
	}
}

func TestCreditNote_Void_FromRefunded_Rejected(t *testing.T) {
	cn := newTestCreditNote()
	now := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	_ = cn.Issue(now)
	_ = cn.Refund(cn.Total())

	if err := cn.Void(); err == nil {
		t.Error("expected error when voiding refunded credit note")
	}
}

func TestCreditNote_Void_FromVoided_Rejected(t *testing.T) {
	cn := newTestCreditNote()
	_ = cn.Void()

	if err := cn.Void(); err == nil {
		t.Error("expected error when voiding already voided credit note")
	}
}

// --- Items defensive copy test ---

func TestCreditNote_Items_ReturnsCopy(t *testing.T) {
	cn := newTestCreditNote()
	items := cn.Items()
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	// Mutating the returned slice should not affect the original
	items[0] = NewCreditNoteItem("mutated", "mutated", jpy(1), big.NewRat(0, 1), jpy(0))
	if cn.Items()[0].InvoiceLineItemID() == "mutated" {
		t.Error("expected items to be a defensive copy")
	}
}
