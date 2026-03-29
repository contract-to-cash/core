package inmemory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/credit"
	"github.com/contract-to-cash/core/domain/shared"
)

// Compile-time interface check.
var _ credit.Repository = (*InMemoryCreditRepository)(nil)

// InMemoryCreditRepository is an in-memory implementation of credit.Repository.
type InMemoryCreditRepository struct {
	mu           sync.RWMutex
	entries      map[shared.CreditEntryID]*credit.CreditEntry
	versions     map[shared.CreditEntryID]int // tracks persisted version per entry
	applications []*credit.CreditApplication
	refunds      []*credit.CreditRefund
	clock        shared.Clock
}

// NewInMemoryCreditRepository creates a new InMemoryCreditRepository.
func NewInMemoryCreditRepository(clock shared.Clock) *InMemoryCreditRepository {
	return &InMemoryCreditRepository{
		entries:  make(map[shared.CreditEntryID]*credit.CreditEntry),
		versions: make(map[shared.CreditEntryID]int),
		clock:    clock,
	}
}

// Save persists a credit entry with optimistic locking.
// Compares the entry's loaded version against the stored version. If they differ,
// another caller has modified the entry since this caller loaded it.
func (r *InMemoryCreditRepository) Save(_ context.Context, entry *credit.CreditEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if storedVersion, ok := r.versions[entry.ID()]; ok {
		if entry.LoadedVersion() != storedVersion {
			return tx.ErrVersionConflict
		}
	}

	r.entries[entry.ID()] = entry
	r.versions[entry.ID()] = entry.Version()
	// Update loadedVersion so subsequent saves from the same pointer don't conflict.
	entry.SetVersion(entry.Version())
	return nil
}

// FindByID loads a credit entry by its ID.
func (r *InMemoryCreditRepository) FindByID(_ context.Context, id shared.CreditEntryID) (*credit.CreditEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.entries[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("credit entry %s not found", id))
	}
	return entry, nil
}

// FindAvailable returns available credit entries for an account and currency.
// Returns entries in FIFO order (createdAt ascending), excluding expired and fully consumed entries.
func (r *InMemoryCreditRepository) FindAvailable(_ context.Context, accountID shared.AccountID, currency shared.Currency) ([]*credit.CreditEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := r.clock.Now()
	var result []*credit.CreditEntry
	for _, entry := range r.entries {
		if entry.AccountID() != accountID {
			continue
		}
		if entry.OriginalAmount().Currency() != currency {
			continue
		}
		if entry.IsExpired(now) {
			continue
		}
		if entry.IsFullyConsumed() {
			continue
		}
		result = append(result, entry)
	}

	// Sort by createdAt ascending (FIFO).
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt().Before(result[j].CreatedAt())
	})

	return result, nil
}

// GetBalance returns the total remaining balance for an account and currency.
func (r *InMemoryCreditRepository) GetBalance(_ context.Context, accountID shared.AccountID, currency shared.Currency) (shared.Money, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := r.clock.Now()
	total := shared.Zero(currency)
	for _, entry := range r.entries {
		if entry.AccountID() != accountID {
			continue
		}
		if entry.OriginalAmount().Currency() != currency {
			continue
		}
		if entry.IsExpired(now) {
			continue
		}
		if entry.IsFullyConsumed() {
			continue
		}
		var err error
		total, err = total.Add(entry.RemainingAmount())
		if err != nil {
			return shared.Money{}, err
		}
	}
	return total, nil
}

// SaveApplication persists a credit application.
func (r *InMemoryCreditRepository) SaveApplication(_ context.Context, app *credit.CreditApplication) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.applications = append(r.applications, app)
	return nil
}

// FindApplicationsByInvoice returns all credit applications for an invoice.
func (r *InMemoryCreditRepository) FindApplicationsByInvoice(_ context.Context, invoiceID shared.InvoiceID) ([]*credit.CreditApplication, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*credit.CreditApplication
	for _, app := range r.applications {
		if app.InvoiceID == invoiceID {
			result = append(result, app)
		}
	}
	return result, nil
}

// SaveRefund persists a credit refund.
func (r *InMemoryCreditRepository) SaveRefund(_ context.Context, refund *credit.CreditRefund) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.refunds = append(r.refunds, refund)
	return nil
}
