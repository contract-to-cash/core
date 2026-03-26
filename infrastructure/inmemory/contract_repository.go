package inmemory

import (
	"context"
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
func (r *InMemoryContractRepository) Save(ctx context.Context, aggregate *contract.ContractAggregate) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	events := aggregate.UncommittedEvents()
	if len(events) > 0 {
		expectedVersion := aggregate.Version()
		if err := r.store.Append(ctx, aggregate.ID(), events, expectedVersion); err != nil {
			return err
		}
		// Update committed version to match the last persisted event version
		aggregate.SetVersion(aggregate.Version() + len(events))
		aggregate.ClearUncommittedEvents()
	}

	r.contracts[aggregate.ContractID()] = aggregate
	return nil
}

// FindByID loads a contract aggregate by its ID.
func (r *InMemoryContractRepository) FindByID(_ context.Context, id shared.ContractID) (*contract.ContractAggregate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agg, ok := r.contracts[id]
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("contract %s not found", id))
	}
	return agg, nil
}

// FindByAccountID returns all contracts for an account.
func (r *InMemoryContractRepository) FindByAccountID(_ context.Context, accountID shared.AccountID) ([]*contract.ContractAggregate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*contract.ContractAggregate
	for _, agg := range r.contracts {
		if agg.AccountID() == accountID {
			result = append(result, agg)
		}
	}
	return result, nil
}

// FindActiveByPlanID returns active contracts using the specified plan.
func (r *InMemoryContractRepository) FindActiveByPlanID(_ context.Context, planID shared.PlanID) ([]*contract.ContractAggregate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*contract.ContractAggregate
	for _, agg := range r.contracts {
		if agg.PlanID() == planID && agg.Status() == contract.ContractStatusActive {
			result = append(result, agg)
		}
	}
	return result, nil
}

// FindExpiring returns contracts expiring before the given time.
func (r *InMemoryContractRepository) FindExpiring(_ context.Context, before time.Time) ([]*contract.ContractAggregate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*contract.ContractAggregate
	for _, agg := range r.contracts {
		if agg.Status() == contract.ContractStatusActive {
			period := agg.CurrentPeriod()
			if !period.End().IsZero() && period.End().Before(before) {
				result = append(result, agg)
			}
		}
	}
	return result, nil
}

// FindTrialsEndingSoon returns trialing contracts whose trial ends before the given time.
func (r *InMemoryContractRepository) FindTrialsEndingSoon(_ context.Context, before time.Time) ([]*contract.ContractAggregate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*contract.ContractAggregate
	for _, agg := range r.contracts {
		if agg.Status() == contract.ContractStatusTrialing && agg.TrialConfig() != nil {
			if agg.TrialConfig().TrialEndDate.Before(before) {
				result = append(result, agg)
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
