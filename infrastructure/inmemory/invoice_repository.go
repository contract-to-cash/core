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
//
// Per-period uniqueness (issue #149): this reference implementation also mirrors
// the partial unique index recommended in invoice.Repository.Save. Saving a
// non-voided, non-proration invoice with a non-zero billing period is rejected
// with a shared.ErrCodeConflict DomainError if a DIFFERENT non-voided,
// non-proration invoice already exists for the same (contract, period). This
// closes the concurrent-GenerateInvoice window under the mutex the same way a
// database unique index would. Voided and proration invoices are exempt so
// void-and-recreate (RegenerateInvoice) and proration adjustments still work.
func (r *InMemoryInvoiceRepository) Save(_ context.Context, inv *invoice.Invoice) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if storedVersion, ok := r.versions[inv.ID()]; ok {
		if inv.LoadedVersion() != storedVersion {
			return tx.ErrVersionConflict
		}
	}

	if err := r.checkPeriodUniquenessLocked(inv); err != nil {
		return err
	}

	// Store an ISOLATED copy (snapshot round-trip), not the caller's pointer, so
	// that the caller's later mutations to inv cannot leak into the repository
	// or into concurrent readers (issue #152). The snapshot restores version and
	// loadedVersion together, so the stored copy's optimistic-locking baseline
	// matches the just-persisted version.
	stored, err := cloneInvoice(inv)
	if err != nil {
		return err
	}
	r.invoices[inv.ID()] = stored
	r.versions[inv.ID()] = inv.Version()
	// Sync loadedVersion so subsequent saves from the same pointer (the common
	// non-isolated in-memory case) compare against the just-persisted version.
	inv.SetVersion(inv.Version())
	return nil
}

// cloneInvoice returns an isolated deep copy of inv via the snapshot round-trip.
// InvoiceFromSnapshot restores version and loadedVersion from the single stored
// version field, so the clone carries the same optimistic-locking baseline as
// the original (a freshly loaded entity has loadedVersion == version).
func cloneInvoice(inv *invoice.Invoice) (*invoice.Invoice, error) {
	return invoice.InvoiceFromSnapshot(inv.ToSnapshot())
}

// checkPeriodUniquenessLocked enforces the per-period uniqueness contract from
// invoice.Repository.Save. It must be called with r.mu held. It rejects the save
// when inv participates in the constraint and a DIFFERENT stored invoice already
// occupies the same (contract, period) slot.
func (r *InMemoryInvoiceRepository) checkPeriodUniquenessLocked(inv *invoice.Invoice) error {
	if !participatesInPeriodUniqueness(inv) {
		return nil
	}
	for id, existing := range r.invoices {
		if id == inv.ID() {
			continue // same record (update / finalize) never collides with itself
		}
		if !participatesInPeriodUniqueness(existing) {
			continue
		}
		if existing.ContractID() == inv.ContractID() &&
			existing.BillingPeriod().Start().Equal(inv.BillingPeriod().Start()) &&
			existing.BillingPeriod().End().Equal(inv.BillingPeriod().End()) {
			return shared.NewDomainError(shared.ErrCodeConflict,
				"invoice already exists for this billing period")
		}
	}
	return nil
}

// participatesInPeriodUniqueness reports whether inv is subject to the
// (contract_id, billing_period) uniqueness constraint. Voided invoices,
// proration adjustments, and invoices without a billing period are exempt —
// matching the partial unique index recommended in invoice.Repository.Save.
// The predicate is defined once on the domain entity
// (Invoice.ParticipatesInPeriodUniqueness) so this repository and the
// BillingService duplicate-invoice guards share a single source of truth
// (issue #232).
func participatesInPeriodUniqueness(inv *invoice.Invoice) bool {
	return inv.ParticipatesInPeriodUniqueness()
}

// FindByID loads an invoice by its ID.
//
// Returns an ISOLATED copy (snapshot round-trip) so concurrent load-modify
// callers never share a live pointer (issue #152). This makes the optimistic
// lock in Save observable: two independent loads each carry loadedVersion, so
// the loser of two concurrent Saves is rejected with tx.ErrVersionConflict.
func (r *InMemoryInvoiceRepository) FindByID(_ context.Context, id shared.InvoiceID) (*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	inv, ok := r.invoices[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("invoice %s not found", id))
	}
	return cloneInvoice(inv)
}

// FindByContractID returns all invoices for a contract.
func (r *InMemoryInvoiceRepository) FindByContractID(_ context.Context, contractID shared.ContractID) ([]*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*invoice.Invoice
	for _, inv := range r.invoices {
		if inv.ContractID() == contractID {
			clone, err := cloneInvoice(inv)
			if err != nil {
				return nil, err
			}
			result = append(result, clone)
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
			clone, err := cloneInvoice(inv)
			if err != nil {
				return nil, err
			}
			result = append(result, clone)
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
		overdue := inv.Status() == invoice.InvoiceStatusOverdue
		dueElapsed := (inv.Status() == invoice.InvoiceStatusIssued || inv.Status() == invoice.InvoiceStatusFinalized) &&
			!inv.DueDate().IsZero() && inv.DueDate().Before(now)
		if overdue || dueElapsed {
			clone, err := cloneInvoice(inv)
			if err != nil {
				return nil, err
			}
			result = append(result, clone)
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
			clone, err := cloneInvoice(inv)
			if err != nil {
				return nil, err
			}
			result = append(result, clone)
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
	return cloneInvoice(inv)
}

// FindByContractAndStatus returns invoices for a contract with a specific status.
func (r *InMemoryInvoiceRepository) FindByContractAndStatus(_ context.Context, contractID shared.ContractID, status invoice.InvoiceStatus) ([]*invoice.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*invoice.Invoice
	for _, inv := range r.invoices {
		if inv.ContractID() == contractID && inv.Status() == status {
			clone, err := cloneInvoice(inv)
			if err != nil {
				return nil, err
			}
			result = append(result, clone)
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
			clone, err := cloneInvoice(inv)
			if err != nil {
				return nil, err
			}
			result = append(result, clone)
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
			clone, err := cloneInvoice(inv)
			if err != nil {
				return nil, err
			}
			result = append(result, clone)
		}
	}
	return result, nil
}
