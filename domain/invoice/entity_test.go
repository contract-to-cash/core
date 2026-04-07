package invoice

import (
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
		WithStatus(InvoiceStatusFinalized),
		WithBillingPeriod(period),
		WithDueDate(due),
		WithAppliedBalance(credit),
		WithAllowPartialPayment(true),
		WithInvoiceNumber("INV-2026-001"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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

func TestInvoice_RecordPayment_Partial(t *testing.T) {
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
