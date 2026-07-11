package inmemory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

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

	// Store an ISOLATED copy (snapshot round-trip), not the caller's pointer, so
	// the caller's later mutations to entry cannot leak into the repository or
	// into concurrent readers (issue #152). FromSnapshot restores version and
	// loadedVersion together, matching the just-persisted baseline.
	stored, err := cloneBalanceEntry(entry)
	if err != nil {
		return err
	}
	r.entries[entry.ID()] = stored
	r.versions[entry.ID()] = entry.Version()
	// Update loadedVersion so subsequent saves from the same pointer don't conflict.
	entry.SetVersion(entry.Version())
	return nil
}

// cloneBalanceEntry returns an isolated deep copy of entry via the snapshot
// round-trip. FromSnapshot restores version and loadedVersion from the single
// stored version field, so the clone carries the same optimistic-locking
// baseline as the original.
func cloneBalanceEntry(entry *balance.BalanceEntry) (*balance.BalanceEntry, error) {
	return balance.FromSnapshot(entry.ToSnapshot())
}

// FindByID loads a balance entry by its ID.
//
// Returns an ISOLATED copy (snapshot round-trip) so concurrent load-modify
// callers never share a live pointer, and the optimistic lock in Save becomes
// observable through the raw repository (issue #152).
func (r *InMemoryBalanceRepository) FindByID(_ context.Context, id shared.BalanceEntryID) (*balance.BalanceEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.entries[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("balance entry %s not found", id))
	}
	return cloneBalanceEntry(entry)
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
		clone, err := cloneBalanceEntry(entry)
		if err != nil {
			return nil, err
		}
		result = append(result, clone)
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

// cloneApplication / cloneRefund return isolated copies of the audit records so
// the repository never hands out (or stores) a pointer a caller can mutate
// (issue #152 discipline / #197). BalanceApplication/BalanceRefund are flat value
// structs whose only reference-typed field is shared.Money (an immutable value
// object), so a shallow struct copy is a full isolation.
func cloneApplication(app *balance.BalanceApplication) *balance.BalanceApplication {
	cp := *app
	return &cp
}

func cloneRefund(ref *balance.BalanceRefund) *balance.BalanceRefund {
	cp := *ref
	return &cp
}

// SaveApplication persists a credit application idempotently (issue #197).
//
// A prior implementation blindly appended, so a transaction retry that re-ran
// applyBalances (e.g. after an optimistic-lock conflict) could record the SAME
// application ID twice, double-counting consumed credit in the audit trail. This
// upserts by BalanceApplication.ID: a repeat save of the same ID overwrites the
// existing record rather than appending a duplicate. An isolated copy is stored
// so a later mutation of the caller's struct cannot leak in.
func (r *InMemoryBalanceRepository) SaveApplication(_ context.Context, app *balance.BalanceApplication) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, existing := range r.applications {
		if existing.ID == app.ID {
			r.applications[i] = cloneApplication(app)
			return nil
		}
	}
	r.applications = append(r.applications, cloneApplication(app))
	return nil
}

// FindApplicationsByInvoice returns all credit applications for an invoice.
// Returns isolated copies so callers cannot mutate stored state (issue #197).
func (r *InMemoryBalanceRepository) FindApplicationsByInvoice(_ context.Context, invoiceID shared.InvoiceID) ([]*balance.BalanceApplication, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*balance.BalanceApplication
	for _, app := range r.applications {
		if app.InvoiceID == invoiceID {
			result = append(result, cloneApplication(app))
		}
	}
	return result, nil
}

// SaveRefund persists a credit refund idempotently (issue #197): a repeat save of
// the same BalanceRefund.ID overwrites rather than appends, mirroring
// SaveApplication. An isolated copy is stored.
func (r *InMemoryBalanceRepository) SaveRefund(_ context.Context, refund *balance.BalanceRefund) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, existing := range r.refunds {
		if existing.ID == refund.ID {
			r.refunds[i] = cloneRefund(refund)
			return nil
		}
	}
	r.refunds = append(r.refunds, cloneRefund(refund))
	return nil
}

// FindRefundsByInvoice returns all credit refunds recorded against an invoice
// (issue #184). Used by the void-restoration flow to skip already-restored
// applications, keeping a double void / retry idempotent. Returns isolated
// copies (issue #197).
func (r *InMemoryBalanceRepository) FindRefundsByInvoice(_ context.Context, invoiceID shared.InvoiceID) ([]*balance.BalanceRefund, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*balance.BalanceRefund
	for _, ref := range r.refunds {
		if ref.InvoiceID == invoiceID {
			result = append(result, cloneRefund(ref))
		}
	}
	return result, nil
}

// FindExpired returns entries whose expiry has passed as of asOf and whose
// remaining amount is still non-zero (i.e. expired credit not yet forfeited by
// MarkExpired), ordered by creation time ascending. Feeds
// batch.BalanceExpirationProcessor (issue #159).
//
// When limit > 0, at most limit entries are returned (the oldest by creation
// time, since the sort precedes truncation), so repeated batch runs drain the
// expired backlog deterministically (issue #197); limit <= 0 means unbounded.
func (r *InMemoryBalanceRepository) FindExpired(_ context.Context, asOf time.Time, limit int) ([]*balance.BalanceEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Collect matching entries first, sort by creation time, THEN clone/truncate
	// so the limit selects the oldest expired entries deterministically.
	var matched []*balance.BalanceEntry
	for _, entry := range r.entries {
		if !entry.IsExpired(asOf) {
			continue
		}
		if entry.IsFullyConsumed() {
			continue
		}
		matched = append(matched, entry)
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].CreatedAt().Before(matched[j].CreatedAt())
	})
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}

	result := make([]*balance.BalanceEntry, 0, len(matched))
	for _, entry := range matched {
		clone, err := cloneBalanceEntry(entry)
		if err != nil {
			return nil, err
		}
		result = append(result, clone)
	}

	return result, nil
}

// FindByAccountID returns all balance entries for an account and currency,
// including fully consumed and expired entries, ordered by creation time ascending.
func (r *InMemoryBalanceRepository) FindByAccountID(_ context.Context, accountID shared.AccountID, currency shared.Currency) ([]*balance.BalanceEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*balance.BalanceEntry
	for _, entry := range r.entries {
		if entry.AccountID() != accountID {
			continue
		}
		if entry.OriginalAmount().Currency() != currency {
			continue
		}
		clone, err := cloneBalanceEntry(entry)
		if err != nil {
			return nil, err
		}
		result = append(result, clone)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt().Before(result[j].CreatedAt())
	})

	return result, nil
}
