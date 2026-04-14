// pointer_isolation_test.go — see issue #96.
package payment

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// TestPayment_FailureReason_GetterIsDefensivelyCopied verifies that mutating
// the *string returned by Payment.FailureReason() does NOT alter the
// payment's internal state.
func TestPayment_FailureReason_GetterIsDefensivelyCopied(t *testing.T) {
	amount := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	p := NewPayment(
		shared.NewPaymentID(),
		shared.NewInvoiceID(),
		amount,
		PaymentMethodCreditCard,
		"tx-1",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	reason := "card declined"
	if err := p.Fail(reason); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	got := p.FailureReason()
	if got == nil {
		t.Fatal("FailureReason must not be nil")
	}
	*got = "mutated"

	again := p.FailureReason()
	if again == nil || *again != reason {
		t.Errorf("Payment.FailureReason() leaks internal pointer: got %v, want %s", again, reason)
	}
}

// TestPayment_FailureReason_NilSafe verifies that a pending payment returns
// nil from FailureReason() without panicking.
func TestPayment_FailureReason_NilSafe(t *testing.T) {
	amount := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	p := NewPayment(
		shared.NewPaymentID(),
		shared.NewInvoiceID(),
		amount,
		PaymentMethodCreditCard,
		"tx-1",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	if p.FailureReason() != nil {
		t.Errorf("expected nil FailureReason, got %v", p.FailureReason())
	}
}
