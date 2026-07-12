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
// This scope is enforced in CI: the forbidigo rule in .golangci.yml blocks
// calls to ToSnapshot / FromSnapshot from any path outside
// domain/*/snapshot*.go, infrastructure/, and tests/integration/.
// See issue #100.
//
// See issue #94 for the design rationale.

package pricing

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// PriceSnapshot is the flat persistence representation of a Price.
//
// # PricingModel: interface, not deep-copied
//
// PricingModel is an interface (FlatPrice, TieredPrice, UsagePrice), so
// ToSnapshot / FromSnapshot cannot deep-copy it without a type switch on
// every implementation. Today this is safe because the three built-in
// implementations are effectively immutable value types, BUT:
//
//   - TieredPrice contains a Tiers []PriceTier slice. If a caller mutates
//     that slice (e.g. append) via the snapshot, the entity is affected.
//   - Any future PricingModel implementation with mutable internal state
//     would inherit the same risk.
//
// Adapters must handle PricingModel serialization explicitly (e.g. a
// discriminated union in JSONB). When doing so, prefer constructing a
// fresh PricingModel from the stored columns rather than retaining and
// reusing the pointer from the snapshot.
type PriceSnapshot struct {
	ID           shared.PriceID
	ProductID    shared.ProductID
	Amount       shared.Money
	Currency     shared.Currency
	Interval     BillingInterval
	PricingModel PricingModel
	Status       PriceStatus
	// Metadata carries integrator-defined key-value pairs (issue #219). It is
	// copied on both ToSnapshot and FromSnapshot so snapshot and entity never
	// alias the same map. Nil in snapshots persisted before #219 — FromSnapshot
	// tolerates that (Metadata() then returns an empty map).
	Metadata  map[string]string
	CreatedAt time.Time
}

// copyMetadata returns an independent copy of m (nil in, nil out).
func copyMetadata(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
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
		Metadata:     copyMetadata(p.metadata),
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
		metadata:     copyMetadata(s.Metadata),
		createdAt:    s.CreatedAt,
	}, nil
}
