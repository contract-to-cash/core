package invoice

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// TestInvoice_Snapshot_RoundTrip verifies that ToSnapshot followed by
// FromSnapshot restores every field exactly. This is the primary contract
// of the Snapshot/Reconstruct pattern.
func TestInvoice_Snapshot_RoundTrip(t *testing.T) {
	t.Parallel()

	period, err := shared.NewDateRange(
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewDateRange: %v", err)
	}

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	discount := shared.NewMoney(big.NewRat(500, 1), shared.CurrencyJPY)
	tax := shared.NewMoney(big.NewRat(950, 1), shared.CurrencyJPY)

	li, err := NewLineItem(
		"li-1", "item", 2,
		shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		big.NewRat(10, 100),
		WithPriceID(shared.PriceID("price-1")),
	)
	if err != nil {
		t.Fatalf("NewLineItem: %v", err)
	}

	paidAt := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	pmID := "pm-1"
	origID := shared.InvoiceID("inv-root")
	revOf := shared.InvoiceID("inv-prev")

	inv, err := NewInvoice(
		shared.InvoiceID("inv-1"),
		shared.AccountID("acc-1"),
		shared.ContractID("ctr-1"),
		subtotal, discount, tax,
		WithInvoiceNumber("INV-2026-001"),
		WithStatus(InvoiceStatusPaid),
		WithBillingPeriod(period),
		WithDueDate(time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)),
		WithIssueDate(time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)),
		WithLineItems([]LineItem{li}),
		WithAllowPartialPayment(true),
		WithPaymentMethodID(&pmID),
		WithOriginalInvoiceID(origID),
		WithRevisionOf(revOf),
		WithMetadata(map[string]string{"k": "v"}),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}
	// Record payment via the domain method so that paidAmount/balance/paidAt
	// are set through the legitimate construction path.
	// Note: NewInvoice with WithStatus(InvoiceStatusPaid) still leaves paidAmount=0,
	// so we simulate a complete life-cycle by creating a finalized invoice first.
	// For round-trip testing purposes, we build state via direct snapshot manipulation.

	snap := inv.ToSnapshot()
	// Manually set fields that the public API cannot reach, to exercise full round-trip.
	snap.Status = InvoiceStatusPaid
	snap.PaidAmount = shared.NewMoney(big.NewRat(10450, 1), shared.CurrencyJPY)
	snap.Balance = shared.Zero(shared.CurrencyJPY)
	snap.PaidAt = &paidAt
	snap.VoidReason = ""

	restored, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}

	if restored.ID() != inv.ID() {
		t.Errorf("ID: got %s want %s", restored.ID(), inv.ID())
	}
	if restored.InvoiceNumber() != "INV-2026-001" {
		t.Errorf("InvoiceNumber mismatch: %s", restored.InvoiceNumber())
	}
	if restored.AccountID() != shared.AccountID("acc-1") {
		t.Errorf("AccountID mismatch: %s", restored.AccountID())
	}
	if restored.ContractID() != shared.ContractID("ctr-1") {
		t.Errorf("ContractID mismatch: %s", restored.ContractID())
	}
	if restored.Status() != InvoiceStatusPaid {
		t.Errorf("Status: got %s want paid", restored.Status())
	}
	if restored.Subtotal().Amount().Cmp(subtotal.Amount()) != 0 {
		t.Errorf("Subtotal mismatch")
	}
	if restored.DiscountAmount().Amount().Cmp(discount.Amount()) != 0 {
		t.Errorf("DiscountAmount mismatch")
	}
	if restored.TaxAmount().Amount().Cmp(tax.Amount()) != 0 {
		t.Errorf("TaxAmount mismatch")
	}
	if restored.PaidAmount().Amount().Cmp(big.NewRat(10450, 1)) != 0 {
		t.Errorf("PaidAmount mismatch: %s", restored.PaidAmount().Amount().RatString())
	}
	if restored.PaidAt() == nil || !restored.PaidAt().Equal(paidAt) {
		t.Errorf("PaidAt mismatch: %v", restored.PaidAt())
	}
	if !restored.AllowPartialPay() {
		t.Errorf("AllowPartialPay not restored")
	}
	if restored.PaymentMethodID() == nil || *restored.PaymentMethodID() != "pm-1" {
		t.Errorf("PaymentMethodID not restored")
	}
	if restored.OriginalInvoiceID() == nil || *restored.OriginalInvoiceID() != origID {
		t.Errorf("OriginalInvoiceID not restored")
	}
	if restored.RevisionOf() == nil || *restored.RevisionOf() != revOf {
		t.Errorf("RevisionOf not restored")
	}
	if meta := restored.Metadata(); meta["k"] != "v" {
		t.Errorf("Metadata not restored: %v", meta)
	}
	if items := restored.LineItems(); len(items) != 1 || items[0].ID() != "li-1" {
		t.Errorf("LineItems not restored: %+v", items)
	}
	if !restored.BillingPeriod().Equals(period) {
		t.Errorf("BillingPeriod mismatch")
	}
}

// TestInvoice_FromSnapshot_RestoresRefundedStatus verifies the primary
// motivation of the Snapshot pattern: reconstituting a status that has no
// public mutator in the domain. Previously, InvoiceStatusRefunded was
// unreachable from the public API.
func TestInvoice_FromSnapshot_RestoresRefundedStatus(t *testing.T) {
	t.Parallel()

	snap := InvoiceSnapshot{
		ID:             shared.InvoiceID("inv-refund"),
		AccountID:      shared.AccountID("acc-1"),
		ContractID:     shared.ContractID("ctr-1"),
		Status:         InvoiceStatusRefunded,
		Subtotal:       shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		TaxAmount:      shared.Zero(shared.CurrencyJPY),
		DiscountAmount: shared.Zero(shared.CurrencyJPY),
		Total:          shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		AmountDue:      shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		PaidAmount:     shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		Balance:        shared.Zero(shared.CurrencyJPY),
		AppliedBalance: shared.Zero(shared.CurrencyJPY),
	}

	inv, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}
	if inv.Status() != InvoiceStatusRefunded {
		t.Errorf("expected refunded status, got %s", inv.Status())
	}
}

// TestInvoice_FromSnapshot_DoesNotRerunBusinessRules verifies that
// reconstituting an invoice whose total would violate NewInvoice's
// recalculation rules still succeeds, because FromSnapshot must not
// re-run construction-time invariants.
func TestInvoice_FromSnapshot_DoesNotRerunBusinessRules(t *testing.T) {
	t.Parallel()

	// Deliberately supply a total that does NOT match subtotal-discount+tax.
	// NewInvoice would overwrite this; FromSnapshot must preserve it.
	snap := InvoiceSnapshot{
		ID:             shared.InvoiceID("inv-historical"),
		AccountID:      shared.AccountID("acc-1"),
		ContractID:     shared.ContractID("ctr-1"),
		Status:         InvoiceStatusPaid,
		Subtotal:       shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		DiscountAmount: shared.Zero(shared.CurrencyJPY),
		TaxAmount:      shared.Zero(shared.CurrencyJPY),
		// Total deliberately != subtotal - discount + tax
		Total:          shared.NewMoney(big.NewRat(999, 1), shared.CurrencyJPY),
		AmountDue:      shared.NewMoney(big.NewRat(999, 1), shared.CurrencyJPY),
		PaidAmount:     shared.NewMoney(big.NewRat(999, 1), shared.CurrencyJPY),
		Balance:        shared.Zero(shared.CurrencyJPY),
		AppliedBalance: shared.Zero(shared.CurrencyJPY),
	}

	inv, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}
	// The restored Total must be exactly what the snapshot said, not recalculated.
	if inv.Total().Amount().Cmp(big.NewRat(999, 1)) != 0 {
		t.Errorf("Total was recalculated: got %s want 999",
			inv.Total().Amount().RatString())
	}
}

// TestInvoice_FromSnapshot_ValidatesID checks that the minimal
// persistence-layer invariant (non-empty ID) is enforced.
func TestInvoice_FromSnapshot_ValidatesID(t *testing.T) {
	t.Parallel()

	snap := InvoiceSnapshot{
		ID: shared.InvoiceID(""),
	}
	if _, err := FromSnapshot(snap); err == nil {
		t.Error("expected error for empty ID")
	}
}

// TestInvoice_ToSnapshot_IsIndependentCopy verifies that mutating the
// returned snapshot does not affect the source invoice.
func TestInvoice_ToSnapshot_IsIndependentCopy(t *testing.T) {
	t.Parallel()

	inv, err := NewInvoice(
		shared.InvoiceID("inv-1"),
		shared.AccountID("acc-1"),
		shared.ContractID("ctr-1"),
		shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithMetadata(map[string]string{"k": "v"}),
		WithLineItems([]LineItem{}),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}

	snap := inv.ToSnapshot()
	snap.Metadata["k"] = "mutated"
	snap.Status = InvoiceStatusVoided

	if inv.Metadata()["k"] != "v" {
		t.Error("ToSnapshot leaked metadata reference")
	}
	if inv.Status() == InvoiceStatusVoided {
		t.Error("ToSnapshot leaked status reference")
	}
}
