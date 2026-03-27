package inmemory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
)

// Compile-time interface check.
var _ invoice.Repository = (*InMemoryInvoiceRepository)(nil)

// InMemoryInvoiceRepository is an in-memory implementation of invoice.Repository.
type InMemoryInvoiceRepository struct {
	mu       sync.RWMutex
	invoices map[shared.InvoiceID]*invoice.Invoice
	clock    shared.Clock
}

// NewInMemoryInvoiceRepository creates a new InMemoryInvoiceRepository.
func NewInMemoryInvoiceRepository(clock shared.Clock) *InMemoryInvoiceRepository {
	return &InMemoryInvoiceRepository{
		invoices: make(map[shared.InvoiceID]*invoice.Invoice),
		clock:    clock,
	}
}

// Save persists an invoice.
func (r *InMemoryInvoiceRepository) Save(_ context.Context, inv *invoice.Invoice) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.invoices[inv.ID()] = inv
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
