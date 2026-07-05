package plugin

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

func newTestContract(t *testing.T, contractID shared.ContractID) *contract.ContractAggregate {
	t.Helper()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	agg := contract.NewContractAggregate(contractID, clock)
	price := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	if err := agg.Create(contract.CreateContractCommand{
		AccountID:    shared.NewAccountID(),
		ContractType: contract.ContractTypeSubscription,
		Interval:     pricing.Monthly(),
		Price:        price,
		BasePrice:    price,
	}, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("failed to create contract: %v", err)
	}
	return agg
}

func TestCalculationContext_ContractAccessors(t *testing.T) {
	contractID := shared.NewContractID()
	agg := newTestContract(t, contractID)
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)

	cc := NewCalculationContext(context.Background(), agg, subtotal)

	if cc.Contract() != agg {
		t.Error("Contract() did not return the aggregate passed in")
	}
	if cc.ContractID() != contractID {
		t.Errorf("ContractID() = %q, want %q", cc.ContractID(), contractID)
	}
}

func TestCalculationContext_ContractID_NilContract(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	cc := NewCalculationContext(context.Background(), nil, subtotal)
	if cc.Contract() != nil {
		t.Error("expected nil contract")
	}
	if cc.ContractID() != "" {
		t.Errorf("expected empty ContractID for nil contract, got %q", cc.ContractID())
	}
}

func TestCalculationContext_ProductID(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	cc := NewCalculationContext(context.Background(), nil, subtotal)

	if cc.ProductID() != "" {
		t.Errorf("expected empty ProductID initially, got %q", cc.ProductID())
	}
	pid := shared.NewProductID()
	cc.SetProductID(pid)
	if cc.ProductID() != pid {
		t.Errorf("ProductID() = %q, want %q", cc.ProductID(), pid)
	}
}

func TestCalculationContext_SetInvoice(t *testing.T) {
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	cc := NewCalculationContext(context.Background(), nil, subtotal)

	if cc.Invoice() != nil {
		t.Error("expected nil invoice initially")
	}
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
		subtotal, shared.Zero(shared.CurrencyJPY), shared.Zero(shared.CurrencyJPY),
	)
	if err != nil {
		t.Fatalf("failed to create invoice: %v", err)
	}
	cc.SetInvoice(inv)
	if cc.Invoice() != inv {
		t.Error("SetInvoice/Invoice round-trip failed")
	}
}

func TestCalculationContext_ContextAccessor(t *testing.T) {
	type ctxKey string
	base := context.WithValue(context.Background(), ctxKey("k"), "v")
	cc := NewCalculationContext(base, nil, shared.Zero(shared.CurrencyJPY))
	if cc.Context().Value(ctxKey("k")) != "v" {
		t.Error("Context() did not return the underlying context")
	}
}
