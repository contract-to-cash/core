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
	p := NewPayment(
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

func TestPayment_FromSnapshot_ValidatesID(t *testing.T) {
	t.Parallel()

	if _, err := FromSnapshot(PaymentSnapshot{}); err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestPayment_ToSnapshot_IsIndependentCopy(t *testing.T) {
	t.Parallel()

	p := NewPayment(
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
