package contract

import (
	"context"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Repository defines the persistence interface for contract aggregates.
type Repository interface {
	// Save persists a contract aggregate.
	//
	// Creation-idempotency contract (issue #159, mirroring the invoice
	// Repository.Save uniqueness pattern from #149): ContractAggregate.Create
	// requires a non-empty CreateContractCommand.IdempotencyKey and carries it
	// on ContractCreatedEvent, but the core can only validate PRESENCE — it
	// has no cross-aggregate view, so two retried Create calls with the same
	// key still produce two distinct aggregates unless the storage backend
	// closes the window. Implementations SHOULD enforce at-most-once creation
	// with a unique index over the created event's idempotency key, e.g.
	//
	//   CREATE UNIQUE INDEX ux_contract_idempotency_key
	//     ON contract_events ((data->>'idempotency_key'))
	//     WHERE type = 'contract.created'
	//       AND coalesce(data->>'idempotency_key', '') <> '';
	//
	// (empty keys — historical pre-#159 events — are exempt). When the
	// constraint fires, Save SHOULD return a shared.DomainError with code
	// shared.ErrCodeConflict so the retried caller can look up the existing
	// contract instead of failing opaquely. Key TTL/expiry, if desired, is
	// likewise the adapter's concern (design-decisions.md section 4.1).
	Save(ctx context.Context, aggregate *ContractAggregate) error

	// FindByID loads a contract aggregate by its ID.
	//
	// Not-found convention (issue #197): implementations MUST return an error for
	// a missing contract — a shared.DomainError with code shared.ErrCodeNotFound —
	// and MUST NOT return (nil, nil). Callers defend against a nil result
	// regardless (BYO-DB defensiveness), but the typed error is the contract. The
	// infrastructure/inmemory implementation is the reference.
	FindByID(ctx context.Context, id shared.ContractID) (*ContractAggregate, error)

	// FindByAccountID returns all contracts for an account.
	FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*ContractAggregate, error)

	// FindExpiring returns contracts expiring before the given time.
	FindExpiring(ctx context.Context, before time.Time) ([]*ContractAggregate, error)

	// FindTrialsEndingBefore returns trialing contracts whose TrialEndDate is
	// strictly before the given time. Called by the trial-expiration batch with
	// `now`, it yields trials that have ALREADY ended (renamed from the
	// misleading FindTrialsEndingSoon, which read as "ending in the near future"
	// — issue #162 B4).
	//
	// limit bounds the number of aggregates returned (issue #197): a positive
	// limit returns at most that many (the OLDEST-eligible first, so repeated
	// batch runs drain the backlog deterministically without starvation); a limit
	// of 0 (or negative) means "no limit" and preserves the original unbounded
	// behaviour. The batch layer threads BatchOptions.Limit here so a run against
	// a large due-set does not load every row into memory at once.
	FindTrialsEndingBefore(ctx context.Context, before time.Time, limit int) ([]*ContractAggregate, error)

	// FindByIDAsOf loads a contract aggregate as of a specific point in time.
	FindByIDAsOf(ctx context.Context, id shared.ContractID, asOf time.Time) (*ContractAggregate, error)

	// FindDueForRenewal returns active contracts whose current period ends on or
	// before asOf.
	//
	// limit bounds the number of aggregates returned (issue #197): a positive
	// limit returns at most that many (oldest-due first for deterministic
	// backlog draining); 0 (or negative) means "no limit" and preserves the
	// original unbounded behaviour. The renewal batch threads BatchOptions.Limit
	// here.
	FindDueForRenewal(ctx context.Context, asOf time.Time, limit int) ([]*ContractAggregate, error)
}
