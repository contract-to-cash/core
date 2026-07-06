package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/shared"
)

// Compile-time interface check.
var _ contract.Repository = (*InMemoryContractRepository)(nil)

// InMemoryContractRepository is an in-memory implementation of contract.Repository.
type InMemoryContractRepository struct {
	mu        sync.RWMutex
	store     *InMemoryEventStore
	contracts map[shared.ContractID]*contract.ContractAggregate
	clock     shared.Clock
}

// NewInMemoryContractRepository creates a new InMemoryContractRepository.
func NewInMemoryContractRepository(store *InMemoryEventStore, clock shared.Clock) *InMemoryContractRepository {
	return &InMemoryContractRepository{
		store:     store,
		contracts: make(map[shared.ContractID]*contract.ContractAggregate),
		clock:     clock,
	}
}

// Save persists a contract aggregate by storing its uncommitted events.
//
// Isolation (issue #152): the aggregate kept in the in-memory index is NOT the
// caller's pointer but a fresh aggregate re-materialized from the event store.
// This mirrors how a real event-sourced adapter works (the durable events are
// the source of truth) and prevents the caller's later mutations to `aggregate`
// from leaking into the repository or into concurrent readers — which would
// otherwise be a data race on the shared aggregate's internal state.
func (r *InMemoryContractRepository) Save(ctx context.Context, aggregate *contract.ContractAggregate) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	events := aggregate.UncommittedEvents()
	if len(events) > 0 {
		expectedVersion := aggregate.Version()
		// A version mismatch surfaces the event store's version_conflict
		// DomainError, which tx.RetryOnConflict recognises (tx.IsVersionConflict).
		if err := r.store.Append(ctx, aggregate.ID(), events, expectedVersion); err != nil {
			return err
		}
		// Update committed version to match the last persisted event version
		aggregate.SetVersion(aggregate.Version() + len(events))
		aggregate.ClearUncommittedEvents()
	}

	// Store an isolated copy re-materialized from the event store so list
	// finders read a snapshot decoupled from the caller's aggregate. If the
	// stream has no events yet (an aggregate saved before raising any event),
	// fall back to indexing the caller's pointer — there is no persisted state
	// to rebuild from.
	stored, err := r.rehydrateLocked(ctx, aggregate.ContractID())
	if err != nil {
		var de *shared.DomainError
		if errors.As(err, &de) && de.Code == shared.ErrCodeNotFound {
			r.contracts[aggregate.ContractID()] = aggregate
			return nil
		}
		return err
	}
	r.contracts[aggregate.ContractID()] = stored
	return nil
}

// rehydrateLocked rebuilds an isolated aggregate from the event store by
// replaying its stream, the way a real event-sourced adapter materializes an
// aggregate on load. The returned aggregate shares no mutable state with the
// stored copy or with any other caller. It does not require r.mu (the event
// store guards its own state); callers hold r.mu only to serialize with Save.
func (r *InMemoryContractRepository) rehydrateLocked(ctx context.Context, id shared.ContractID) (*contract.ContractAggregate, error) {
	events, err := r.store.Load(ctx, string(id))
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("contract %s not found", id))
	}
	agg := contract.NewContractAggregate(id, r.clock)
	if err := agg.LoadFromHistory(events); err != nil {
		return nil, err
	}
	return agg, nil
}

// FindByID loads a contract aggregate by its ID.
//
// Returns a fresh aggregate re-materialized from the event store so that
// concurrent load-modify callers never share a live pointer (issue #152). This
// makes the event store's optimistic-lock contract observable: two independent
// loads each carry their own version, so the loser of two concurrent Saves is
// rejected with a version_conflict instead of both silently succeeding.
func (r *InMemoryContractRepository) FindByID(ctx context.Context, id shared.ContractID) (*contract.ContractAggregate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if _, ok := r.contracts[id]; !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("contract %s not found", id))
	}
	return r.rehydrateLocked(ctx, id)
}

// FindByAccountID returns all contracts for an account.
func (r *InMemoryContractRepository) FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*contract.ContractAggregate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*contract.ContractAggregate
	for id, agg := range r.contracts {
		if agg.AccountID() == accountID {
			clone, err := r.rehydrateLocked(ctx, id)
			if err != nil {
				return nil, err
			}
			result = append(result, clone)
		}
	}
	return result, nil
}

// FindExpiring returns contracts expiring before the given time.
func (r *InMemoryContractRepository) FindExpiring(ctx context.Context, before time.Time) ([]*contract.ContractAggregate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*contract.ContractAggregate
	for id, agg := range r.contracts {
		if agg.Status() == contract.ContractStatusActive {
			period := agg.CurrentPeriod()
			if !period.End().IsZero() && period.End().Before(before) {
				clone, err := r.rehydrateLocked(ctx, id)
				if err != nil {
					return nil, err
				}
				result = append(result, clone)
			}
		}
	}
	return result, nil
}

// FindTrialsEndingSoon returns trialing contracts whose trial ends before the given time.
func (r *InMemoryContractRepository) FindTrialsEndingSoon(ctx context.Context, before time.Time) ([]*contract.ContractAggregate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*contract.ContractAggregate
	for id, agg := range r.contracts {
		if agg.Status() == contract.ContractStatusTrialing && agg.TrialConfig() != nil {
			if agg.TrialConfig().TrialEndDate.Before(before) {
				clone, err := r.rehydrateLocked(ctx, id)
				if err != nil {
					return nil, err
				}
				result = append(result, clone)
			}
		}
	}
	return result, nil
}

// FindDueForRenewal returns active contracts whose current period ends on or before asOf.
func (r *InMemoryContractRepository) FindDueForRenewal(ctx context.Context, asOf time.Time) ([]*contract.ContractAggregate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*contract.ContractAggregate
	for id, agg := range r.contracts {
		if agg.Status() == contract.ContractStatusActive {
			period := agg.CurrentPeriod()
			if !period.End().IsZero() && !period.End().After(asOf) {
				clone, err := r.rehydrateLocked(ctx, id)
				if err != nil {
					return nil, err
				}
				result = append(result, clone)
			}
		}
	}
	return result, nil
}

// FindByIDAsOf loads a contract aggregate as of a specific point in time.
func (r *InMemoryContractRepository) FindByIDAsOf(ctx context.Context, id shared.ContractID, asOf time.Time) (*contract.ContractAggregate, error) {
	events, err := r.store.LoadUntil(ctx, string(id), asOf)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("contract %s not found as of %s", id, asOf))
	}

	agg := contract.NewContractAggregate(id, r.clock)
	if err := agg.LoadFromHistory(events); err != nil {
		return nil, err
	}
	return agg, nil
}
