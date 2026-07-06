package payment

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Guard added for issue #148: NewPayment rejects a negative amount (which would
// corrupt refund and balance math downstream) but permits zero, so a
// zero-amount invoice can be settled by a zero-amount payment.
func TestNewPayment_AmountGuards(t *testing.T) {
	money := func(n int64) shared.Money {
		return shared.NewMoney(big.NewRat(n, 1), shared.CurrencyJPY)
	}
	processedAt := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		amount  shared.Money
		wantErr bool
	}{
		{"negative rejected", money(-1), true},
		{"zero allowed", money(0), false},
		{"positive allowed", money(5000), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := NewPayment(
				shared.NewPaymentID(),
				shared.NewInvoiceID(),
				tc.amount,
				PaymentMethodCreditCard,
				"gw_tx",
				processedAt,
			)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var de *shared.DomainError
				if !errors.As(err, &de) || de.Code != shared.ErrCodeValidation {
					t.Fatalf("expected validation_error, got %v", err)
				}
				if p != nil {
					t.Error("expected nil payment on rejection")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p == nil {
				t.Fatal("expected non-nil payment")
			}
			if p.Amount().Amount().Cmp(tc.amount.Amount()) != 0 {
				t.Errorf("expected amount %s, got %s",
					tc.amount.Amount().RatString(), p.Amount().Amount().RatString())
			}
		})
	}
}
