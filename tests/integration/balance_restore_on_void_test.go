package integration

import (
	"context"
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// TestReissueInvoice_RestoresConsumedCredit is the regression test for issue #184:
// voiding an invoice (via CreditNoteService.ReissueInvoice) must return the credit
// the invoice consumed to the ledger, so the regenerated replacement can re-apply
// it instead of billing full price while the credit stays consumed forever.
func TestReissueInvoice_RestoresConsumedCredit(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()
	creditNoteRepo := inmemory.NewInMemoryCreditNoteRepository()
	registry := plugin.NewRegistry()

	billingSvc := service.NewBillingService(
		contractRepo, invoiceRepo, usageRepo,
		balance.BalanceConfig{}, priceRepo, productRepo,
		registry, service.BillingConfig{DaysUntilDue: 30}, clock,
		service.WithBalanceRepo(balanceRepo),
	)

	price := moneyJPY(5000)
	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, price)

	// Seed a 2000 JPY credit for the account.
	creditEntry, _ := balance.NewBalanceEntry(agg.AccountID(), moneyJPY(2000), balance.BalanceReasonGoodwill, clock.Now())
	if err := balanceRepo.Save(ctx, creditEntry); err != nil {
		t.Fatalf("failed to seed balance: %v", err)
	}

	// Original invoice consumes 2000 of the 5000 total.
	original, err := billingSvc.GenerateInvoice(ctx, agg.ContractID(), agg.CurrentPeriod())
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}
	if original.AppliedBalance().Amount().Cmp(big.NewRat(2000, 1)) != 0 {
		t.Fatalf("precondition: expected 2000 credit applied to original, got %s",
			original.AppliedBalance().Amount().RatString())
	}
	if bal, _ := balanceRepo.GetBalance(ctx, agg.AccountID(), price.Currency()); !bal.IsZero() {
		t.Fatalf("precondition: expected 0 remaining balance after original, got %s", bal.Amount().RatString())
	}

	creditNoteSvc := service.NewCreditNoteService(invoiceRepo, creditNoteRepo, registry, clock,
		service.WithBillingService(billingSvc),
	)

	replacement, err := creditNoteSvc.ReissueInvoice(ctx, original.ID(), "billing error")
	if err != nil {
		t.Fatalf("ReissueInvoice failed: %v", err)
	}

	// The consumed credit must have been restored and re-applied to the replacement.
	// Before the fix this was 0 (credit lost, full 5000 billed).
	if replacement.AppliedBalance().Amount().Cmp(big.NewRat(2000, 1)) != 0 {
		t.Errorf("expected 2000 credit re-applied on replacement, got %s",
			replacement.AppliedBalance().Amount().RatString())
	}
	if replacement.AmountDue().Amount().Cmp(big.NewRat(3000, 1)) != 0 {
		t.Errorf("expected amountDue 3000 on replacement, got %s",
			replacement.AmountDue().Amount().RatString())
	}

	// Exactly one refund audit row must exist for the voided original.
	refunds, err := balanceRepo.FindRefundsByInvoice(ctx, original.ID())
	if err != nil {
		t.Fatalf("FindRefundsByInvoice failed: %v", err)
	}
	if len(refunds) != 1 {
		t.Fatalf("expected exactly 1 refund recorded for voided original, got %d", len(refunds))
	}
	if refunds[0].Amount.Amount().Cmp(big.NewRat(2000, 1)) != 0 {
		t.Errorf("expected refund amount 2000, got %s", refunds[0].Amount.Amount().RatString())
	}

	// The original is voided; the replacement re-consumed the credit → net balance 0.
	if bal, _ := balanceRepo.GetBalance(ctx, agg.AccountID(), price.Currency()); !bal.IsZero() {
		t.Errorf("expected 0 remaining balance after reissue, got %s", bal.Amount().RatString())
	}
}

// TestRegenerateInvoice_RestoresConsumedCredit covers the same fix through the
// BillingService.RegenerateInvoice void-and-recreate path (issue #184).
func TestRegenerateInvoice_RestoresConsumedCredit(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()
	registry := plugin.NewRegistry()

	billingSvc := service.NewBillingService(
		contractRepo, invoiceRepo, usageRepo,
		balance.BalanceConfig{}, priceRepo, productRepo,
		registry, service.BillingConfig{DaysUntilDue: 30}, clock,
		service.WithBalanceRepo(balanceRepo),
	)

	price := moneyJPY(5000)
	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, price)

	creditEntry, _ := balance.NewBalanceEntry(agg.AccountID(), moneyJPY(2000), balance.BalanceReasonGoodwill, clock.Now())
	if err := balanceRepo.Save(ctx, creditEntry); err != nil {
		t.Fatalf("failed to seed balance: %v", err)
	}

	original, err := billingSvc.GenerateInvoice(ctx, agg.ContractID(), agg.CurrentPeriod())
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}
	if original.AppliedBalance().Amount().Cmp(big.NewRat(2000, 1)) != 0 {
		t.Fatalf("precondition: expected 2000 credit applied, got %s", original.AppliedBalance().Amount().RatString())
	}

	// Void the original outside the regenerate call (the void-and-recreate flow).
	loaded, err := invoiceRepo.FindByID(ctx, original.ID())
	if err != nil {
		t.Fatalf("failed to load original: %v", err)
	}
	if err := loaded.VoidWithReason("apply late coupon"); err != nil {
		t.Fatalf("failed to void original: %v", err)
	}
	if err := invoiceRepo.Save(ctx, loaded); err != nil {
		t.Fatalf("failed to save voided original: %v", err)
	}

	replacement, err := billingSvc.RegenerateInvoice(ctx, agg.ContractID(), agg.CurrentPeriod())
	if err != nil {
		t.Fatalf("RegenerateInvoice failed: %v", err)
	}

	if replacement.AppliedBalance().Amount().Cmp(big.NewRat(2000, 1)) != 0 {
		t.Errorf("expected 2000 credit re-applied on regenerated invoice, got %s",
			replacement.AppliedBalance().Amount().RatString())
	}

	refunds, err := balanceRepo.FindRefundsByInvoice(ctx, original.ID())
	if err != nil {
		t.Fatalf("FindRefundsByInvoice failed: %v", err)
	}
	if len(refunds) != 1 {
		t.Fatalf("expected exactly 1 refund for voided original, got %d", len(refunds))
	}
}

// TestRestoreBalancesForVoidedInvoice_Idempotent verifies a double restore /
// retry restores the consumed credit at most once (issue #184).
func TestRestoreBalancesForVoidedInvoice_Idempotent(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()
	registry := plugin.NewRegistry()

	billingSvc := service.NewBillingService(
		contractRepo, invoiceRepo, usageRepo,
		balance.BalanceConfig{}, priceRepo, productRepo,
		registry, service.BillingConfig{DaysUntilDue: 30}, clock,
		service.WithBalanceRepo(balanceRepo),
	)

	price := moneyJPY(5000)
	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, price)

	creditEntry, _ := balance.NewBalanceEntry(agg.AccountID(), moneyJPY(2000), balance.BalanceReasonGoodwill, clock.Now())
	if err := balanceRepo.Save(ctx, creditEntry); err != nil {
		t.Fatalf("failed to seed balance: %v", err)
	}

	original, err := billingSvc.GenerateInvoice(ctx, agg.ContractID(), agg.CurrentPeriod())
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}
	if bal, _ := balanceRepo.GetBalance(ctx, agg.AccountID(), price.Currency()); !bal.IsZero() {
		t.Fatalf("precondition: expected 0 balance after generate, got %s", bal.Amount().RatString())
	}

	// First restore returns the 2000 to the ledger.
	if err := billingSvc.RestoreBalancesForVoidedInvoice(ctx, original.ID()); err != nil {
		t.Fatalf("first restore failed: %v", err)
	}
	// Second restore must be a no-op (idempotent), not a double-credit.
	if err := billingSvc.RestoreBalancesForVoidedInvoice(ctx, original.ID()); err != nil {
		t.Fatalf("second restore failed: %v", err)
	}

	bal, _ := balanceRepo.GetBalance(ctx, agg.AccountID(), price.Currency())
	if bal.Amount().Cmp(big.NewRat(2000, 1)) != 0 {
		t.Errorf("expected balance restored to exactly 2000 (not doubled), got %s", bal.Amount().RatString())
	}

	refunds, err := balanceRepo.FindRefundsByInvoice(ctx, original.ID())
	if err != nil {
		t.Fatalf("FindRefundsByInvoice failed: %v", err)
	}
	if len(refunds) != 1 {
		t.Errorf("expected exactly 1 refund after double restore, got %d", len(refunds))
	}
}
