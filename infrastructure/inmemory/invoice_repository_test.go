package inmemory

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
)

func newTestInvoice(t *testing.T, accountID shared.AccountID, contractID shared.ContractID, opts ...invoice.InvoiceOption) *invoice.Invoice {
	t.Helper()
	jpy := shared.CurrencyJPY
	subtotal := shared.NewMoney(new(big.Rat).SetInt64(10000), jpy)
	discount := shared.Zero(jpy)
	tax := shared.NewMoney(new(big.Rat).SetInt64(1000), jpy)

	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(),
		accountID,
		contractID,
		subtotal,
		discount,
		tax,
		opts...,
	)
	if err != nil {
		t.Fatalf("NewInvoice failed: %v", err)
	}
	return inv
}

func TestInMemoryInvoiceRepository_SaveAndFindByID(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	inv := newTestInvoice(t, accountID, contractID)

	if err := repo.Save(ctx, inv); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	found, err := repo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if found.ID() != inv.ID() {
		t.Errorf("expected ID %s, got %s", inv.ID(), found.ID())
	}
	if found.AccountID() != accountID {
		t.Errorf("expected accountID %s, got %s", accountID, found.AccountID())
	}
}

func TestInMemoryInvoiceRepository_FindByID_NotFound(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	_, err := repo.FindByID(ctx, shared.InvoiceID("nonexistent"))
	if err == nil {
		t.Error("expected error for non-existent invoice")
	}
}

func TestInMemoryInvoiceRepository_FindByContractID(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	contractID1 := shared.NewContractID()
	contractID2 := shared.NewContractID()

	inv1 := newTestInvoice(t, accountID, contractID1)
	inv2 := newTestInvoice(t, accountID, contractID1)
	inv3 := newTestInvoice(t, accountID, contractID2)

	for _, inv := range []*invoice.Invoice{inv1, inv2, inv3} {
		if err := repo.Save(ctx, inv); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	results, err := repo.FindByContractID(ctx, contractID1)
	if err != nil {
		t.Fatalf("FindByContractID failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 invoices for contractID1, got %d", len(results))
	}
}

func TestInMemoryInvoiceRepository_FindByAccountID(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	accountID1 := shared.NewAccountID()
	accountID2 := shared.NewAccountID()
	contractID := shared.NewContractID()

	inv1 := newTestInvoice(t, accountID1, contractID)
	inv2 := newTestInvoice(t, accountID1, contractID)
	inv3 := newTestInvoice(t, accountID2, contractID)

	for _, inv := range []*invoice.Invoice{inv1, inv2, inv3} {
		if err := repo.Save(ctx, inv); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	results, err := repo.FindByAccountID(ctx, accountID1)
	if err != nil {
		t.Fatalf("FindByAccountID failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 invoices for accountID1, got %d", len(results))
	}
}

func TestInMemoryInvoiceRepository_FindByStatus(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()

	// Draft invoice (default).
	inv1 := newTestInvoice(t, accountID, contractID)
	// Finalized invoice.
	inv2 := newTestInvoiceInStatus(t, accountID, contractID, invoice.InvoiceStatusFinalized)
	// Another draft.
	inv3 := newTestInvoice(t, accountID, contractID)

	for _, inv := range []*invoice.Invoice{inv1, inv2, inv3} {
		if err := repo.Save(ctx, inv); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	drafts, err := repo.FindByStatus(ctx, invoice.InvoiceStatusDraft)
	if err != nil {
		t.Fatalf("FindByStatus failed: %v", err)
	}
	if len(drafts) != 2 {
		t.Errorf("expected 2 draft invoices, got %d", len(drafts))
	}

	finalized, err := repo.FindByStatus(ctx, invoice.InvoiceStatusFinalized)
	if err != nil {
		t.Fatalf("FindByStatus failed: %v", err)
	}
	if len(finalized) != 1 {
		t.Errorf("expected 1 finalized invoice, got %d", len(finalized))
	}
}

func TestInMemoryInvoiceRepository_FindByContractAndStatus(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	contractID1 := shared.NewContractID()
	contractID2 := shared.NewContractID()

	inv1 := newTestInvoice(t, accountID, contractID1)
	inv2 := newTestInvoiceInStatus(t, accountID, contractID1, invoice.InvoiceStatusFinalized)
	inv3 := newTestInvoice(t, accountID, contractID2)

	for _, inv := range []*invoice.Invoice{inv1, inv2, inv3} {
		if err := repo.Save(ctx, inv); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	results, err := repo.FindByContractAndStatus(ctx, contractID1, invoice.InvoiceStatusDraft)
	if err != nil {
		t.Fatalf("FindByContractAndStatus failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 draft invoice for contractID1, got %d", len(results))
	}
}

func TestInMemoryInvoiceRepository_FindByContractAndPeriod(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()

	period1Start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	period1End := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	period1, err := shared.NewDateRange(period1Start, period1End)
	if err != nil {
		t.Fatalf("NewDateRange failed: %v", err)
	}

	period2Start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	period2End := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	period2, err := shared.NewDateRange(period2Start, period2End)
	if err != nil {
		t.Fatalf("NewDateRange failed: %v", err)
	}

	inv1 := newTestInvoice(t, accountID, contractID, invoice.WithBillingPeriod(period1))
	inv2 := newTestInvoice(t, accountID, contractID, invoice.WithBillingPeriod(period2))

	for _, inv := range []*invoice.Invoice{inv1, inv2} {
		if err := repo.Save(ctx, inv); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	results, err := repo.FindByContractAndPeriod(ctx, contractID, period1)
	if err != nil {
		t.Fatalf("FindByContractAndPeriod failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 invoice for period1, got %d", len(results))
	}
	if results[0].ID() != inv1.ID() {
		t.Errorf("expected invoice %s, got %s", inv1.ID(), results[0].ID())
	}

	// Non-matching period returns empty.
	noMatchStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	noMatchEnd := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	noMatch, _ := shared.NewDateRange(noMatchStart, noMatchEnd)
	empty, err := repo.FindByContractAndPeriod(ctx, contractID, noMatch)
	if err != nil {
		t.Fatalf("FindByContractAndPeriod failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 invoices for non-matching period, got %d", len(empty))
	}
}

func TestInMemoryInvoiceRepository_FindUnpaidByContract(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()

	// Draft (unpaid).
	inv1 := newTestInvoice(t, accountID, contractID)
	// Finalized (unpaid).
	inv2 := newTestInvoiceInStatus(t, accountID, contractID, invoice.InvoiceStatusFinalized)
	// Paid (not unpaid).
	inv3 := newTestInvoiceInStatus(t, accountID, contractID, invoice.InvoiceStatusPaid)
	// Voided (not unpaid).
	inv4 := newTestInvoiceInStatus(t, accountID, contractID, invoice.InvoiceStatusVoided)
	// Overdue (unpaid).
	inv5 := newTestInvoiceInStatus(t, accountID, contractID, invoice.InvoiceStatusOverdue)

	for _, inv := range []*invoice.Invoice{inv1, inv2, inv3, inv4, inv5} {
		if err := repo.Save(ctx, inv); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	results, err := repo.FindUnpaidByContract(ctx, contractID)
	if err != nil {
		t.Fatalf("FindUnpaidByContract failed: %v", err)
	}
	// Draft, Finalized, Overdue = 3 unpaid.
	if len(results) != 3 {
		t.Errorf("expected 3 unpaid invoices, got %d", len(results))
	}
}

func TestInMemoryInvoiceRepository_FindOverdue(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	clock := shared.FixedClock{FixedTime: now}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()

	// Explicitly overdue status.
	inv1 := newTestInvoiceInStatus(t, accountID, contractID, invoice.InvoiceStatusOverdue)

	// Issued with past due date — should be found as overdue.
	pastDue := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	inv2 := newTestInvoiceInStatus(t, accountID, contractID, invoice.InvoiceStatusIssued,
		invoice.WithDueDate(pastDue),
	)

	// Finalized with past due date — should also be found as overdue.
	inv3 := newTestInvoiceInStatus(t, accountID, contractID, invoice.InvoiceStatusFinalized,
		invoice.WithDueDate(pastDue),
	)

	// Issued with future due date — should NOT be found.
	futureDue := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	inv4 := newTestInvoiceInStatus(t, accountID, contractID, invoice.InvoiceStatusIssued,
		invoice.WithDueDate(futureDue),
	)

	// Paid — should NOT be found.
	inv5 := newTestInvoiceInStatus(t, accountID, contractID, invoice.InvoiceStatusPaid)

	for _, inv := range []*invoice.Invoice{inv1, inv2, inv3, inv4, inv5} {
		if err := repo.Save(ctx, inv); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	results, err := repo.FindOverdue(ctx)
	if err != nil {
		t.Fatalf("FindOverdue failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 overdue invoices, got %d", len(results))
	}
}

// TestInMemoryInvoiceRepository_Save_OptimisticLock_RejectsConcurrentFinalize
// is the #130 regression test: when two callers each load an independent copy
// of the same draft invoice (as a real RDBMS hands each transaction its own row
// snapshot) and both finalize, exactly one Save wins and the loser is
// deterministically rejected with tx.ErrVersionConflict. Without optimistic
// locking both saves would succeed (last-writer-wins), letting the caller fire
// OnInvoiceIssued twice.
func TestInMemoryInvoiceRepository_Save_OptimisticLock_RejectsConcurrentFinalize(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	inv := newTestInvoice(t, shared.NewAccountID(), shared.NewContractID())
	if err := repo.Save(ctx, inv); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	stored, err := repo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}

	// Two independent loads (snapshot round-trip simulates per-tx isolation).
	winner, err := invoice.InvoiceFromSnapshot(stored.ToSnapshot())
	if err != nil {
		t.Fatalf("clone winner failed: %v", err)
	}
	loser, err := invoice.InvoiceFromSnapshot(stored.ToSnapshot())
	if err != nil {
		t.Fatalf("clone loser failed: %v", err)
	}

	if err := winner.Finalize(); err != nil {
		t.Fatalf("winner Finalize failed: %v", err)
	}
	if err := repo.Save(ctx, winner); err != nil {
		t.Fatalf("winner Save should succeed, got: %v", err)
	}

	if err := loser.Finalize(); err != nil {
		t.Fatalf("loser Finalize failed: %v", err)
	}
	err = repo.Save(ctx, loser)
	if !errors.Is(err, tx.ErrVersionConflict) {
		t.Fatalf("loser Save should be rejected with ErrVersionConflict, got: %v", err)
	}

	// The persisted invoice is finalized exactly once, at version 1.
	final, err := repo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("final FindByID failed: %v", err)
	}
	if final.Status() != invoice.InvoiceStatusFinalized {
		t.Errorf("stored status: got %s, want finalized", final.Status())
	}
	if final.Version() != 1 {
		t.Errorf("stored version: got %d, want 1", final.Version())
	}
}

// TestInMemoryInvoiceRepository_Save_SamePointerReSaveSucceeds guards the
// common non-isolated path: loading via FindByID (which returns the stored
// pointer) and re-saving after a mutation must not spuriously conflict, because
// Save syncs loadedVersion to the persisted version.
func TestInMemoryInvoiceRepository_Save_SamePointerReSaveSucceeds(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	inv := newTestInvoice(t, shared.NewAccountID(), shared.NewContractID())
	if err := repo.Save(ctx, inv); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	loaded, err := repo.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if err := loaded.Finalize(); err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}
	if err := repo.Save(ctx, loaded); err != nil {
		t.Fatalf("re-save after finalize should succeed, got: %v", err)
	}
	// A second save from the same synced pointer also succeeds.
	if err := repo.Save(ctx, loaded); err != nil {
		t.Fatalf("second re-save should succeed, got: %v", err)
	}
}

func TestInMemoryInvoiceRepository_FindByIDAsOf(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	inv := newTestInvoice(t, accountID, contractID)

	if err := repo.Save(ctx, inv); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// In-memory implementation returns current state regardless of asOf.
	asOf := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	found, err := repo.FindByIDAsOf(ctx, inv.ID(), asOf)
	if err != nil {
		t.Fatalf("FindByIDAsOf failed: %v", err)
	}
	if found.ID() != inv.ID() {
		t.Errorf("expected ID %s, got %s", inv.ID(), found.ID())
	}

	// Not found case.
	_, err = repo.FindByIDAsOf(ctx, shared.InvoiceID("nonexistent"), asOf)
	if err == nil {
		t.Error("expected error for non-existent invoice")
	}
}

// TestInMemoryInvoiceRepository_Save_PeriodUniqueness exercises the issue #149
// per-period uniqueness constraint: a second DISTINCT non-voided invoice for the
// same (contract, period) is rejected with a conflict DomainError, while voided
// and proration invoices are exempt and may coexist with the period's invoice.
func TestInMemoryInvoiceRepository_Save_PeriodUniqueness(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	repo := NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	period, err := shared.NewDateRange(
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewDateRange failed: %v", err)
	}

	// First regular period invoice: accepted.
	first := newTestInvoice(t, accountID, contractID, invoice.WithBillingPeriod(period))
	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("first Save should succeed: %v", err)
	}

	// Second DISTINCT non-voided invoice for the same period: rejected as conflict.
	second := newTestInvoice(t, accountID, contractID, invoice.WithBillingPeriod(period))
	err = repo.Save(ctx, second)
	var domainErr *shared.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != shared.ErrCodeConflict {
		t.Fatalf("expected conflict DomainError for duplicate period invoice, got: %v", err)
	}

	// Re-saving the SAME record (update / finalize) must not self-collide.
	if err := repo.Save(ctx, first); err != nil {
		t.Errorf("re-saving the same invoice must not conflict, got: %v", err)
	}

	// A proration invoice for the same period is exempt and coexists.
	proration := newTestInvoice(t, accountID, contractID,
		invoice.WithBillingPeriod(period),
		invoice.WithMetadata(map[string]string{
			invoice.MetadataKeyInvoiceType: invoice.InvoiceTypeProration,
		}),
	)
	if err := repo.Save(ctx, proration); err != nil {
		t.Errorf("proration invoice must be exempt from period uniqueness, got: %v", err)
	}

	// A voided invoice for the same period is exempt and coexists.
	voided := newTestInvoiceInStatus(t, accountID, contractID, invoice.InvoiceStatusVoided,
		invoice.WithBillingPeriod(period),
	)
	if err := repo.Save(ctx, voided); err != nil {
		t.Errorf("voided invoice must be exempt from period uniqueness, got: %v", err)
	}
}
