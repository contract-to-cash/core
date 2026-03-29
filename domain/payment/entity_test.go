package payment

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestNewPayment(t *testing.T) {
	id := shared.NewPaymentID()
	invoiceID := shared.NewInvoiceID()
	amount := shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY)
	method := PaymentMethodCreditCard
	gatewayTxID := "gw_tx_12345"
	processedAt := time.Now().UTC()

	p := NewPayment(id, invoiceID, amount, method, gatewayTxID, processedAt)

	if p.ID() != id {
		t.Errorf("expected id %s, got %s", id, p.ID())
	}
	if p.InvoiceID() != invoiceID {
		t.Errorf("expected invoiceID %s, got %s", invoiceID, p.InvoiceID())
	}
	if p.Amount().Amount().Cmp(big.NewRat(5000, 1)) != 0 {
		t.Errorf("expected amount 5000, got %s", p.Amount().Amount().RatString())
	}
	if p.Method() != PaymentMethodCreditCard {
		t.Errorf("expected method credit_card, got %s", p.Method())
	}
	if p.Status() != PaymentStatusPending {
		t.Errorf("expected status pending, got %s", p.Status())
	}
	if p.GatewayTransactionID() != gatewayTxID {
		t.Errorf("expected gateway tx id %s, got %s", gatewayTxID, p.GatewayTransactionID())
	}
	if p.FailureReason() != nil {
		t.Error("expected failure reason to be nil")
	}
	if p.ProcessedAt() != processedAt {
		t.Errorf("expected processedAt %v, got %v", processedAt, p.ProcessedAt())
	}
	if p.Metadata() == nil {
		t.Error("expected metadata to be initialized")
	}
}

func TestPaymentStatus_Constants(t *testing.T) {
	statuses := []PaymentStatus{
		PaymentStatusPending,
		PaymentStatusCompleted,
		PaymentStatusFailed,
		PaymentStatusPartiallyRefunded,
		PaymentStatusRefunded,
		PaymentStatusChargedBack,
	}

	expected := []string{
		"pending", "completed", "failed",
		"partially_refunded", "refunded", "charged_back",
	}

	for i, s := range statuses {
		if string(s) != expected[i] {
			t.Errorf("expected %s, got %s", expected[i], s)
		}
	}
}

func newTestPayment() *Payment {
	return NewPayment(
		shared.NewPaymentID(),
		shared.NewInvoiceID(),
		shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY),
		PaymentMethodCreditCard,
		"gw_tx_test",
		time.Now().UTC(),
	)
}

func completePayment(t *testing.T, p *Payment) {
	t.Helper()
	if err := p.Complete(); err != nil {
		t.Fatalf("setup: failed to complete payment: %v", err)
	}
}

func TestPayment_Complete(t *testing.T) {
	p := newTestPayment()

	if err := p.Complete(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusCompleted {
		t.Errorf("expected completed, got %s", p.Status())
	}
}

func TestPayment_Complete_InvalidState(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	if err := p.Complete(); err == nil {
		t.Error("expected error completing already completed payment")
	}
}

func TestPayment_Fail(t *testing.T) {
	p := newTestPayment()
	reason := "insufficient_funds"

	if err := p.Fail(reason); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusFailed {
		t.Errorf("expected failed, got %s", p.Status())
	}
	if p.FailureReason() == nil || *p.FailureReason() != reason {
		t.Errorf("expected failure reason %q", reason)
	}
}

func TestPayment_Fail_InvalidState(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	if err := p.Fail("test"); err == nil {
		t.Error("expected error failing completed payment")
	}
}

func TestPayment_MarkRefunded_FromCompleted(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	if err := p.MarkRefunded(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusRefunded {
		t.Errorf("expected refunded, got %s", p.Status())
	}
}

func TestPayment_MarkRefunded_FromPartiallyRefunded(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)
	if err := p.MarkPartiallyRefunded(); err != nil {
		t.Fatalf("setup: failed to partially refund: %v", err)
	}

	if err := p.MarkRefunded(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusRefunded {
		t.Errorf("expected refunded, got %s", p.Status())
	}
}

func TestPayment_MarkRefunded_InvalidState(t *testing.T) {
	p := newTestPayment()

	if err := p.MarkRefunded(); err == nil {
		t.Error("expected error refunding pending payment")
	}
}

func TestPayment_MarkPartiallyRefunded(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	if err := p.MarkPartiallyRefunded(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusPartiallyRefunded {
		t.Errorf("expected partially_refunded, got %s", p.Status())
	}
}

func TestPayment_MarkPartiallyRefunded_InvalidState(t *testing.T) {
	p := newTestPayment()

	if err := p.MarkPartiallyRefunded(); err == nil {
		t.Error("expected error partially refunding pending payment")
	}
}

func TestPayment_MarkChargedBack(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	if err := p.MarkChargedBack(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusChargedBack {
		t.Errorf("expected charged_back, got %s", p.Status())
	}
}

func TestPayment_MarkChargedBack_InvalidState(t *testing.T) {
	p := newTestPayment()

	if err := p.MarkChargedBack(); err == nil {
		t.Error("expected error charging back pending payment")
	}
}

func TestPayment_Metadata_ReturnsCopy(t *testing.T) {
	p := newTestPayment()
	meta := p.Metadata()
	meta["key"] = "value"

	if len(p.Metadata()) != 0 {
		t.Error("Metadata() should return a copy, not a reference")
	}
}

// --- RecordRefund tests ---

func TestPayment_RecordRefund_FullRefund(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	err := p.RecordRefund(p.Amount())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusRefunded {
		t.Errorf("expected refunded, got %s", p.Status())
	}
	if p.RefundedAmount().Amount().Cmp(p.Amount().Amount()) != 0 {
		t.Errorf("expected refundedAmount == amount")
	}
}

func TestPayment_RecordRefund_PartialRefund(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	partial := shared.NewMoney(big.NewRat(2000, 1), shared.CurrencyJPY)
	err := p.RecordRefund(partial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusPartiallyRefunded {
		t.Errorf("expected partially_refunded, got %s", p.Status())
	}
	if p.RefundedAmount().Amount().Cmp(big.NewRat(2000, 1)) != 0 {
		t.Errorf("expected refundedAmount 2000, got %s", p.RefundedAmount().Amount().RatString())
	}
}

func TestPayment_RecordRefund_CumulativePartialRefunds(t *testing.T) {
	p := newTestPayment() // amount = 5000
	completePayment(t, p)

	partial := shared.NewMoney(big.NewRat(2000, 1), shared.CurrencyJPY)

	// First partial refund
	if err := p.RecordRefund(partial); err != nil {
		t.Fatalf("first refund failed: %v", err)
	}
	if p.Status() != PaymentStatusPartiallyRefunded {
		t.Errorf("expected partially_refunded after first refund, got %s", p.Status())
	}

	// Second partial refund from partially_refunded state
	if err := p.RecordRefund(partial); err != nil {
		t.Fatalf("second refund failed: %v", err)
	}
	if p.Status() != PaymentStatusPartiallyRefunded {
		t.Errorf("expected partially_refunded after second refund, got %s", p.Status())
	}
	if p.RefundedAmount().Amount().Cmp(big.NewRat(4000, 1)) != 0 {
		t.Errorf("expected cumulative refundedAmount 4000, got %s", p.RefundedAmount().Amount().RatString())
	}

	// Final refund to reach full amount
	remaining := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	if err := p.RecordRefund(remaining); err != nil {
		t.Fatalf("final refund failed: %v", err)
	}
	if p.Status() != PaymentStatusRefunded {
		t.Errorf("expected refunded after final refund, got %s", p.Status())
	}
}

func TestPayment_RecordRefund_ExceedsAmount(t *testing.T) {
	p := newTestPayment() // amount = 5000
	completePayment(t, p)

	excess := shared.NewMoney(big.NewRat(6000, 1), shared.CurrencyJPY)
	err := p.RecordRefund(excess)
	if err == nil {
		t.Fatal("expected error when refund exceeds payment amount")
	}
}

func TestPayment_RecordRefund_CumulativeExceedsAmount(t *testing.T) {
	p := newTestPayment() // amount = 5000
	completePayment(t, p)

	partial := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	if err := p.RecordRefund(partial); err != nil {
		t.Fatalf("first refund failed: %v", err)
	}

	// Second refund would exceed total
	excess := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	err := p.RecordRefund(excess)
	if err == nil {
		t.Fatal("expected error when cumulative refund exceeds payment amount")
	}
}

func TestPayment_RecordRefund_InvalidState(t *testing.T) {
	// Pending payment cannot be refunded
	p := newTestPayment()
	refundAmt := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	err := p.RecordRefund(refundAmt)
	if err == nil {
		t.Fatal("expected error refunding pending payment")
	}

	// Failed payment cannot be refunded
	p2 := newTestPayment()
	_ = p2.Fail("test")
	err = p2.RecordRefund(refundAmt)
	if err == nil {
		t.Fatal("expected error refunding failed payment")
	}
}

// --- IdempotencyKey tests ---

func TestPayment_IdempotencyKey(t *testing.T) {
	p := newTestPayment()
	if p.IdempotencyKey() != "" {
		t.Error("expected empty idempotency key by default")
	}

	p.SetIdempotencyKey("idem-123")
	if p.IdempotencyKey() != "idem-123" {
		t.Errorf("expected idem-123, got %s", p.IdempotencyKey())
	}
}

func TestPaymentMethod_Constants(t *testing.T) {
	methods := []PaymentMethod{
		PaymentMethodCreditCard,
		PaymentMethodBankTransfer,
		PaymentMethodDirectDebit,
		PaymentMethodConvenience,
		PaymentMethodCarrier,
	}

	expected := []string{
		"credit_card", "bank_transfer", "direct_debit",
		"convenience_store", "carrier",
	}

	for i, m := range methods {
		if string(m) != expected[i] {
			t.Errorf("expected %s, got %s", expected[i], m)
		}
	}
}
