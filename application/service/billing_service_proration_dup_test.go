package service

// Tests for issue #232 (duplicate-invoice guards must exempt proration
// invoices — the guards must mirror the per-period uniqueness contract in
// invoice.Repository.Save, which ranges over non-voided, non-proration
// invoices only) and issue #241 item 3 (a wired balance repository whose
// transaction-scoped Repos omits Balances must fail loudly instead of
// silently skipping credit application).

import (
	"context"
	"strings"
	"testing"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// newProrationInvoiceForTest builds a non-voided proration adjustment invoice
// (metadata invoice_type=proration) for the aggregate's account/contract.
func newProrationInvoiceForTest(t *testing.T, agg *contract.ContractAggregate, period shared.DateRange, amount shared.Money) *invoice.Invoice {
	t.Helper()
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		amount, jpy(0), jpy(0),
		invoice.WithBillingPeriod(period),
		invoice.WithMetadata(map[string]string{
			invoice.MetadataKeyInvoiceType: invoice.InvoiceTypeProration,
		}),
	)
	if err != nil {
		t.Fatalf("unexpected error creating proration invoice: %v", err)
	}
	if !inv.IsProration() {
		t.Fatal("test fixture is not recognised as a proration invoice")
	}
	return inv
}

// TestGenerateInvoice_ProrationDoesNotBlockPeriodInvoice guards issue #232:
// a proration adjustment invoice (e.g. from a mid-period upgrade) coexists
// with the period's regular invoice, so its presence must not block
// GenerateInvoice for the same period. This is the arrears scenario: the
// upgrade proration is billed mid-period, the regular period invoice at
// period end.
func TestGenerateInvoice_ProrationDoesNotBlockPeriodInvoice(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	prorationInv := newProrationInvoiceForTest(t, agg, period, jpy(2000))
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{prorationInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), period)
	if err != nil {
		t.Fatalf("proration invoice must not block the period's regular invoice: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice")
	}
	if inv.IsProration() {
		t.Error("generated regular invoice must not be tagged as proration")
	}
}

// TestGenerateInvoice_OneTime_ProrationDoesNotBlock guards issue #232 for the
// one-time branch of checkDuplicateInvoice. GenerateProrationInvoice guards
// contract STATUS (active) but not contract TYPE, so a proration invoice can
// exist for an active one-time contract; "only one invoice ever" means one
// REGULAR invoice.
func TestGenerateInvoice_OneTime_ProrationDoesNotBlock(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeOneTime, jpy(5000))

	prorationInv := newProrationInvoiceForTest(t, agg, currentPeriodOf(agg), jpy(2000))
	invRepo := &mockInvoiceRepo{existingByContract: []*invoice.Invoice{prorationInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("proration invoice must not block the one-time contract's regular invoice: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice")
	}
}

// TestGenerateInvoice_ProrationPlusRegular_DuplicateStillBlocked verifies the
// duplicate guard still fires when a REGULAR non-voided invoice exists for the
// period, even with a proration invoice alongside it (issue #232 regression c).
func TestGenerateInvoice_ProrationPlusRegular_DuplicateStillBlocked(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	prorationInv := newProrationInvoiceForTest(t, agg, period, jpy(2000))
	regularInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{prorationInv, regularInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	_, err = svc.GenerateInvoice(context.Background(), agg.ContractID(), period)
	if err == nil {
		t.Fatal("a regular non-voided invoice for the period must still be rejected as duplicate")
	}
	assertDomainError(t, err, shared.ErrCodeConflict)
}

// TestRegenerateInvoice_ProrationCoexists_Succeeds guards issue #232 on the
// RegenerateInvoice inline duplicate loop and on rejectIfActivePeriodInvoice
// (its in-tx duplicate re-check): void-and-recreate of the period's regular
// invoice must succeed even though a non-voided proration invoice exists for
// the same period, and the revision chain must link to the voided REGULAR
// invoice (a non-voided proration is never a revision target).
func TestRegenerateInvoice_ProrationCoexists_Succeeds(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating voided invoice: %v", err)
	}
	transitionInvoiceForTest(voidedInv, invoice.InvoiceStatusVoided)
	prorationInv := newProrationInvoiceForTest(t, agg, period, jpy(2000))

	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{voidedInv, prorationInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	inv, err := svc.RegenerateInvoice(context.Background(), agg.ContractID(), period)
	if err != nil {
		t.Fatalf("a coexisting proration invoice must not block regeneration: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invoice")
	}
	if inv.RevisionOf() == nil || *inv.RevisionOf() != voidedInv.ID() {
		t.Errorf("expected revision chain to link to voided regular invoice %s, got %v",
			voidedInv.ID(), inv.RevisionOf())
	}
	if inv.Metadata()[invoice.MetadataKeyInvoiceType] != invoice.InvoiceTypeRegeneration {
		t.Errorf("expected invoice_type=regeneration, got %q",
			inv.Metadata()[invoice.MetadataKeyInvoiceType])
	}
}

// TestRejectIfActivePeriodInvoice_ProrationExempt exercises the in-tx re-check
// helper directly: a non-voided proration invoice must not raise a conflict,
// while a regular non-voided invoice must.
func TestRejectIfActivePeriodInvoice_ProrationExempt(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	period := currentPeriodOf(agg)

	prorationInv := newProrationInvoiceForTest(t, agg, period, jpy(2000))
	invRepo := &mockInvoiceRepo{existingByPeriod: []*invoice.Invoice{prorationInv}}
	svc := newBillingSvcWithPrice(agg, invRepo, priceEntity, clock)

	if err := svc.rejectIfActivePeriodInvoice(context.Background(), agg.ContractID(), period); err != nil {
		t.Fatalf("proration invoice must be exempt from the active-period check: %v", err)
	}

	regularInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}
	invRepo.existingByPeriod = []*invoice.Invoice{prorationInv, regularInv}
	err = svc.rejectIfActivePeriodInvoice(context.Background(), agg.ContractID(), period)
	if err == nil {
		t.Fatal("regular non-voided invoice must raise a conflict")
	}
	assertDomainError(t, err, shared.ErrCodeConflict)
}

// --- issue #241 item 3: silent credit-application skip ---

// staticTxManager is a TxManager stand-in for a consumer-provided manager that
// passes a FIXED Repos set to the closure. It deliberately does NOT implement
// the reposProvider interface, so tx.Run cannot backfill repos it omits —
// modelling a real database TxManager whose Repos wiring forgot a repository.
type staticTxManager struct{ repos tx.Repos }

func (m *staticTxManager) RunInTx(ctx context.Context, fn func(context.Context, tx.Repos) error) error {
	return fn(ctx, m.repos)
}

// TestGenerateInvoice_BalanceRepoWiredButTxReposOmitBalances_FailsLoudly guards
// issue #241 item 3: when the service holds a balance repository
// (WithBalanceRepo) but the TxManager's Repos omits Balances, the pipeline must
// fail with a configuration DomainError instead of silently skipping credit
// application (silent misbilling).
func TestGenerateInvoice_BalanceRepoWiredButTxReposOmitBalances_FailsLoudly(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	invRepo := &mockInvoiceRepo{}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
		WithBalanceRepo(&mockBalanceRepo{}),
		WithBillingTxManager(&staticTxManager{repos: tx.Repos{
			Contracts: &mockContractRepo{agg: agg},
			Invoices:  invRepo,
			// Balances deliberately omitted.
		}}),
	)

	_, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err == nil {
		t.Fatal("expected a configuration error when Balances is missing from the tx-scoped Repos")
	}
	assertDomainError(t, err, shared.ErrCodeValidation)
	if !strings.Contains(err.Error(), "Balances") {
		t.Errorf("expected error to name the missing Balances repo, got %q", err.Error())
	}
	if invRepo.saved != nil {
		t.Error("no invoice must be saved when the pipeline aborts on misconfiguration")
	}
}

// TestGenerateInvoice_NoBalanceRepo_TxReposOmitBalances_StillSkips verifies the
// pre-#241 behavior is preserved when NO balance repository was ever wired:
// credit application is intentionally disabled and the pipeline proceeds.
func TestGenerateInvoice_NoBalanceRepo_TxReposOmitBalances_StillSkips(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))
	invRepo := &mockInvoiceRepo{}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
		// No WithBalanceRepo.
		WithBillingTxManager(&staticTxManager{repos: tx.Repos{
			Contracts: &mockContractRepo{agg: agg},
			Invoices:  invRepo,
		}}),
	)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error without a wired balance repo: %v", err)
	}
	if !inv.AppliedBalance().IsZero() {
		t.Errorf("expected zero applied balance, got %s", inv.AppliedBalance().Amount().RatString())
	}
}

// TestRestoreBalancesForVoidedInvoice_BalanceRepoWiredButTxReposOmitBalances_FailsLoudly
// extends the issue #241 loud-failure policy to RestoreBalancesForVoidedInvoice
// (the reversal used by CreditNoteService.ReissueInvoice-style void paths): a
// reissue that silently skips restoring the voided invoice's consumed credit is
// the same misbilling class as silently skipping credit application.
func TestRestoreBalancesForVoidedInvoice_BalanceRepoWiredButTxReposOmitBalances_FailsLoudly(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("unexpected error creating voided invoice: %v", err)
	}
	transitionInvoiceForTest(voidedInv, invoice.InvoiceStatusVoided)
	invRepo := &mockInvoiceRepo{byID: voidedInv}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
		WithBalanceRepo(&mockBalanceRepo{}),
		WithBillingTxManager(&staticTxManager{repos: tx.Repos{
			Contracts: &mockContractRepo{agg: agg},
			Invoices:  invRepo,
			// Balances deliberately omitted.
		}}),
	)

	err = svc.RestoreBalancesForVoidedInvoice(context.Background(), voidedInv.ID())
	if err == nil {
		t.Fatal("expected a configuration error when Balances is missing from the tx-scoped Repos")
	}
	assertDomainError(t, err, shared.ErrCodeValidation)
	if !strings.Contains(err.Error(), "Balances") {
		t.Errorf("expected error to name the missing Balances repo, got %q", err.Error())
	}
}

// TestRestoreBalancesForVoidedInvoice_NoBalanceRepo_StillNoop verifies the
// documented no-op is preserved when NO balance repository was ever wired
// (nothing to restore), while the voided-status guard still runs first.
func TestRestoreBalancesForVoidedInvoice_NoBalanceRepo_StillNoop(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(1000))

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("unexpected error creating voided invoice: %v", err)
	}
	transitionInvoiceForTest(voidedInv, invoice.InvoiceStatusVoided)
	invRepo := &mockInvoiceRepo{byID: voidedInv}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
		// No WithBalanceRepo.
		WithBillingTxManager(&staticTxManager{repos: tx.Repos{
			Contracts: &mockContractRepo{agg: agg},
			Invoices:  invRepo,
		}}),
	)

	if err := svc.RestoreBalancesForVoidedInvoice(context.Background(), voidedInv.ID()); err != nil {
		t.Fatalf("expected no-op without a wired balance repo, got: %v", err)
	}

	// The voided-status guard still runs before the no-op: a draft invoice is rejected.
	draftInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(1000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("unexpected error creating draft invoice: %v", err)
	}
	invRepo.byID = draftInv
	err = svc.RestoreBalancesForVoidedInvoice(context.Background(), draftInv.ID())
	if err == nil {
		t.Fatal("expected voided-status guard to reject a non-voided invoice")
	}
	assertDomainError(t, err, shared.ErrCodeBusinessRule)
}
