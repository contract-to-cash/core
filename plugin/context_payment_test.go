package plugin

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
)

func jpy(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func newTestInvoice(t *testing.T) *invoice.Invoice {
	t.Helper()
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(),
		shared.AccountID("acct-001"),
		shared.ContractID("contract-001"),
		jpy(10000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("newTestInvoice failed: %v", err)
	}
	return inv
}

func newTestPayment(invoiceID shared.InvoiceID) *payment.Payment {
	return payment.NewPayment(
		shared.NewPaymentID(),
		invoiceID,
		jpy(10000),
		payment.PaymentMethodCreditCard,
		"txn-001",
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	)
}

func TestPaymentContext_NewAndGetters(t *testing.T) {
	inv := newTestInvoice(t)
	p := newTestPayment(inv.ID())

	ctx := NewPaymentContext(context.Background(), p, inv)

	if ctx.Context() == nil {
		t.Error("expected non-nil context")
	}
	if ctx.Payment() != p {
		t.Error("expected payment to match")
	}
	if ctx.Invoice() != inv {
		t.Error("expected invoice to match")
	}
	if ctx.Contract() != nil {
		t.Error("expected nil contract initially")
	}
}

func TestPaymentContext_SetContract(t *testing.T) {
	inv := newTestInvoice(t)
	ctx := NewPaymentContext(context.Background(), nil, inv)

	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(shared.NewContractID(), clock)

	ctx.SetContract(agg)
	if ctx.Contract() != agg {
		t.Error("expected contract to be set")
	}
}

func TestPaymentContext_ContractID_FromInvoice(t *testing.T) {
	inv := newTestInvoice(t)
	ctx := NewPaymentContext(context.Background(), nil, inv)

	if ctx.ContractID() != inv.ContractID() {
		t.Errorf("expected contract ID %s, got %s", inv.ContractID(), ctx.ContractID())
	}
}

func TestPaymentContext_AccountID_FromInvoice(t *testing.T) {
	inv := newTestInvoice(t)
	ctx := NewPaymentContext(context.Background(), nil, inv)

	if ctx.AccountID() != inv.AccountID() {
		t.Errorf("expected account ID %s, got %s", inv.AccountID(), ctx.AccountID())
	}
}

func TestPaymentContext_NilInvoice_ReturnsEmptyIDs(t *testing.T) {
	ctx := NewPaymentContext(context.Background(), nil, nil)

	if ctx.ContractID() != "" {
		t.Errorf("expected empty contract ID, got %s", ctx.ContractID())
	}
	if ctx.AccountID() != "" {
		t.Errorf("expected empty account ID, got %s", ctx.AccountID())
	}
	if ctx.Invoice() != nil {
		t.Error("expected nil invoice")
	}
}

func TestPaymentContext_NilPayment(t *testing.T) {
	inv := newTestInvoice(t)
	ctx := NewPaymentContext(context.Background(), nil, inv)

	if ctx.Payment() != nil {
		t.Error("expected nil payment")
	}
	// Should still work for invoice-based getters
	if ctx.ContractID() == "" {
		t.Error("expected non-empty contract ID even with nil payment")
	}
}
