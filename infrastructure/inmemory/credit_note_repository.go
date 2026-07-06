package inmemory

import (
	"context"
	"fmt"
	"sync"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
)

// Compile-time interface check.
var _ invoice.CreditNoteRepository = (*InMemoryCreditNoteRepository)(nil)

// InMemoryCreditNoteRepository is an in-memory implementation of
// invoice.CreditNoteRepository.
type InMemoryCreditNoteRepository struct {
	mu          sync.RWMutex
	creditNotes map[shared.CreditNoteID]*invoice.CreditNote
	versions    map[shared.CreditNoteID]int // stored optimistic-locking version per credit note
}

// NewInMemoryCreditNoteRepository creates a new InMemoryCreditNoteRepository.
func NewInMemoryCreditNoteRepository() *InMemoryCreditNoteRepository {
	return &InMemoryCreditNoteRepository{
		creditNotes: make(map[shared.CreditNoteID]*invoice.CreditNote),
		versions:    make(map[shared.CreditNoteID]int),
	}
}

// Save persists a credit note using optimistic locking (issue #147).
//
// It compares the credit note's LoadedVersion against the stored version. If
// they differ, another caller has persisted a newer version since this caller
// loaded the credit note, and the save is rejected with tx.ErrVersionConflict.
// This makes the load → check → save sequence in the CreditNoteService
// transition methods race-safe: the loser of a concurrent Apply-vs-Refund (or
// any two transitions on the same issued credit note) is deterministically
// rejected instead of both succeeding.
//
// The first save of a given ID has no stored version to compare against and
// always succeeds, so callers that construct a fresh credit note
// (LoadedVersion 0) are unaffected.
func (r *InMemoryCreditNoteRepository) Save(_ context.Context, cn *invoice.CreditNote) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if storedVersion, ok := r.versions[cn.ID()]; ok {
		if cn.LoadedVersion() != storedVersion {
			return tx.ErrVersionConflict
		}
	}

	r.creditNotes[cn.ID()] = cn
	r.versions[cn.ID()] = cn.Version()
	// Sync loadedVersion so subsequent saves from the same pointer (the common
	// non-isolated in-memory case) compare against the just-persisted version.
	cn.SetVersion(cn.Version())
	return nil
}

// FindByID loads a credit note by its ID.
func (r *InMemoryCreditNoteRepository) FindByID(_ context.Context, id shared.CreditNoteID) (*invoice.CreditNote, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cn, ok := r.creditNotes[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("credit note %s not found", id))
	}
	return cn, nil
}

// FindByInvoiceID returns all credit notes for an invoice.
func (r *InMemoryCreditNoteRepository) FindByInvoiceID(_ context.Context, invoiceID shared.InvoiceID) ([]*invoice.CreditNote, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*invoice.CreditNote
	for _, cn := range r.creditNotes {
		if cn.InvoiceID() == invoiceID {
			result = append(result, cn)
		}
	}
	return result, nil
}

// FindByAccountID returns all credit notes for an account.
func (r *InMemoryCreditNoteRepository) FindByAccountID(_ context.Context, accountID shared.AccountID) ([]*invoice.CreditNote, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*invoice.CreditNote
	for _, cn := range r.creditNotes {
		if cn.AccountID() == accountID {
			result = append(result, cn)
		}
	}
	return result, nil
}

// FindByContractID returns all credit notes for a contract.
func (r *InMemoryCreditNoteRepository) FindByContractID(_ context.Context, contractID shared.ContractID) ([]*invoice.CreditNote, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*invoice.CreditNote
	for _, cn := range r.creditNotes {
		if cn.ContractID() == contractID {
			result = append(result, cn)
		}
	}
	return result, nil
}

// FindByStatus returns all credit notes with the given status.
func (r *InMemoryCreditNoteRepository) FindByStatus(_ context.Context, status invoice.CreditNoteStatus) ([]*invoice.CreditNote, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*invoice.CreditNote
	for _, cn := range r.creditNotes {
		if cn.Status() == status {
			result = append(result, cn)
		}
	}
	return result, nil
}
