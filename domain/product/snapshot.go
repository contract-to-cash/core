// Package product — snapshot.go
//
// Snapshot / Reconstruct pattern for Product.
//
// DANGER ZONE — PERSISTENCE ADAPTERS ONLY
//
// NewProduct generates a fresh ULID for the ID, which makes it impossible
// to reconstruct an existing product through the normal constructor path.
// FromSnapshot solves this by honoring the caller-supplied ID.
//
// Application code MUST NOT use these APIs. Use NewProduct and the
// state-transition methods (AddFeature, Archive, ...) instead.
//
// See issue #94 for the design rationale.

package product

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// ProductSnapshot is the flat persistence representation of a Product.
//
// Feature and UsageMetric are already exported value types in the product
// package, so we embed them directly rather than defining parallel *Snapshot
// structs.
type ProductSnapshot struct {
	ID           shared.ProductID
	Name         string
	Description  string
	Features     []Feature
	UsageMetrics []UsageMetric
	Status       ProductStatus
	Metadata     map[string]string
	CreatedAt    time.Time
}

// ToSnapshot returns a flat, independent copy of the product's internal state.
//
// For persistence adapters only.
func (p *Product) ToSnapshot() ProductSnapshot {
	features := make([]Feature, len(p.features))
	copy(features, p.features)

	usageMetrics := make([]UsageMetric, len(p.usageMetrics))
	copy(usageMetrics, p.usageMetrics)

	metadata := make(map[string]string, len(p.metadata))
	for k, v := range p.metadata {
		metadata[k] = v
	}

	return ProductSnapshot{
		ID:           p.id,
		Name:         p.name,
		Description:  p.description,
		Features:     features,
		UsageMetrics: usageMetrics,
		Status:       p.status,
		Metadata:     metadata,
		CreatedAt:    p.createdAt,
	}
}

// FromSnapshot reconstructs a Product from a persistence snapshot.
//
// Preserves the caller-supplied ID exactly (unlike NewProduct which generates
// a fresh ULID).
//
// For persistence adapters only.
func FromSnapshot(s ProductSnapshot) (*Product, error) {
	if s.ID == "" {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"product snapshot: ID must not be empty")
	}

	features := make([]Feature, len(s.Features))
	copy(features, s.Features)

	usageMetrics := make([]UsageMetric, len(s.UsageMetrics))
	copy(usageMetrics, s.UsageMetrics)

	metadata := make(map[string]string, len(s.Metadata))
	for k, v := range s.Metadata {
		metadata[k] = v
	}

	return &Product{
		id:           s.ID,
		name:         s.Name,
		description:  s.Description,
		features:     features,
		usageMetrics: usageMetrics,
		status:       s.Status,
		metadata:     metadata,
		createdAt:    s.CreatedAt,
	}, nil
}
