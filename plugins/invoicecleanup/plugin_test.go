package invoicecleanup

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

func jpy(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func TestOnContractCancel_VoidsDraftAndFinalized(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	contractID := shared.NewContractID()
	accountID := shared.NewAccountID()

	// Create a draft invoice
	draftInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(10000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("NewInvoice failed: %v", err)
	}
	_ = invoiceRepo.Save(ctx, draftInv)

	// Create a finalized invoice
	finalizedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(5000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("NewInvoice failed: %v", err)
	}
	_ = finalizedInv.Finalize()
	_ = invoiceRepo.Save(ctx, finalizedInv)

	// Create a paid invoice (should NOT be voided)
	paidInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(3000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusPaid),
	)
	if err != nil {
		t.Fatalf("NewInvoice failed: %v", err)
	}
	_ = invoiceRepo.Save(ctx, paidInv)

	// Create contract aggregate and cancel it
	agg := contract.NewContractAggregate(contractID, clock)
	_ = agg.Create(contract.CreateContractCommand{
		AccountID:    accountID,
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        jpy(10000),
		BasePrice:    jpy(10000),
	}, eventstore.EventMetadata{UserID: "test"})
	_ = agg.Activate(eventstore.EventMetadata{UserID: "test"})
	_ = agg.Cancel("customer request", eventstore.EventMetadata{UserID: "test"})

	// Execute the plugin
	p := NewInvoiceCleanupPlugin(invoiceRepo)
	pluginCtx := plugin.NewContext(ctx)
	err = p.OnContractCancel(pluginCtx, agg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify: draft invoice should be voided
	inv1, _ := invoiceRepo.FindByID(ctx, draftInv.ID())
	if inv1.Status() != invoice.InvoiceStatusVoided {
		t.Errorf("expected draft invoice to be voided, got %s", inv1.Status())
	}

	// Verify: finalized invoice should be voided
	inv2, _ := invoiceRepo.FindByID(ctx, finalizedInv.ID())
	if inv2.Status() != invoice.InvoiceStatusVoided {
		t.Errorf("expected finalized invoice to be voided, got %s", inv2.Status())
	}

	// Verify: paid invoice should remain paid
	inv3, _ := invoiceRepo.FindByID(ctx, paidInv.ID())
	if inv3.Status() != invoice.InvoiceStatusPaid {
		t.Errorf("expected paid invoice to remain paid, got %s", inv3.Status())
	}
}

func TestOnContractCancel_NoInvoices(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)

	contractID := shared.NewContractID()
	agg := contract.NewContractAggregate(contractID, clock)
	_ = agg.Create(contract.CreateContractCommand{
		AccountID:    shared.NewAccountID(),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        jpy(1000),
		BasePrice:    jpy(1000),
	}, eventstore.EventMetadata{UserID: "test"})
	_ = agg.Activate(eventstore.EventMetadata{UserID: "test"})
	_ = agg.Cancel("no reason", eventstore.EventMetadata{UserID: "test"})

	p := NewInvoiceCleanupPlugin(invoiceRepo)
	err := p.OnContractCancel(plugin.NewContext(context.Background()), agg)
	if err != nil {
		t.Fatalf("unexpected error on empty invoices: %v", err)
	}
}
