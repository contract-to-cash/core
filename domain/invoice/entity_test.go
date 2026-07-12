package invoice

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestNewInvoice_CurrencyMismatch_Subtotal_Discount(t *testing.T) {
	id := shared.NewInvoiceID()
	acct := shared.NewAccountID()
	contractID := shared.NewContractID()
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	discount := shared.NewMoney(big.NewRat(500, 1), shared.CurrencyUSD) // mismatch
	tax := shared.Zero(shared.CurrencyJPY)

	_, err := NewInvoice(id, acct, contractID, subtotal, discount, tax)
	if err == nil {
		t.Fatal("expected error for currency mismatch between subtotal and discount, got nil")
	}
	domErr, ok := err.(*shared.DomainError)
	if !ok {
		t.Fatalf("expected *shared.DomainError, got %T", err)
	}
	if domErr.Code != shared.ErrCodeCurrencyMismatch {
		t.Errorf("expected error code %s, got %s", shared.ErrCodeCurrencyMismatch, domErr.Code)
	}
}

func TestNewInvoice_CurrencyMismatch_AfterDiscount_Tax(t *testing.T) {
	id := shared.NewInvoiceID()
	acct := shared.NewAccountID()
	contractID := shared.NewContractID()
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	discount := shared.Zero(shared.CurrencyJPY)
	tax := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyUSD) // mismatch

	_, err := NewInvoice(id, acct, contractID, subtotal, discount, tax)
	if err == nil {
		t.Fatal("expected error for currency mismatch between subtotal and tax, got nil")
	}
	domErr, ok := err.(*shared.DomainError)
	if !ok {
		t.Fatalf("expected *shared.DomainError, got %T", err)
	}
	if domErr.Code != shared.ErrCodeCurrencyMismatch {
		t.Errorf("expected error code %s, got %s", shared.ErrCodeCurrencyMismatch, domErr.Code)
	}
}

func TestNewInvoice_Defaults(t *testing.T) {
	id := shared.NewInvoiceID()
	acct := shared.NewAccountID()
	contract := shared.NewContractID()
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	discount := shared.Zero(shared.CurrencyJPY)
	tax := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)

	inv, err := NewInvoice(id, acct, contract, subtotal, discount, tax)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if inv.ID() != id {
		t.Errorf("expected id %s, got %s", id, inv.ID())
	}
	if inv.AccountID() != acct {
		t.Errorf("expected account %s, got %s", acct, inv.AccountID())
	}
	if inv.ContractID() != contract {
		t.Errorf("expected contract %s, got %s", contract, inv.ContractID())
	}
	if inv.Status() != InvoiceStatusDraft {
		t.Errorf("expected status draft, got %s", inv.Status())
	}

	// total = subtotal - discount + tax = 10000 - 0 + 1000 = 11000
	expectedTotal := big.NewRat(11000, 1)
	if inv.Total().Amount().Cmp(expectedTotal) != 0 {
		t.Errorf("expected total 11000, got %s", inv.Total().Amount().RatString())
	}
	if inv.Balance().Amount().Cmp(expectedTotal) != 0 {
		t.Errorf("expected balance 11000, got %s", inv.Balance().Amount().RatString())
	}
	if !inv.PaidAmount().IsZero() {
		t.Errorf("expected paid amount 0, got %s", inv.PaidAmount().Amount().RatString())
	}
	if inv.AllowPartialPay() {
		t.Error("expected allowPartialPay to be false by default")
	}
}

func TestNewInvoice_WithOptions(t *testing.T) {
	id := shared.NewInvoiceID()
	acct := shared.NewAccountID()
	contract := shared.NewContractID()
	subtotal := shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY)
	discount := shared.NewMoney(big.NewRat(500, 1), shared.CurrencyJPY)
	tax := shared.NewMoney(big.NewRat(450, 1), shared.CurrencyJPY)

	due := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	period, err := shared.NewDateRange(start, end)
	if err != nil {
		t.Fatalf("unexpected error creating date range: %v", err)
	}
	credit := shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY)

	inv, err := NewInvoice(id, acct, contract, subtotal, discount, tax,
		WithBillingPeriod(period),
		WithDueDate(due),
		WithAppliedBalance(credit),
		WithAllowPartialPayment(true),
		WithInvoiceNumber("INV-2026-001"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// WithStatus accepts only Draft (issue #238) — reach finalized via the
	// real transition.
	if err := inv.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	if inv.Status() != InvoiceStatusFinalized {
		t.Errorf("expected status finalized, got %s", inv.Status())
	}
	if inv.DueDate() != due {
		t.Errorf("expected due date %v, got %v", due, inv.DueDate())
	}
	if inv.BillingPeriod().Start() != start {
		t.Errorf("expected billing period start %v, got %v", start, inv.BillingPeriod().Start())
	}
	if inv.AppliedBalance().Amount().Cmp(big.NewRat(100, 1)) != 0 {
		t.Errorf("expected applied balance 100, got %s", inv.AppliedBalance().Amount().RatString())
	}
	if !inv.AllowPartialPay() {
		t.Error("expected allowPartialPay to be true")
	}
	if inv.InvoiceNumber() != "INV-2026-001" {
		t.Errorf("expected invoice number INV-2026-001, got %s", inv.InvoiceNumber())
	}
}

func mustNewInvoice(t *testing.T, id shared.InvoiceID, accountID shared.AccountID, contractID shared.ContractID, subtotal, discount, tax shared.Money, opts ...InvoiceOption) *Invoice {
	t.Helper()
	inv, err := NewInvoice(id, accountID, contractID, subtotal, discount, tax, opts...)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	return inv
}

func TestInvoice_Finalize(t *testing.T) {
	inv := mustNewInvoice(t,
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
	)

	if err := inv.Finalize(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.Status() != InvoiceStatusFinalized {
		t.Errorf("expected status finalized, got %s", inv.Status())
	}

	// Cannot finalize again
	if err := inv.Finalize(); err == nil {
		t.Error("expected error when finalizing non-draft invoice")
	}
}

func TestInvoice_RecordPayment_Full(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	inv := mustNewInvoice(t,
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		subtotal,
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
	)

	if err := inv.Finalize(); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	paidAt := time.Now().UTC()
	err := inv.RecordPayment(shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY), paidAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if inv.Status() != InvoiceStatusPaid {
		t.Errorf("expected status paid, got %s", inv.Status())
	}
	if !inv.Balance().IsZero() {
		t.Errorf("expected balance 0, got %s", inv.Balance().Amount().RatString())
	}
	if inv.PaidAt() == nil {
		t.Error("expected paidAt to be set")
	}
}

func TestInvoice_RecordPayment_Overpayment(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	inv := mustNewInvoice(t,
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		subtotal,
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
	)

	if err := inv.Finalize(); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	// Attempt to pay more than amountDue
	paidAt := time.Now().UTC()
	err := inv.RecordPayment(shared.NewMoney(big.NewRat(15000, 1), shared.CurrencyJPY), paidAt)
	if err == nil {
		t.Fatal("expected error for overpayment, got nil")
	}

	// Status should remain finalized
	if inv.Status() != InvoiceStatusFinalized {
		t.Errorf("expected status finalized after rejected overpayment, got %s", inv.Status())
	}
	// Balance should be unchanged
	if inv.Balance().Amount().Cmp(big.NewRat(10000, 1)) != 0 {
		t.Errorf("expected balance 10000 after rejected overpayment, got %s", inv.Balance().Amount().RatString())
	}
}

func TestInvoice_RecordPayment_CumulativeOverpayment(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	inv := mustNewInvoice(t,
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		subtotal,
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithAllowPartialPayment(true),
	)

	if err := inv.Finalize(); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	paidAt := time.Now().UTC()

	// First partial payment: 7000
	if err := inv.RecordPayment(shared.NewMoney(big.NewRat(7000, 1), shared.CurrencyJPY), paidAt); err != nil {
		t.Fatalf("first payment failed: %v", err)
	}
	if inv.Status() != InvoiceStatusPartialPaid {
		t.Errorf("expected partial_paid after first payment, got %s", inv.Status())
	}

	// Second payment exceeding remaining: 5000 (remaining is 3000)
	err := inv.RecordPayment(shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY), paidAt)
	if err == nil {
		t.Fatal("expected error for cumulative overpayment, got nil")
	}

	// Status and balance should reflect only the first payment
	if inv.Status() != InvoiceStatusPartialPaid {
		t.Errorf("expected partial_paid after rejected second payment, got %s", inv.Status())
	}
	if inv.Balance().Amount().Cmp(big.NewRat(3000, 1)) != 0 {
		t.Errorf("expected balance 3000, got %s", inv.Balance().Amount().RatString())
	}

	// Pay exactly the remaining amount: 3000
	if err := inv.RecordPayment(shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY), paidAt); err != nil {
		t.Fatalf("exact remaining payment failed: %v", err)
	}
	if inv.Status() != InvoiceStatusPaid {
		t.Errorf("expected paid after exact payment, got %s", inv.Status())
	}
	if !inv.Balance().IsZero() {
		t.Errorf("expected zero balance, got %s", inv.Balance().Amount().RatString())
	}
}

// TestInvoice_RecordPayment_PartialRejectedWhenNotAllowed guards review #1: when
// allowPartialPay is false (the default), a payment that leaves a balance must be
// rejected — the documented opt-in business rule (design-decisions 3.1).
func TestInvoice_RecordPayment_PartialRejectedWhenNotAllowed(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	inv := mustNewInvoice(t,
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		subtotal,
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		// allowPartialPay defaults to false (no WithAllowPartialPayment)
	)
	if err := inv.Finalize(); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	// Validate must reject too (pre-charge gate).
	if err := inv.ValidatePayment(shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)); err == nil {
		t.Fatal("expected ValidatePayment to reject partial payment when not allowed")
	}

	err := inv.RecordPayment(shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY), time.Now().UTC())
	if err == nil {
		t.Fatal("expected error for partial payment when allowPartialPay is false")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) || domErr.Code != shared.ErrCodeBusinessRule {
		t.Errorf("expected business_rule_violation, got %v", err)
	}
	// Invoice must be unchanged.
	if inv.Status() != InvoiceStatusFinalized {
		t.Errorf("expected status unchanged (finalized), got %s", inv.Status())
	}
	if inv.PaidAmount().Amount().Sign() != 0 {
		t.Errorf("expected no payment recorded, paidAmount=%s", inv.PaidAmount().Amount().RatString())
	}

	// A FULL payment must still be accepted even when partial is disallowed.
	if err := inv.RecordPayment(subtotal, time.Now().UTC()); err != nil {
		t.Fatalf("full payment should be accepted: %v", err)
	}
	if inv.Status() != InvoiceStatusPaid {
		t.Errorf("expected paid after full payment, got %s", inv.Status())
	}
}

func TestInvoice_RecordPayment_Partial(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	inv := mustNewInvoice(t,
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		subtotal,
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithAllowPartialPayment(true),
	)

	if err := inv.Finalize(); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	paidAt := time.Now().UTC()
	err := inv.RecordPayment(shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY), paidAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if inv.Status() != InvoiceStatusPartialPaid {
		t.Errorf("expected status partial_paid, got %s", inv.Status())
	}
	expectedBalance := big.NewRat(7000, 1)
	if inv.Balance().Amount().Cmp(expectedBalance) != 0 {
		t.Errorf("expected balance 7000, got %s", inv.Balance().Amount().RatString())
	}
}

// TestNewInvoice_SignAndMagnitudeInvariants exercises the issue #188 invariants:
// subtotal / discount / tax must be non-negative and discount must not exceed
// subtotal. A violation must surface a validation *shared.DomainError.
func TestNewInvoice_SignAndMagnitudeInvariants(t *testing.T) {
	tests := []struct {
		name     string
		subtotal shared.Money
		discount shared.Money
		tax      shared.Money
		wantErr  bool
		wantCode shared.ErrorCode
	}{
		{
			name:     "valid non-negative amounts",
			subtotal: jpy(10000),
			discount: jpy(500),
			tax:      jpy(950),
			wantErr:  false,
		},
		{
			name:     "valid discount equal to subtotal",
			subtotal: jpy(10000),
			discount: jpy(10000),
			tax:      shared.Zero(shared.CurrencyJPY),
			wantErr:  false,
		},
		{
			name:     "negative subtotal rejected",
			subtotal: jpy(-1),
			discount: shared.Zero(shared.CurrencyJPY),
			tax:      shared.Zero(shared.CurrencyJPY),
			wantErr:  true,
			wantCode: shared.ErrCodeValidation,
		},
		{
			name:     "negative discount rejected",
			subtotal: jpy(10000),
			discount: jpy(-1000),
			tax:      shared.Zero(shared.CurrencyJPY),
			wantErr:  true,
			wantCode: shared.ErrCodeValidation,
		},
		{
			name:     "negative tax rejected",
			subtotal: jpy(10000),
			discount: shared.Zero(shared.CurrencyJPY),
			tax:      jpy(-1),
			wantErr:  true,
			wantCode: shared.ErrCodeValidation,
		},
		{
			name:     "discount exceeding subtotal rejected",
			subtotal: jpy(100),
			discount: jpy(200),
			tax:      shared.Zero(shared.CurrencyJPY),
			wantErr:  true,
			wantCode: shared.ErrCodeValidation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewInvoice(shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
				tt.subtotal, tt.discount, tt.tax)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var domErr *shared.DomainError
			if !errors.As(err, &domErr) {
				t.Fatalf("expected *shared.DomainError, got %T", err)
			}
			if domErr.Code != tt.wantCode {
				t.Errorf("expected error code %s, got %s", tt.wantCode, domErr.Code)
			}
		})
	}
}

// TestWithAppliedBalance_Bounds checks that WithAppliedBalance rejects negative
// and greater-than-total applied balances while accepting in-range values (#188).
func TestWithAppliedBalance_Bounds(t *testing.T) {
	// total = 10000 - 0 + 0 = 10000
	subtotal := jpy(10000)
	zero := shared.Zero(shared.CurrencyJPY)

	tests := []struct {
		name    string
		applied shared.Money
		wantErr bool
		code    shared.ErrorCode
	}{
		{name: "in range", applied: jpy(3000), wantErr: false},
		{name: "equal to total", applied: jpy(10000), wantErr: false},
		{name: "negative rejected", applied: jpy(-1), wantErr: true, code: shared.ErrCodeValidation},
		{name: "exceeds total rejected", applied: jpy(10001), wantErr: true, code: shared.ErrCodeValidation},
		{name: "currency mismatch rejected", applied: shared.NewMoney(big.NewRat(100, 1), shared.CurrencyUSD), wantErr: true, code: shared.ErrCodeCurrencyMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv, err := NewInvoice(shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
				subtotal, zero, zero, WithAppliedBalance(tt.applied))
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				wantDue, _ := subtotal.Subtract(tt.applied)
				if inv.AmountDue().Amount().Cmp(wantDue.Amount()) != 0 {
					t.Errorf("expected amount due %s, got %s", wantDue.Amount().RatString(), inv.AmountDue().Amount().RatString())
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var domErr *shared.DomainError
			if !errors.As(err, &domErr) {
				t.Fatalf("expected *shared.DomainError, got %T", err)
			}
			if domErr.Code != tt.code {
				t.Errorf("expected error code %s, got %s", tt.code, domErr.Code)
			}
		})
	}
}

// TestWithAmountDue_Bounds checks that WithAmountDue rejects negative and
// greater-than-total amounts (#188).
func TestWithAmountDue_Bounds(t *testing.T) {
	subtotal := jpy(10000)
	zero := shared.Zero(shared.CurrencyJPY)

	tests := []struct {
		name    string
		due     shared.Money
		wantErr bool
		code    shared.ErrorCode
	}{
		{name: "in range", due: jpy(5000), wantErr: false},
		{name: "equal to total", due: jpy(10000), wantErr: false},
		{name: "negative rejected", due: jpy(-1), wantErr: true, code: shared.ErrCodeValidation},
		{name: "exceeds total rejected", due: jpy(10001), wantErr: true, code: shared.ErrCodeValidation},
		{name: "currency mismatch rejected", due: shared.NewMoney(big.NewRat(100, 1), shared.CurrencyUSD), wantErr: true, code: shared.ErrCodeCurrencyMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv, err := NewInvoice(shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
				subtotal, zero, zero, WithAmountDue(tt.due))
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if inv.AmountDue().Amount().Cmp(tt.due.Amount()) != 0 {
					t.Errorf("expected amount due %s, got %s", tt.due.Amount().RatString(), inv.AmountDue().Amount().RatString())
				}
				// balance must track amountDue (paidAmount is zero at
				// construction), not stay stuck at the full total (issue #196).
				if inv.Balance().Amount().Cmp(tt.due.Amount()) != 0 {
					t.Errorf("expected balance synced to amount due %s, got %s",
						tt.due.Amount().RatString(), inv.Balance().Amount().RatString())
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var domErr *shared.DomainError
			if !errors.As(err, &domErr) {
				t.Fatalf("expected *shared.DomainError, got %T", err)
			}
			if domErr.Code != tt.code {
				t.Errorf("expected error code %s, got %s", tt.code, domErr.Code)
			}
		})
	}
}
