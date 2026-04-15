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

	restored, err := InvoiceFromSnapshot(snap)
	if err != nil {
		t.Fatalf("InvoiceFromSnapshot: %v", err)
	}

	// --- scalar and ID fields ---
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

	// --- money fields (all 8) ---
	if restored.Subtotal().Amount().Cmp(subtotal.Amount()) != 0 {
		t.Errorf("Subtotal mismatch: got %s", restored.Subtotal().Amount().RatString())
	}
	if restored.DiscountAmount().Amount().Cmp(discount.Amount()) != 0 {
		t.Errorf("DiscountAmount mismatch: got %s", restored.DiscountAmount().Amount().RatString())
	}
	if restored.TaxAmount().Amount().Cmp(tax.Amount()) != 0 {
		t.Errorf("TaxAmount mismatch: got %s", restored.TaxAmount().Amount().RatString())
	}
	// Total is the auto-computed (subtotal - discount + tax) stored in the snapshot.
	expectedTotal := big.NewRat(10450, 1) // 10000 - 500 + 950
	if restored.Total().Amount().Cmp(expectedTotal) != 0 {
		t.Errorf("Total mismatch: got %s want %s",
			restored.Total().Amount().RatString(), expectedTotal.RatString())
	}
	if restored.AppliedBalance().Amount().Cmp(big.NewRat(0, 1)) != 0 {
		t.Errorf("AppliedBalance mismatch: got %s", restored.AppliedBalance().Amount().RatString())
	}
	if restored.AmountDue().Amount().Cmp(expectedTotal) != 0 {
		t.Errorf("AmountDue mismatch: got %s", restored.AmountDue().Amount().RatString())
	}
	if restored.PaidAmount().Amount().Cmp(big.NewRat(10450, 1)) != 0 {
		t.Errorf("PaidAmount mismatch: %s", restored.PaidAmount().Amount().RatString())
	}
	if !restored.Balance().IsZero() {
		t.Errorf("Balance mismatch: got %s", restored.Balance().Amount().RatString())
	}

	// --- time fields ---
	if restored.PaidAt() == nil || !restored.PaidAt().Equal(paidAt) {
		t.Errorf("PaidAt mismatch: %v", restored.PaidAt())
	}
	if !restored.IssueDate().Equal(time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("IssueDate mismatch: %v", restored.IssueDate())
	}
	if !restored.DueDate().Equal(time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("DueDate mismatch: %v", restored.DueDate())
	}
	if !restored.BillingPeriod().Equals(period) {
		t.Errorf("BillingPeriod mismatch")
	}

	// --- optional / pointer fields ---
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
	if restored.VoidReason() != "" {
		t.Errorf("VoidReason mismatch: %q", restored.VoidReason())
	}
	if meta := restored.Metadata(); meta["k"] != "v" {
		t.Errorf("Metadata not restored: %v", meta)
	}

	// --- line items: verify every LineItem field ---
	items := restored.LineItems()
	if len(items) != 1 {
		t.Fatalf("LineItems length: got %d want 1", len(items))
	}
	got := items[0]
	if got.ID() != "li-1" {
		t.Errorf("LineItem.ID: got %s", got.ID())
	}
	if got.Description() != "item" {
		t.Errorf("LineItem.Description: got %s", got.Description())
	}
	if got.Quantity() != 2 {
		t.Errorf("LineItem.Quantity: got %d", got.Quantity())
	}
	if got.UnitPrice().Amount().Cmp(big.NewRat(5000, 1)) != 0 {
		t.Errorf("LineItem.UnitPrice: got %s", got.UnitPrice().Amount().RatString())
	}
	if got.Amount().Amount().Cmp(big.NewRat(10000, 1)) != 0 {
		t.Errorf("LineItem.Amount: got %s", got.Amount().Amount().RatString())
	}
	if got.TaxRate() == nil || got.TaxRate().Cmp(big.NewRat(10, 100)) != 0 {
		t.Errorf("LineItem.TaxRate: got %v", got.TaxRate())
	}
	if got.PriceID() != shared.PriceID("price-1") {
		t.Errorf("LineItem.PriceID: got %s", got.PriceID())
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

	inv, err := InvoiceFromSnapshot(snap)
	if err != nil {
		t.Fatalf("InvoiceFromSnapshot: %v", err)
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

	inv, err := InvoiceFromSnapshot(snap)
	if err != nil {
		t.Fatalf("InvoiceFromSnapshot: %v", err)
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
	if _, err := InvoiceFromSnapshot(snap); err == nil {
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

// TestInvoice_ToSnapshot_PointerIndependence verifies that pointer fields
// at the Snapshot boundary are isolated: mutating the snapshot's pointer
// fields (directly or transitively) must not affect the source invoice.
//
// Note: this tests the Snapshot boundary. Entity-getter pointer isolation
// is tested independently in pointer_isolation_test.go (see issue #96).
func TestInvoice_ToSnapshot_PointerIndependence(t *testing.T) {
	t.Parallel()

	taxRate := big.NewRat(10, 100)
	li, err := NewLineItem(
		"li-1", "x", 1,
		shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		taxRate,
	)
	if err != nil {
		t.Fatalf("NewLineItem: %v", err)
	}

	paidAt := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	pmID := "pm-1"
	origID := shared.InvoiceID("orig")
	revOf := shared.InvoiceID("prev")

	inv, err := NewInvoice(
		shared.InvoiceID("inv-1"),
		shared.AccountID("acc-1"),
		shared.ContractID("ctr-1"),
		shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithLineItems([]LineItem{li}),
		WithPaymentMethodID(&pmID),
		WithOriginalInvoiceID(origID),
		WithRevisionOf(revOf),
	)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
	}
	// Manually set paidAt through snapshot round-trip (no public mutator).
	tmp := inv.ToSnapshot()
	tmp.PaidAt = &paidAt
	inv, err = InvoiceFromSnapshot(tmp)
	if err != nil {
		t.Fatalf("InvoiceFromSnapshot: %v", err)
	}

	snap := inv.ToSnapshot()

	// LineItem-nested *big.Rat must not be shared.
	snap.LineItems[0].TaxRate.SetInt64(999)
	if got := inv.LineItems()[0].TaxRate().RatString(); got == "999" {
		t.Errorf("LineItem.TaxRate pointer was shared: snapshot mutation leaked to entity (%s)", got)
	}

	// LineItem-nested metadata map must not be shared.
	snap2 := inv.ToSnapshot()
	snap2.LineItems[0].Metadata["leak"] = "yes"
	if _, leaked := inv.LineItems()[0].Metadata()["leak"]; leaked {
		t.Error("LineItem.Metadata map was shared")
	}

	// Top-level *time.Time must not be shared.
	snap3 := inv.ToSnapshot()
	*snap3.PaidAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	if inv.PaidAt() != nil && inv.PaidAt().Year() == 2099 {
		t.Error("Invoice.PaidAt pointer was shared")
	}

	// Top-level *string must not be shared.
	snap4 := inv.ToSnapshot()
	*snap4.PaymentMethodID = "mutated"
	if inv.PaymentMethodID() != nil && *inv.PaymentMethodID() == "mutated" {
		t.Error("Invoice.PaymentMethodID pointer was shared")
	}

	// Top-level *shared.InvoiceID must not be shared.
	snap5 := inv.ToSnapshot()
	*snap5.OriginalInvoiceID = shared.InvoiceID("mutated")
	if inv.OriginalInvoiceID() != nil && *inv.OriginalInvoiceID() == shared.InvoiceID("mutated") {
		t.Error("Invoice.OriginalInvoiceID pointer was shared")
	}

	snap6 := inv.ToSnapshot()
	*snap6.RevisionOf = shared.InvoiceID("mutated")
	if inv.RevisionOf() != nil && *inv.RevisionOf() == shared.InvoiceID("mutated") {
		t.Error("Invoice.RevisionOf pointer was shared")
	}
}

// TestInvoice_FromSnapshot_PointerIndependence verifies that after
// FromSnapshot, mutating the original snapshot's pointer fields does not
// affect the reconstructed invoice. This protects adapters that build a
// snapshot from DB rows and keep it around for logging/retry.
func TestInvoice_FromSnapshot_PointerIndependence(t *testing.T) {
	t.Parallel()

	taxRate := big.NewRat(10, 100)
	paidAt := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	pmID := "pm-1"
	origID := shared.InvoiceID("orig")
	revOf := shared.InvoiceID("prev")

	snap := InvoiceSnapshot{
		ID:                shared.InvoiceID("inv-1"),
		AccountID:         shared.AccountID("acc-1"),
		ContractID:        shared.ContractID("ctr-1"),
		Subtotal:          shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		DiscountAmount:    shared.Zero(shared.CurrencyJPY),
		TaxAmount:         shared.Zero(shared.CurrencyJPY),
		Total:             shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		AmountDue:         shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		PaidAmount:        shared.Zero(shared.CurrencyJPY),
		Balance:           shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		AppliedBalance:    shared.Zero(shared.CurrencyJPY),
		Status:            InvoiceStatusPaid,
		PaidAt:            &paidAt,
		PaymentMethodID:   &pmID,
		OriginalInvoiceID: &origID,
		RevisionOf:        &revOf,
		LineItems: []LineItemSnapshot{
			{
				ID:          "li-1",
				Description: "x",
				Quantity:    1,
				UnitPrice:   shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
				Amount:      shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
				TaxRate:     taxRate,
				Metadata:    map[string]string{"k": "v"},
			},
		},
		Metadata: map[string]string{"k": "v"},
	}

	inv, err := InvoiceFromSnapshot(snap)
	if err != nil {
		t.Fatalf("InvoiceFromSnapshot: %v", err)
	}

	// Mutate the original snapshot's pointers/maps/slices.
	*snap.PaidAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	*snap.PaymentMethodID = "mutated"
	*snap.OriginalInvoiceID = shared.InvoiceID("mutated")
	*snap.RevisionOf = shared.InvoiceID("mutated")
	snap.LineItems[0].TaxRate.SetInt64(999)
	snap.LineItems[0].Metadata["leak"] = "yes"
	snap.Metadata["leak"] = "yes"

	// The reconstructed invoice must not be affected.
	if inv.PaidAt() == nil || inv.PaidAt().Year() != 2026 {
		t.Errorf("InvoiceFromSnapshot: PaidAt pointer was shared (year=%v)", inv.PaidAt())
	}
	if inv.PaymentMethodID() == nil || *inv.PaymentMethodID() != "pm-1" {
		t.Errorf("InvoiceFromSnapshot: PaymentMethodID pointer was shared: %v", inv.PaymentMethodID())
	}
	if inv.OriginalInvoiceID() == nil || *inv.OriginalInvoiceID() != shared.InvoiceID("orig") {
		t.Errorf("InvoiceFromSnapshot: OriginalInvoiceID pointer was shared: %v", inv.OriginalInvoiceID())
	}
	if inv.RevisionOf() == nil || *inv.RevisionOf() != shared.InvoiceID("prev") {
		t.Errorf("InvoiceFromSnapshot: RevisionOf pointer was shared: %v", inv.RevisionOf())
	}
	if got := inv.LineItems()[0].TaxRate().RatString(); got == "999" {
		t.Errorf("InvoiceFromSnapshot: LineItem.TaxRate pointer was shared: %s", got)
	}
	if _, leaked := inv.LineItems()[0].Metadata()["leak"]; leaked {
		t.Error("InvoiceFromSnapshot: LineItem.Metadata map was shared")
	}
	if _, leaked := inv.Metadata()["leak"]; leaked {
		t.Error("InvoiceFromSnapshot: Invoice.Metadata map was shared")
	}
}
