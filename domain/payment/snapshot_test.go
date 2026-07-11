package payment

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestPayment_Snapshot_RoundTrip(t *testing.T) {
	t.Parallel()

	processedAt := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	p, _ := NewPayment(
		shared.PaymentID("pay-1"),
		shared.InvoiceID("inv-1"),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		PaymentMethodCreditCard,
		"tx-abc",
		processedAt,
	)
	p.SetIdempotencyKey("idem-1")

	snap := p.ToSnapshot()
	// Directly set state only reachable via business rules.
	snap.Status = PaymentStatusPartiallyRefunded
	snap.RefundedAmount = shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	reason := "customer complaint"
	snap.FailureReason = &reason

	restored, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}

	if restored.ID() != shared.PaymentID("pay-1") {
		t.Errorf("ID mismatch: %s", restored.ID())
	}
	if restored.InvoiceID() != shared.InvoiceID("inv-1") {
		t.Errorf("InvoiceID mismatch")
	}
	if restored.Amount().Amount().Cmp(big.NewRat(10000, 1)) != 0 {
		t.Errorf("Amount mismatch")
	}
	if restored.RefundedAmount().Amount().Cmp(big.NewRat(3000, 1)) != 0 {
		t.Errorf("RefundedAmount mismatch: %s", restored.RefundedAmount().Amount().RatString())
	}
	if restored.Method() != PaymentMethodCreditCard {
		t.Errorf("Method mismatch: %s", restored.Method())
	}
	if restored.Status() != PaymentStatusPartiallyRefunded {
		t.Errorf("Status mismatch: %s", restored.Status())
	}
	if restored.GatewayTransactionID() != "tx-abc" {
		t.Errorf("GatewayTransactionID mismatch")
	}
	if restored.IdempotencyKey() != "idem-1" {
		t.Errorf("IdempotencyKey mismatch")
	}
	if restored.FailureReason() == nil || *restored.FailureReason() != "customer complaint" {
		t.Errorf("FailureReason mismatch")
	}
	if !restored.ProcessedAt().Equal(processedAt) {
		t.Errorf("ProcessedAt mismatch")
	}
}

// TestPayment_FromSnapshot_AllowsHistoricalOverRefund verifies that we can
// reconstitute a payment whose refundedAmount exceeds the original amount —
// a state that RecordRefund would reject today but may exist in historical
// DB rows (e.g. from rounding errors in an earlier version of the system,
// or from manual DBA adjustments).
func TestPayment_FromSnapshot_AllowsHistoricalOverRefund(t *testing.T) {
	t.Parallel()

	snap := PaymentSnapshot{
		ID:                   shared.PaymentID("pay-overrefund"),
		InvoiceID:            shared.InvoiceID("inv-1"),
		Amount:               shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		RefundedAmount:       shared.NewMoney(big.NewRat(1001, 1), shared.CurrencyJPY), // > amount
		Method:               PaymentMethodCreditCard,
		Status:               PaymentStatusRefunded,
		GatewayTransactionID: "tx-1",
		ProcessedAt:          time.Now(),
	}
	p, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot should accept historical over-refund: %v", err)
	}
	if p.RefundedAmount().Amount().Cmp(big.NewRat(1001, 1)) != 0 {
		t.Errorf("RefundedAmount not preserved: got %s",
			p.RefundedAmount().Amount().RatString())
	}
}

// TestPayment_FromSnapshot_AllowsRefundedStateDirectly verifies that we
// can restore a Payment in PaymentStatusRefunded state without going
// through MarkRefunded / RecordRefund (which enforce business rules).
func TestPayment_FromSnapshot_AllowsRefundedStateDirectly(t *testing.T) {
	t.Parallel()

	snap := PaymentSnapshot{
		ID:                   shared.PaymentID("pay-1"),
		InvoiceID:            shared.InvoiceID("inv-1"),
		Amount:               shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		RefundedAmount:       shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		Method:               PaymentMethodBankTransfer,
		Status:               PaymentStatusRefunded,
		GatewayTransactionID: "tx-1",
		ProcessedAt:          time.Now(),
	}
	p, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}
	if p.Status() != PaymentStatusRefunded {
		t.Errorf("expected refunded, got %s", p.Status())
	}
}

// TestPayment_Snapshot_PreservesVersion verifies that the optimistic-locking
// version survives a ToSnapshot / FromSnapshot round trip and that loadedVersion
// is restored from the same field (issue #190).
func TestPayment_Snapshot_PreservesVersion(t *testing.T) {
	t.Parallel()

	p, err := NewPayment(
		shared.PaymentID("pay-ver"),
		shared.InvoiceID("inv-1"),
		shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		PaymentMethodCreditCard,
		"tx-1",
		time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewPayment: %v", err)
	}
	// A fresh payment starts at version 0.
	if p.Version() != 0 {
		t.Fatalf("precondition: fresh payment version = %d, want 0", p.Version())
	}
	// Complete + partial refund bump the version twice.
	if err := p.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := p.RecordRefund(shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)); err != nil {
		t.Fatalf("RecordRefund: %v", err)
	}
	want := p.Version()
	if want != 2 {
		t.Fatalf("precondition: expected version 2 after Complete+RecordRefund, got %d", want)
	}

	snap := p.ToSnapshot()
	if snap.Version != want {
		t.Errorf("snapshot Version = %d, want %d", snap.Version, want)
	}

	restored, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}
	if got := restored.Version(); got != want {
		t.Errorf("restored Version = %d, want %d", got, want)
	}
	if got := restored.LoadedVersion(); got != want {
		t.Errorf("restored LoadedVersion = %d, want %d (must be restored from Version)", got, want)
	}
}

func TestPayment_FromSnapshot_ValidatesID(t *testing.T) {
	t.Parallel()

	if _, err := FromSnapshot(PaymentSnapshot{}); err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestPayment_ToSnapshot_IsIndependentCopy(t *testing.T) {
	t.Parallel()

	p, _ := NewPayment(
		shared.PaymentID("pay-1"),
		shared.InvoiceID("inv-1"),
		shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		PaymentMethodCreditCard,
		"tx-1",
		time.Now(),
	)

	snap := p.ToSnapshot()
	snap.Status = PaymentStatusFailed

	if p.Status() == PaymentStatusFailed {
		t.Error("ToSnapshot leaked status reference")
	}
}

// TestPayment_PointerIndependence verifies that pointer/map fields
// (FailureReason, Metadata) are isolated at the Snapshot boundary, in both
// ToSnapshot and FromSnapshot directions.
func TestPayment_PointerIndependence(t *testing.T) {
	t.Parallel()

	// Build a Payment with a failure reason via snapshot round-trip (Fail()
	// alone cannot set the reason on an already-failed payment we create
	// freshly for this test — we go through FromSnapshot).
	reason := "gateway timeout"
	snap := PaymentSnapshot{
		ID:                   shared.PaymentID("pay-1"),
		InvoiceID:            shared.InvoiceID("inv-1"),
		Amount:               shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY),
		RefundedAmount:       shared.Zero(shared.CurrencyJPY),
		Method:               PaymentMethodCreditCard,
		Status:               PaymentStatusFailed,
		GatewayTransactionID: "tx-1",
		FailureReason:        &reason,
		ProcessedAt:          time.Now(),
		Metadata:             map[string]string{"k": "v"},
	}
	p, err := FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot: %v", err)
	}

	// ToSnapshot: mutate snapshot, entity must be unaffected.
	out := p.ToSnapshot()
	*out.FailureReason = "mutated"
	out.Metadata["leak"] = "yes"
	if p.FailureReason() != nil && *p.FailureReason() == "mutated" {
		t.Error("ToSnapshot: FailureReason pointer was shared")
	}
	if _, leaked := p.Metadata()["leak"]; leaked {
		t.Error("ToSnapshot: Metadata map was shared")
	}

	// FromSnapshot: mutate original source snapshot, reconstructed entity
	// must be unaffected.
	*snap.FailureReason = "mutated-src"
	snap.Metadata["leak-src"] = "yes"
	if p.FailureReason() != nil && *p.FailureReason() == "mutated-src" {
		t.Error("FromSnapshot: FailureReason pointer was shared")
	}
	if _, leaked := p.Metadata()["leak-src"]; leaked {
		t.Error("FromSnapshot: Metadata map was shared")
	}
}
