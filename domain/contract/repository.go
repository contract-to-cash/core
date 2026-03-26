package contract

import (
	"context"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Repository defines the persistence interface for contract aggregates.
type Repository interface {
	// Save persists a contract aggregate.
	Save(ctx context.Context, aggregate *ContractAggregate) error

	// FindByID loads a contract aggregate by its ID.
	FindByID(ctx context.Context, id shared.ContractID) (*ContractAggregate, error)

	// FindByAccountID returns all contracts for an account.
	FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*ContractAggregate, error)

	// FindActiveByPlanID returns active contracts using the specified plan.
	FindActiveByPlanID(ctx context.Context, planID shared.PlanID) ([]*ContractAggregate, error)

	// FindExpiring returns contracts expiring before the given time.
	FindExpiring(ctx context.Context, before time.Time) ([]*ContractAggregate, error)

	// FindTrialsEndingSoon returns trialing contracts whose trial ends before the given time.
	FindTrialsEndingSoon(ctx context.Context, before time.Time) ([]*ContractAggregate, error)

	// FindByIDAsOf loads a contract aggregate as of a specific point in time.
	FindByIDAsOf(ctx context.Context, id shared.ContractID, asOf time.Time) (*ContractAggregate, error)
}
