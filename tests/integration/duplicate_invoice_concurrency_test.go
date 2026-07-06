package integration

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// newDuplicateInvoiceBillingService wires a BillingService against real
// in-memory repositories for the issue #149 duplicate-invoice regression tests.
func newDuplicateInvoiceBillingService(
	t *testing.T,
	clock shared.Clock,
) (*service.BillingService, *inmemory.InMemoryContractRepository, *inmemory.InMemoryPriceRepository, *inmemory.InMemoryInvoiceRepository) {
	t.Helper()

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
	return svc, contractRepo, priceRepo, invoiceRepo
}

// TestGenerateInvoice_ConcurrentSamePeriod_SingleWinner_Integration is the
// issue #149 regression test. Several goroutines call GenerateInvoice for the
// SAME contract and billing period at once. The pre-tx duplicate check can pass
// in all of them (the reads are not serialized against each other), so the
// guarantee must come from the in-tx re-check plus the repository's per-period
// uniqueness constraint. Exactly one call must persist a billable invoice; every
// loser must be rejected with a clean shared.ErrCodeConflict domain error.
//
// Without the fix, both concurrent inserts of a brand-new invoice (no prior
// version to conflict on) would succeed, producing two billable invoices for one
// period and a downstream double charge.
func TestGenerateInvoice_ConcurrentSamePeriod_SingleWinner_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	svc, contractRepo, priceRepo, invoiceRepo := newDuplicateInvoiceBillingService(t, clock)

	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, moneyJPY(5000))
	period := agg.CurrentPeriod()

	const n = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	var failures []error

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.GenerateInvoice(ctx, agg.ContractID(), period)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
			} else {
				failures = append(failures, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful GenerateInvoice, got %d", successes)
	}
	if len(failures) != n-1 {
		t.Fatalf("expected %d rejected GenerateInvoice calls, got %d", n-1, len(failures))
	}
	for _, err := range failures {
		var domainErr *shared.DomainError
		if !errors.As(err, &domainErr) || domainErr.Code != shared.ErrCodeConflict {
			t.Errorf("expected a clean conflict domain error for a losing generate, got: %v", err)
		}
	}

	// Exactly one non-voided invoice must be persisted for the period.
	persisted, err := invoiceRepo.FindByContractAndPeriod(ctx, agg.ContractID(), period)
	if err != nil {
		t.Fatalf("FindByContractAndPeriod failed: %v", err)
	}
	active := 0
	for _, inv := range persisted {
		if inv.Status() != invoice.InvoiceStatusVoided {
			active++
		}
	}
	if active != 1 {
		t.Errorf("expected exactly 1 persisted non-voided invoice for the period, got %d (total=%d)",
			active, len(persisted))
	}
}

// TestRegenerateInvoice_SamePeriodAfterVoid_Succeeds_Integration verifies the
// issue #149 fix does not break void-and-recreate: after the period's invoice is
// voided, RegenerateInvoice must succeed for the SAME period (the voided original
// is exempt from the uniqueness constraint) and link the replacement to it.
func TestRegenerateInvoice_SamePeriodAfterVoid_Succeeds_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	svc, contractRepo, priceRepo, invoiceRepo := newDuplicateInvoiceBillingService(t, clock)

	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, moneyJPY(5000))
	period := agg.CurrentPeriod()

	// Generate the period's invoice, then void it.
	original, err := svc.GenerateInvoice(ctx, agg.ContractID(), period)
	if err != nil {
		t.Fatalf("initial GenerateInvoice failed: %v", err)
	}
	if err := original.VoidWithReason("superseded by coupon application"); err != nil {
		t.Fatalf("VoidWithReason failed: %v", err)
	}
	if err := invoiceRepo.Save(ctx, original); err != nil {
		t.Fatalf("saving voided invoice failed: %v", err)
	}

	// Regeneration for the same period must succeed now that the original is voided.
	replacement, err := svc.RegenerateInvoice(ctx, agg.ContractID(), period)
	if err != nil {
		t.Fatalf("RegenerateInvoice after void must succeed, got: %v", err)
	}
	if replacement == nil {
		t.Fatal("expected a replacement invoice")
	}
	if rev := replacement.RevisionOf(); rev == nil || *rev != original.ID() {
		t.Errorf("replacement.RevisionOf() must link to the voided original %s, got %v", original.ID(), rev)
	}

	// Exactly one non-voided invoice must exist for the period (the replacement),
	// alongside the voided original.
	persisted, err := invoiceRepo.FindByContractAndPeriod(ctx, agg.ContractID(), period)
	if err != nil {
		t.Fatalf("FindByContractAndPeriod failed: %v", err)
	}
	active, voided := 0, 0
	for _, inv := range persisted {
		if inv.Status() == invoice.InvoiceStatusVoided {
			voided++
		} else {
			active++
		}
	}
	if active != 1 || voided != 1 {
		t.Errorf("expected 1 active + 1 voided invoice for the period, got active=%d voided=%d", active, voided)
	}

	// A SECOND regeneration without voiding the replacement must be rejected,
	// because a non-voided invoice now occupies the period.
	if _, err := svc.RegenerateInvoice(ctx, agg.ContractID(), period); err == nil {
		t.Error("expected RegenerateInvoice to be rejected while a non-voided invoice occupies the period")
	}
}

// TestGenerateInvoice_SequentialDistinctPeriods_Unaffected_Integration confirms
// the per-period uniqueness constraint keys on the billing period: generating
// invoices for DIFFERENT periods of the same contract must all succeed.
func TestGenerateInvoice_SequentialDistinctPeriods_Unaffected_Integration(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	svc, contractRepo, priceRepo, invoiceRepo := newDuplicateInvoiceBillingService(t, clock)

	// Build an auto-renewing active contract so RenewWithInterval advances the
	// billing period (a non-auto-renewing contract would expire instead).
	price := moneyJPY(5000)
	priceEntity := pricing.NewPrice(
		shared.NewProductID(), price, price.Currency(),
		pricing.BillingCycleMonthly, nil, clock.Now(),
	)
	if err := priceRepo.Save(ctx, priceEntity); err != nil {
		t.Fatalf("failed to save price: %v", err)
	}
	agg := contract.NewContractAggregate(shared.NewContractID(), clock)
	if err := agg.Create(contract.CreateContractCommand{
		IdempotencyKey: "idem-integration-duplicate_invoice_concurrency-1",
		AccountID:      shared.AccountID("acc-149-periods"),
		PriceID:        priceEntity.ID(),
		ContractType:   contract.ContractTypeSubscription,
		Interval:       pricing.Monthly(),
		Price:          price,
		BasePrice:      price,
		AutoRenew:      true,
	}, emptyMetadata()); err != nil {
		t.Fatalf("failed to create contract: %v", err)
	}
	if err := agg.Activate(emptyMetadata()); err != nil {
		t.Fatalf("failed to activate contract: %v", err)
	}
	if err := contractRepo.Save(ctx, agg); err != nil {
		t.Fatalf("failed to save contract: %v", err)
	}

	// First period: the contract's current period.
	period1 := agg.CurrentPeriod()
	if _, err := svc.GenerateInvoice(ctx, agg.ContractID(), period1); err != nil {
		t.Fatalf("GenerateInvoice for period 1 failed: %v", err)
	}

	// Advance the contract to the next period via renewal so a distinct period
	// invoice is permitted (billing period must match the contract's current period).
	if err := agg.RenewWithInterval(agg.GetInterval(), emptyMetadata()); err != nil {
		t.Fatalf("RenewWithInterval failed: %v", err)
	}
	if err := contractRepo.Save(ctx, agg); err != nil {
		t.Fatalf("saving renewed contract failed: %v", err)
	}
	period2 := agg.CurrentPeriod()
	if period2.Equals(period1) {
		t.Fatalf("expected a distinct period after renewal, got the same period %s", period2)
	}
	if _, err := svc.GenerateInvoice(ctx, agg.ContractID(), period2); err != nil {
		t.Fatalf("GenerateInvoice for period 2 (distinct) must succeed, got: %v", err)
	}

	// Both periods must now hold exactly one invoice each.
	for i, period := range []shared.DateRange{period1, period2} {
		got, err := invoiceRepo.FindByContractAndPeriod(ctx, agg.ContractID(), period)
		if err != nil {
			t.Fatalf("FindByContractAndPeriod (period %d) failed: %v", i+1, err)
		}
		if len(got) != 1 {
			t.Errorf("period %d: expected exactly 1 invoice, got %d", i+1, len(got))
		}
	}
}
