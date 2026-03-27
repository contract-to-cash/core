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
	_ = p.Complete()

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
	_ = p.Complete()

	if err := p.Fail("test"); err == nil {
		t.Error("expected error failing completed payment")
	}
}

func TestPayment_MarkRefunded_FromCompleted(t *testing.T) {
	p := newTestPayment()
	_ = p.Complete()

	if err := p.MarkRefunded(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusRefunded {
		t.Errorf("expected refunded, got %s", p.Status())
	}
}

func TestPayment_MarkRefunded_FromPartiallyRefunded(t *testing.T) {
	p := newTestPayment()
	_ = p.Complete()
	_ = p.MarkPartiallyRefunded()

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
	_ = p.Complete()

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
	_ = p.Complete()

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
