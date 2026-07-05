package inmemory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
)

// Compile-time interface check.
var _ invoice.Repository = (*InMemoryInvoiceRepository)(nil)

// InMemoryInvoiceRepository is an in-memory implementation of invoice.Repository.
type InMemoryInvoiceRepository struct {
	mu       sync.RWMutex
	invoices map[shared.InvoiceID]*invoice.Invoice
	versions map[shared.InvoiceID]int // stored optimistic-locking version per invoice
	clock    shared.Clock
}

// NewInMemoryInvoiceRepository creates a new InMemoryInvoiceRepository.
func NewInMemoryInvoiceRepository(clock shared.Clock) *InMemoryInvoiceRepository {
	return &InMemoryInvoiceRepository{
		invoices: make(map[shared.InvoiceID]*invoice.Invoice),
		versions: make(map[shared.InvoiceID]int),
		clock:    clock,
	}
}

// Save persists an invoice using optimistic locking (issue #130).
//
// It compares the invoice's LoadedVersion against the stored version. If they
// differ, another caller has persisted a newer version since this caller
// loaded the invoice, and the save is rejected with tx.ErrVersionConflict.
// This makes the load → check → save sequence in BillingService.FinalizeInvoice
// race-safe: the loser of two concurrent finalizations is deterministically
// rejected instead of both succeeding (which would double-fire OnInvoiceIssued).
//
// The first save of a given ID has no stored version to compare against and
// always succeeds, so callers that construct a fresh invoice (LoadedVersion 0)
// are unaffected.
func (r *InMemoryInvoiceRepository) Save(_ context.Context, inv *invoice.Invoice) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if storedVersion, ok := r.versions[inv.ID()]; ok {
		if inv.LoadedVersion() != storedVersion {
			return tx.ErrVersionConflict
		}
	}

	r.invoices[inv.ID()] = inv
	r.versions[inv.ID()] = inv.Version()
	// Sync loadedVersion so subsequent saves from the same pointer (the common
	// non-isolated in-memory case) compare against the just-persisted version.
	inv.SetVersion(inv.Version())
	return nil
}

// FindByID loads an invoice by its ID.
func (r *InMemoryInvoiceRepository) FindByID(_ context.Context, id shared.InvoiceID) (*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	inv, ok := r.invoices[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("invoice %s not found", id))
	}
	return inv, nil
}

// FindByContractID returns all invoices for a contract.
func (r *InMemoryInvoiceRepository) FindByContractID(_ context.Context, contractID shared.ContractID) ([]*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*invoice.Invoice
	for _, inv := range r.invoices {
		if inv.ContractID() == contractID {
			result = append(result, inv)
		}
	}
	return result, nil
}

// FindByAccountID returns all invoices for an account.
func (r *InMemoryInvoiceRepository) FindByAccountID(_ context.Context, accountID shared.AccountID) ([]*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*invoice.Invoice
	for _, inv := range r.invoices {
		if inv.AccountID() == accountID {
			result = append(result, inv)
		}
	}
	return result, nil
}

// FindOverdue returns all overdue invoices.
func (r *InMemoryInvoiceRepository) FindOverdue(_ context.Context) ([]*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := r.clock.Now()
	var result []*invoice.Invoice
	for _, inv := range r.invoices {
		if inv.Status() == invoice.InvoiceStatusOverdue {
			result = append(result, inv)
		} else if (inv.Status() == invoice.InvoiceStatusIssued || inv.Status() == invoice.InvoiceStatusFinalized) &&
			!inv.DueDate().IsZero() && inv.DueDate().Before(now) {
			result = append(result, inv)
		}
	}
	return result, nil
}

// FindByStatus returns all invoices with the given status.
func (r *InMemoryInvoiceRepository) FindByStatus(_ context.Context, status invoice.InvoiceStatus) ([]*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*invoice.Invoice
	for _, inv := range r.invoices {
		if inv.Status() == status {
			result = append(result, inv)
		}
	}
	return result, nil
}

// FindByIDAsOf loads an invoice as of a specific point in time.
// In this in-memory implementation, we simply return the current state
// since we don't track historical invoice states.
func (r *InMemoryInvoiceRepository) FindByIDAsOf(_ context.Context, id shared.InvoiceID, _ time.Time) (*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	inv, ok := r.invoices[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("invoice %s not found", id))
	}
	return inv, nil
}

// FindByContractAndStatus returns invoices for a contract with a specific status.
func (r *InMemoryInvoiceRepository) FindByContractAndStatus(_ context.Context, contractID shared.ContractID, status invoice.InvoiceStatus) ([]*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*invoice.Invoice
	for _, inv := range r.invoices {
		if inv.ContractID() == contractID && inv.Status() == status {
			result = append(result, inv)
		}
	}
	return result, nil
}

// FindByContractAndPeriod returns invoices for a contract within a billing period.
func (r *InMemoryInvoiceRepository) FindByContractAndPeriod(_ context.Context, contractID shared.ContractID, period shared.DateRange) ([]*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*invoice.Invoice
	for _, inv := range r.invoices {
		if inv.ContractID() == contractID &&
			inv.BillingPeriod().Start().Equal(period.Start()) &&
			inv.BillingPeriod().End().Equal(period.End()) {
			result = append(result, inv)
		}
	}
	return result, nil
}

// FindUnpaidByContract returns all unpaid invoices (Draft, Finalized, Issued, Overdue) for a contract.
func (r *InMemoryInvoiceRepository) FindUnpaidByContract(_ context.Context, contractID shared.ContractID) ([]*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	unpaidStatuses := map[invoice.InvoiceStatus]bool{
		invoice.InvoiceStatusDraft:       true,
		invoice.InvoiceStatusFinalized:   true,
		invoice.InvoiceStatusIssued:      true,
		invoice.InvoiceStatusOverdue:     true,
		invoice.InvoiceStatusPartialPaid: true,
	}

	var result []*invoice.Invoice
	for _, inv := range r.invoices {
		if inv.ContractID() == contractID && unpaidStatuses[inv.Status()] {
			result = append(result, inv)
		}
	}
	return result, nil
}
