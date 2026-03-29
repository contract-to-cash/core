package inmemory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/shared"
)

// Compile-time interface check.
var _ balance.Repository = (*InMemoryBalanceRepository)(nil)

// InMemoryBalanceRepository is an in-memory implementation of balance.Repository.
type InMemoryBalanceRepository struct {
	mu           sync.RWMutex
	entries      map[shared.BalanceEntryID]*balance.BalanceEntry
	versions     map[shared.BalanceEntryID]int // tracks persisted version per entry
	applications []*balance.BalanceApplication
	refunds      []*balance.BalanceRefund
	clock        shared.Clock
}

// NewInMemoryBalanceRepository creates a new InMemoryBalanceRepository.
func NewInMemoryBalanceRepository(clock shared.Clock) *InMemoryBalanceRepository {
	return &InMemoryBalanceRepository{
		entries:  make(map[shared.BalanceEntryID]*balance.BalanceEntry),
		versions: make(map[shared.BalanceEntryID]int),
		clock:    clock,
	}
}

// Save persists a balance entry with optimistic locking.
// Compares the entry's loaded version against the stored version. If they differ,
// another caller has modified the entry since this caller loaded it.
func (r *InMemoryBalanceRepository) Save(_ context.Context, entry *balance.BalanceEntry) error {
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

// FindByID loads a balance entry by its ID.
func (r *InMemoryBalanceRepository) FindByID(_ context.Context, id shared.BalanceEntryID) (*balance.BalanceEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.entries[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("balance entry %s not found", id))
	}
	return entry, nil
}

// FindAvailable returns available credit entries for an account and currency.
// Returns entries in FIFO order (createdAt ascending), excluding expired and fully consumed entries.
func (r *InMemoryBalanceRepository) FindAvailable(_ context.Context, accountID shared.AccountID, currency shared.Currency) ([]*balance.BalanceEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := r.clock.Now()
	var result []*balance.BalanceEntry
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
func (r *InMemoryBalanceRepository) GetBalance(_ context.Context, accountID shared.AccountID, currency shared.Currency) (shared.Money, error) {
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
func (r *InMemoryBalanceRepository) SaveApplication(_ context.Context, app *balance.BalanceApplication) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.applications = append(r.applications, app)
	return nil
}

// FindApplicationsByInvoice returns all credit applications for an invoice.
func (r *InMemoryBalanceRepository) FindApplicationsByInvoice(_ context.Context, invoiceID shared.InvoiceID) ([]*balance.BalanceApplication, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*balance.BalanceApplication
	for _, app := range r.applications {
		if app.InvoiceID == invoiceID {
			result = append(result, app)
		}
	}
	return result, nil
}

// SaveRefund persists a credit refund.
func (r *InMemoryBalanceRepository) SaveRefund(_ context.Context, refund *balance.BalanceRefund) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.refunds = append(r.refunds, refund)
	return nil
}
