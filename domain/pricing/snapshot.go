// Package pricing — snapshot.go
//
// Snapshot / Reconstruct pattern for Price.
//
// # Relation to ContractAggregate
//
// ContractAggregate (in domain/contract) is event-sourced and uses
// MarshalSnapshot / LoadFromSnapshot. Price is state-based and uses
// ToSnapshot / FromSnapshot returning a typed struct. Do not mix.
//
// # DANGER ZONE — PERSISTENCE ADAPTERS ONLY
//
// NewPrice generates a fresh ULID for the ID, making it impossible to
// reconstruct an existing price through the normal constructor path.
// Additionally, NewPrice always starts a Price in PriceStatusActive, so
// archived prices cannot be restored without going through Archive() —
// which FromSnapshot bypasses to avoid re-running business rules.
//
// Application code MUST NOT use these APIs. Use NewPrice / NewPriceWithInterval
// and the Archive() method instead.
//
// See issue #94 for the design rationale.

package pricing

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// PriceSnapshot is the flat persistence representation of a Price.
//
// Note that PricingModel is an interface. Adapters must handle its
// serialization explicitly (e.g. discriminated union in JSONB).
type PriceSnapshot struct {
	ID           shared.PriceID
	ProductID    shared.ProductID
	Amount       shared.Money
	Currency     shared.Currency
	Interval     BillingInterval
	PricingModel PricingModel
	Status       PriceStatus
	CreatedAt    time.Time
}

// ToSnapshot returns a flat, independent copy of the price's internal state.
//
// For persistence adapters only.
func (p *Price) ToSnapshot() PriceSnapshot {
	return PriceSnapshot{
		ID:           p.id,
		ProductID:    p.productID,
		Amount:       p.amount,
		Currency:     p.currency,
		Interval:     p.interval,
		PricingModel: p.pricingModel,
		Status:       p.status,
		CreatedAt:    p.createdAt,
	}
}

// FromSnapshot reconstructs a Price from a persistence snapshot.
//
// Preserves the caller-supplied ID and status exactly.
//
// For persistence adapters only.
func FromSnapshot(s PriceSnapshot) (*Price, error) {
	if s.ID == "" {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"price snapshot: ID must not be empty")
	}
	return &Price{
		id:           s.ID,
		productID:    s.ProductID,
		amount:       s.Amount,
		currency:     s.Currency,
		interval:     s.Interval,
		pricingModel: s.PricingModel,
		status:       s.Status,
		createdAt:    s.CreatedAt,
	}, nil
}
