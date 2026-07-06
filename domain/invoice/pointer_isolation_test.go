// pointer_isolation_test.go verifies that entity getters and construction
// options do NOT leak internal pointer state. This aligns invoice-domain
// entities with the domain/shared/money.go precedent of value-semantics +
// pointer isolation. See issue #96.
package invoice

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// --- WithLineItems intake copy (issue #162 L-6) ---

// TestWithLineItems_IntakeIsDefensivelyCopied verifies that mutating the
// caller's line-item slice after passing it to NewInvoice via WithLineItems does
// NOT alter the invoice's stored line items.
func TestWithLineItems_IntakeIsDefensivelyCopied(t *testing.T) {
	li, err := NewLineItem("li-1", "Item", 1, jpy(1000), jpy(1000), nil)
	if err != nil {
		t.Fatalf("NewLineItem: %v", err)
	}
	items := []LineItem{li}

	inv, err := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		jpy(1000), jpy(0), jpy(0),
		WithLineItems(items),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}

	// Overwrite the caller's backing array element after construction.
	tampered, err := NewLineItem("hacked", "tampered", 1, jpy(999999), jpy(999999), nil)
	if err != nil {
		t.Fatalf("NewLineItem: %v", err)
	}
	items[0] = tampered

	got := inv.LineItems()
	if len(got) != 1 {
		t.Fatalf("expected 1 line item, got %d", len(got))
	}
	if got[0].ID() != "li-1" {
		t.Errorf("WithLineItems does not defend slice at intake: got id %q, want li-1", got[0].ID())
	}
}

// --- Revision-chain self-reference guards (issue #162 L-9) ---

// TestSetRevisionOf_RejectsSelfReference verifies that linking an invoice as a
// revision of itself is a no-op (no mutation, no version bump) while a real
// parent link is applied and bumps the version.
func TestSetRevisionOf_RejectsSelfReference(t *testing.T) {
	id := shared.NewInvoiceID()
	inv, err := NewInvoice(id, shared.NewAccountID(), shared.NewContractID(),
		jpy(1000), jpy(0), jpy(0))
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}

	v0 := inv.Version()
	inv.SetRevisionOf(id) // self-reference — must no-op
	if inv.RevisionOf() != nil {
		t.Errorf("SetRevisionOf(self) must not set revisionOf, got %v", inv.RevisionOf())
	}
	if inv.Version() != v0 {
		t.Errorf("SetRevisionOf(self) must not bump version: got %d, want %d", inv.Version(), v0)
	}

	parent := shared.NewInvoiceID()
	inv.SetRevisionOf(parent)
	if inv.RevisionOf() == nil || *inv.RevisionOf() != parent {
		t.Errorf("SetRevisionOf(parent) must set revisionOf to %s, got %v", parent, inv.RevisionOf())
	}
	if inv.Version() != v0+1 {
		t.Errorf("SetRevisionOf(parent) must bump version to %d, got %d", v0+1, inv.Version())
	}
}

// TestSetOriginalInvoiceID_RejectsSelfReference mirrors the revision-of guard
// for the chain-root link.
func TestSetOriginalInvoiceID_RejectsSelfReference(t *testing.T) {
	id := shared.NewInvoiceID()
	inv, err := NewInvoice(id, shared.NewAccountID(), shared.NewContractID(),
		jpy(1000), jpy(0), jpy(0))
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}

	v0 := inv.Version()
	inv.SetOriginalInvoiceID(id) // self-reference — must no-op
	if inv.OriginalInvoiceID() != nil {
		t.Errorf("SetOriginalInvoiceID(self) must not set originalInvoiceID, got %v", inv.OriginalInvoiceID())
	}
	if inv.Version() != v0 {
		t.Errorf("SetOriginalInvoiceID(self) must not bump version: got %d, want %d", inv.Version(), v0)
	}

	root := shared.NewInvoiceID()
	inv.SetOriginalInvoiceID(root)
	if inv.OriginalInvoiceID() == nil || *inv.OriginalInvoiceID() != root {
		t.Errorf("SetOriginalInvoiceID(root) must set originalInvoiceID to %s, got %v", root, inv.OriginalInvoiceID())
	}
	if inv.Version() != v0+1 {
		t.Errorf("SetOriginalInvoiceID(root) must bump version to %d, got %d", v0+1, inv.Version())
	}
}

// --- LineItem ---

// TestLineItem_TaxRate_GetterIsDefensivelyCopied verifies that mutating the
// pointer returned by LineItem.TaxRate() does NOT alter the line item's
// internal state. This matches the Money precedent.
func TestLineItem_TaxRate_GetterIsDefensivelyCopied(t *testing.T) {
	taxRate := big.NewRat(10, 100) // 10%
	li, err := NewLineItem("li-1", "Item", 1, jpy(1000), jpy(1000), taxRate)
	if err != nil {
		t.Fatalf("NewLineItem: %v", err)
	}

	// Attempt to corrupt the internal taxRate via the getter.
	li.TaxRate().SetInt64(999)

	got := li.TaxRate()
	if got == nil {
		t.Fatal("TaxRate must not be nil")
	}
	if got.Cmp(big.NewRat(10, 100)) != 0 {
		t.Errorf("LineItem.TaxRate() leaks internal pointer: got %s, want 10/100", got.RatString())
	}
}

// TestLineItem_TaxRate_GetterNilSafe verifies that a nil taxRate is returned
// as nil (and the getter does not panic).
func TestLineItem_TaxRate_GetterNilSafe(t *testing.T) {
	li, err := NewLineItem("li-1", "Item", 1, jpy(1000), jpy(1000), nil)
	if err != nil {
		t.Fatalf("NewLineItem: %v", err)
	}
	if li.TaxRate() != nil {
		t.Errorf("expected nil TaxRate, got %v", li.TaxRate())
	}
}

// TestNewLineItem_TaxRate_IntakeIsDefensivelyCopied verifies that mutating
// the caller's taxRate after passing it to NewLineItem does NOT alter the
// line item's internal state. Pattern C (intake defense).
func TestNewLineItem_TaxRate_IntakeIsDefensivelyCopied(t *testing.T) {
	taxRate := big.NewRat(10, 100)
	li, err := NewLineItem("li-1", "Item", 1, jpy(1000), jpy(1000), taxRate)
	if err != nil {
		t.Fatalf("NewLineItem: %v", err)
	}

	// Mutate the caller-owned rat after construction.
	taxRate.SetInt64(999)

	got := li.TaxRate()
	if got == nil {
		t.Fatal("TaxRate must not be nil")
	}
	if got.Cmp(big.NewRat(10, 100)) != 0 {
		t.Errorf("NewLineItem does not defend taxRate at intake: got %s, want 10/100", got.RatString())
	}
}

// --- Invoice.PaidAt ---

func TestInvoice_PaidAt_GetterIsDefensivelyCopied(t *testing.T) {
	subtotal := jpy(10000)
	inv, err := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		subtotal,
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}
	if err := inv.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	paidAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := inv.RecordPayment(subtotal, paidAt); err != nil {
		t.Fatalf("RecordPayment: %v", err)
	}

	// Attempt to corrupt the internal paidAt via the getter.
	got := inv.PaidAt()
	if got == nil {
		t.Fatal("PaidAt must not be nil")
	}
	*got = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

	again := inv.PaidAt()
	if again == nil || !again.Equal(paidAt) {
		t.Errorf("Invoice.PaidAt() leaks internal pointer: got %v, want %v", again, paidAt)
	}
}

// --- Invoice.PaymentMethodID ---

func TestInvoice_PaymentMethodID_GetterIsDefensivelyCopied(t *testing.T) {
	id := "pm-visa-1234"
	inv, err := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		jpy(10000),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithPaymentMethodID(&id),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}

	// Corrupt via getter.
	got := inv.PaymentMethodID()
	if got == nil {
		t.Fatal("PaymentMethodID must not be nil")
	}
	*got = "pm-hacked"

	again := inv.PaymentMethodID()
	if again == nil || *again != "pm-visa-1234" {
		t.Errorf("Invoice.PaymentMethodID() leaks internal pointer: got %v, want pm-visa-1234", again)
	}
}

// TestWithPaymentMethodID_IntakeIsDefensivelyCopied verifies that mutating
// the caller's *string after passing it to WithPaymentMethodID does NOT
// alter the invoice's internal state. Pattern C.
func TestWithPaymentMethodID_IntakeIsDefensivelyCopied(t *testing.T) {
	id := "pm-visa-1234"
	inv, err := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		jpy(10000),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithPaymentMethodID(&id),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}

	// Mutate caller's variable.
	id = "pm-hacked"

	got := inv.PaymentMethodID()
	if got == nil || *got != "pm-visa-1234" {
		t.Errorf("WithPaymentMethodID does not defend at intake: got %v, want pm-visa-1234", got)
	}
}

// --- Invoice.OriginalInvoiceID ---

func TestInvoice_OriginalInvoiceID_GetterIsDefensivelyCopied(t *testing.T) {
	origID := shared.InvoiceID("inv-orig")
	inv, err := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		jpy(10000),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithOriginalInvoiceID(origID),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}

	got := inv.OriginalInvoiceID()
	if got == nil {
		t.Fatal("OriginalInvoiceID must not be nil")
	}
	*got = shared.InvoiceID("hacked")

	again := inv.OriginalInvoiceID()
	if again == nil || *again != origID {
		t.Errorf("Invoice.OriginalInvoiceID() leaks internal pointer: got %v, want %s", again, origID)
	}
}

// --- Invoice.RevisionOf ---

func TestInvoice_RevisionOf_GetterIsDefensivelyCopied(t *testing.T) {
	revOf := shared.InvoiceID("inv-prev")
	inv, err := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		jpy(10000),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithRevisionOf(revOf),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}

	got := inv.RevisionOf()
	if got == nil {
		t.Fatal("RevisionOf must not be nil")
	}
	*got = shared.InvoiceID("hacked")

	again := inv.RevisionOf()
	if again == nil || *again != revOf {
		t.Errorf("Invoice.RevisionOf() leaks internal pointer: got %v, want %s", again, revOf)
	}
}

// --- Invoice.LineItems()[i].TaxRate (reproducing the exact issue example) ---

// --- Nil-safety tests for Invoice pointer getters ---

// TestInvoice_OptionalPointers_NilByDefault verifies that Invoice getters
// for optional *T fields correctly return nil when unset, and that the
// defensive-copy wrappers do not panic on nil internals.
func TestInvoice_OptionalPointers_NilByDefault(t *testing.T) {
	inv, err := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		jpy(10000),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}

	if got := inv.PaidAt(); got != nil {
		t.Errorf("expected nil PaidAt, got %v", got)
	}
	if got := inv.PaymentMethodID(); got != nil {
		t.Errorf("expected nil PaymentMethodID, got %v", got)
	}
	if got := inv.OriginalInvoiceID(); got != nil {
		t.Errorf("expected nil OriginalInvoiceID, got %v", got)
	}
	if got := inv.RevisionOf(); got != nil {
		t.Errorf("expected nil RevisionOf, got %v", got)
	}
}

// TestWithPaymentMethodID_NilInput verifies that passing nil to
// WithPaymentMethodID yields a nil PaymentMethodID (not a panic).
func TestWithPaymentMethodID_NilInput(t *testing.T) {
	inv, err := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		jpy(10000),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithPaymentMethodID(nil),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}
	if got := inv.PaymentMethodID(); got != nil {
		t.Errorf("expected nil PaymentMethodID for nil option, got %v", got)
	}
}

// TestInvoice_LineItems_TaxRate_DoesNotCorruptOriginal reproduces the
// issue #96 example:
//
//	inv.LineItems()[0].TaxRate().SetInt64(999)
//
// must NOT mutate the internal state of the invoice's line item.
func TestInvoice_LineItems_TaxRate_DoesNotCorruptOriginal(t *testing.T) {
	taxRate := big.NewRat(10, 100)
	li, err := NewLineItem("li-1", "Item", 1, jpy(1000), jpy(1000), taxRate)
	if err != nil {
		t.Fatalf("NewLineItem: %v", err)
	}
	inv, err := NewInvoice(
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		jpy(1000),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithLineItems([]LineItem{li}),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}

	inv.LineItems()[0].TaxRate().SetInt64(999)

	got := inv.LineItems()[0].TaxRate()
	if got == nil || got.Cmp(big.NewRat(10, 100)) != 0 {
		t.Errorf("Invoice line item TaxRate corrupted via getter chain: got %v", got)
	}
}
