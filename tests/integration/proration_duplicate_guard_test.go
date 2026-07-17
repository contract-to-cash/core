package integration

// Integration tests for issue #232: the billing service's duplicate-invoice
// guards must exempt proration invoices, mirroring the per-period uniqueness
// contract in invoice.Repository.Save (voided and proration invoices are
// exempt; the in-memory repository and the SQL adapters' partial unique
// indexes all implement that exemption). These tests run against the real
// in-memory repositories so both the service-level guards and the
// repository-level constraint are exercised end to end.

import (
	"context"
	"errors"
	"testing"

	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// prorationGuardFixture wires a BillingService against the in-memory
// repositories and returns it with an active subscription contract.
type prorationGuardFixture struct {
	svc         *service.BillingService
	invoiceRepo *inmemory.InMemoryInvoiceRepository
	agg         *contract.ContractAggregate
}

func newProrationGuardFixture(t *testing.T, ctx context.Context) *prorationGuardFixture {
	t.Helper()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()

	svc := service.NewBillingService(
		contractRepo,
		invoiceRepo,
		usageRepo,
		balance.BalanceConfig{},
		priceRepo, productRepo,
		plugin.NewRegistry(),
		service.BillingConfig{DaysUntilDue: 30},
		clock,
		service.WithBalanceRepo(balanceRepo),
	)

	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, moneyJPY(10000))
	return &prorationGuardFixture{svc: svc, invoiceRepo: invoiceRepo, agg: agg}
}

// generateProration bills a mid-period upgrade adjustment through the full
// pipeline for the contract's current period.
func (f *prorationGuardFixture) generateProration(t *testing.T, ctx context.Context) *invoice.Invoice {
	t.Helper()
	prorationInv, err := f.svc.GenerateProrationInvoice(ctx, f.agg.ContractID(), contract.PlanChangeProration{
		CreditAmount:     moneyJPY(3000),
		ChargeAmount:     moneyJPY(5000),
		AdjustmentAmount: moneyJPY(2000),
		EffectiveDate:    fixedClock().Now(),
	})
	if err != nil {
		t.Fatalf("GenerateProrationInvoice failed: %v", err)
	}
	if !prorationInv.IsProration() {
		t.Fatal("expected the proration invoice to be tagged invoice_type=proration")
	}
	return prorationInv
}

// TestProrationInvoiceDoesNotBlockPeriodInvoice covers scenario (a): a
// mid-period upgrade generates a proration invoice; the regular period invoice
// billed afterwards (the arrears/period-end billing run) must still succeed
// for the same period and both invoices must be persisted.
func TestProrationInvoiceDoesNotBlockPeriodInvoice(t *testing.T) {
	ctx := context.Background()
	f := newProrationGuardFixture(t, ctx)
	period := f.agg.CurrentPeriod()

	prorationInv := f.generateProration(t, ctx)

	regularInv, err := f.svc.GenerateInvoice(ctx, f.agg.ContractID(), period)
	if err != nil {
		t.Fatalf("GenerateInvoice must succeed despite the coexisting proration invoice: %v", err)
	}
	assertMoneyEquals(t, "regular invoice subtotal", regularInv.Subtotal(), 10000)

	// Both invoices persisted for the same period.
	stored, err := f.invoiceRepo.FindByContractAndPeriod(ctx, f.agg.ContractID(), period)
	if err != nil {
		t.Fatalf("FindByContractAndPeriod failed: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("expected 2 invoices (proration + regular) for the period, got %d", len(stored))
	}
	ids := map[shared.InvoiceID]bool{}
	for _, inv := range stored {
		ids[inv.ID()] = true
	}
	if !ids[prorationInv.ID()] || !ids[regularInv.ID()] {
		t.Errorf("expected both proration %s and regular %s persisted, got %v",
			prorationInv.ID(), regularInv.ID(), ids)
	}
}

// TestProrationInvoiceDoesNotBlockVoidAndRecreate covers scenario (b): with a
// proration invoice coexisting, voiding the period's regular invoice and
// regenerating it (the ReissueInvoice-style void-and-recreate path) must
// succeed, and the revision chain must link to the voided REGULAR invoice.
func TestProrationInvoiceDoesNotBlockVoidAndRecreate(t *testing.T) {
	ctx := context.Background()
	f := newProrationGuardFixture(t, ctx)
	period := f.agg.CurrentPeriod()

	regularInv, err := f.svc.GenerateInvoice(ctx, f.agg.ContractID(), period)
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}

	f.generateProration(t, ctx)

	// Void the regular invoice (load-modify-save so the optimistic lock is honored).
	loaded, err := f.invoiceRepo.FindByID(ctx, regularInv.ID())
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if err := loaded.Void(); err != nil {
		t.Fatalf("Void failed: %v", err)
	}
	if err := f.invoiceRepo.Save(ctx, loaded); err != nil {
		t.Fatalf("saving voided invoice failed: %v", err)
	}

	regenerated, err := f.svc.RegenerateInvoice(ctx, f.agg.ContractID(), period)
	if err != nil {
		t.Fatalf("RegenerateInvoice must succeed despite the coexisting proration invoice: %v", err)
	}
	if regenerated.RevisionOf() == nil || *regenerated.RevisionOf() != regularInv.ID() {
		t.Errorf("expected revision chain to link to the voided regular invoice %s, got %v",
			regularInv.ID(), regenerated.RevisionOf())
	}
	if regenerated.Metadata()[invoice.MetadataKeyInvoiceType] != invoice.InvoiceTypeRegeneration {
		t.Errorf("expected invoice_type=regeneration, got %q",
			regenerated.Metadata()[invoice.MetadataKeyInvoiceType])
	}
}

// TestDuplicateRegularInvoiceStillRejectedWithProrationPresent covers scenario
// (c): the proration exemption must not weaken the guard for REGULAR invoices —
// a second regular invoice for the same period is still rejected with a
// conflict DomainError.
func TestDuplicateRegularInvoiceStillRejectedWithProrationPresent(t *testing.T) {
	ctx := context.Background()
	f := newProrationGuardFixture(t, ctx)
	period := f.agg.CurrentPeriod()

	f.generateProration(t, ctx)

	if _, err := f.svc.GenerateInvoice(ctx, f.agg.ContractID(), period); err != nil {
		t.Fatalf("first regular GenerateInvoice failed: %v", err)
	}

	_, err := f.svc.GenerateInvoice(ctx, f.agg.ContractID(), period)
	if err == nil {
		t.Fatal("second regular invoice for the same period must be rejected")
	}
	var domainErr *shared.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if domainErr.Code != shared.ErrCodeConflict {
		t.Errorf("expected conflict error code, got %s (message: %s)", domainErr.Code, domainErr.Message)
	}
}
