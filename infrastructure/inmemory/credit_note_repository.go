package inmemory

import (
	"context"
	"fmt"
	"sync"

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
}

// NewInMemoryCreditNoteRepository creates a new InMemoryCreditNoteRepository.
func NewInMemoryCreditNoteRepository() *InMemoryCreditNoteRepository {
	return &InMemoryCreditNoteRepository{
		creditNotes: make(map[shared.CreditNoteID]*invoice.CreditNote),
	}
}

// Save persists a credit note.
func (r *InMemoryCreditNoteRepository) Save(_ context.Context, cn *invoice.CreditNote) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.creditNotes[cn.ID()] = cn
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
