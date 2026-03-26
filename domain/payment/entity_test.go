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
