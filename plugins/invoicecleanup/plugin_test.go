package invoicecleanup

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
		t.Fatalf("unexpected error creating draft invoice: %v", err)
	}
	_ = invoiceRepo.Save(ctx, draftInv)

	// Create a finalized invoice
	finalizedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(5000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("unexpected error creating finalized invoice: %v", err)
	}
	_ = finalizedInv.Finalize()
	_ = invoiceRepo.Save(ctx, finalizedInv)

	// Create a paid invoice (should NOT be voided). WithStatus accepts only
	// Draft (issue #238), so reach paid via the real transitions.
	paidInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(3000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("unexpected error creating paid invoice: %v", err)
	}
	if err := paidInv.Finalize(); err != nil {
		t.Fatalf("finalize paid invoice: %v", err)
	}
	if err := paidInv.RecordPayment(paidInv.AmountDue(), clock.Now()); err != nil {
		t.Fatalf("record payment: %v", err)
	}
	_ = invoiceRepo.Save(ctx, paidInv)

	// Create contract aggregate and cancel it
	agg := contract.NewContractAggregate(contractID, clock)
	_ = agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-invoicecleanup-plugin-1",
		AccountID:      accountID,
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          jpy(10000),
		BasePrice:      jpy(10000),
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
		IdempotencyKey: "idem-invoicecleanup-plugin-2",
		AccountID:      shared.NewAccountID(),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          jpy(1000),
		BasePrice:      jpy(1000),
	}, eventstore.EventMetadata{UserID: "test"})
	_ = agg.Activate(eventstore.EventMetadata{UserID: "test"})
	_ = agg.Cancel("no reason", eventstore.EventMetadata{UserID: "test"})

	p := NewInvoiceCleanupPlugin(invoiceRepo)
	err := p.OnContractCancel(plugin.NewContext(context.Background()), agg)
	if err != nil {
		t.Fatalf("unexpected error on empty invoices: %v", err)
	}
}

// TestOnContractCancel_SkipsInvoicesWithAppliedBalance verifies that a Draft or
// Finalized invoice which already consumed account credit (AppliedBalance > 0) is
// NOT voided on contract cancellation (issue #184). Voiding it here would destroy
// the consumed credit, because this plugin cannot atomically restore it.
func TestOnContractCancel_SkipsInvoicesWithAppliedBalance(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	contractID := shared.NewContractID()
	accountID := shared.NewAccountID()

	// Draft invoice that consumed 2000 of credit (appliedBalance > 0): must be skipped.
	creditedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(10000), jpy(0), jpy(0),
		invoice.WithAppliedBalance(jpy(2000)),
	)
	if err != nil {
		t.Fatalf("unexpected error creating credited invoice: %v", err)
	}
	_ = invoiceRepo.Save(ctx, creditedInv)

	// A plain draft invoice with no applied balance: should still be voided.
	plainInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(5000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("unexpected error creating plain invoice: %v", err)
	}
	_ = invoiceRepo.Save(ctx, plainInv)

	agg := contract.NewContractAggregate(contractID, clock)
	_ = agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-invoicecleanup-appliedbalance-1",
		AccountID:      accountID,
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          jpy(10000),
		BasePrice:      jpy(10000),
	}, eventstore.EventMetadata{UserID: "test"})
	_ = agg.Activate(eventstore.EventMetadata{UserID: "test"})
	_ = agg.Cancel("customer request", eventstore.EventMetadata{UserID: "test"})

	p := NewInvoiceCleanupPlugin(invoiceRepo)
	if err := p.OnContractCancel(plugin.NewContext(ctx), agg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The credited invoice must be left untouched (still draft), preserving credit.
	got, _ := invoiceRepo.FindByID(ctx, creditedInv.ID())
	if got.Status() != invoice.InvoiceStatusDraft {
		t.Errorf("expected credited invoice to be SKIPPED (still draft), got %s", got.Status())
	}

	// The plain invoice must still be voided.
	gotPlain, _ := invoiceRepo.FindByID(ctx, plainInv.ID())
	if gotPlain.Status() != invoice.InvoiceStatusVoided {
		t.Errorf("expected plain draft invoice to be voided, got %s", gotPlain.Status())
	}
}
