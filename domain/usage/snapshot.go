// Package usage — snapshot.go
//
// Snapshot / Reconstruct pattern for UsageRecord.
//
// # Relation to ContractAggregate
//
// ContractAggregate (in domain/contract) is event-sourced and uses
// MarshalSnapshot / LoadFromSnapshot. UsageRecord is state-based and uses
// ToSnapshot / FromSnapshot returning a typed struct. Do not mix.
//
// # DANGER ZONE — PERSISTENCE ADAPTERS ONLY
//
// The types and functions in this file deliberately bypass NewUsageRecord's
// quantity validation. Application code MUST NOT use these APIs.
//
// This scope is enforced in CI: the forbidigo rule in .golangci.yml blocks
// calls to ToSnapshot / FromSnapshot from any path outside
// domain/*/snapshot*.go, infrastructure/, and tests/. See issue #100.
//
// See issue #94 for the design rationale.

package usage

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// UsageRecordSnapshot is the flat persistence representation of a UsageRecord.
type UsageRecordSnapshot struct {
	ID             shared.UsageRecordID
	ContractID     shared.ContractID
	MetricName     shared.MetricName
	Quantity       int64
	Timestamp      time.Time
	Metadata       map[string]string
	IdempotencyKey string
}

// ToSnapshot returns a flat, independent copy of the record's internal state.
//
// For persistence adapters only.
func (r *UsageRecord) ToSnapshot() UsageRecordSnapshot {
	metadata := make(map[string]string, len(r.metadata))
	for k, v := range r.metadata {
		metadata[k] = v
	}
	return UsageRecordSnapshot{
		ID:             r.id,
		ContractID:     r.contractID,
		MetricName:     r.metricName,
		Quantity:       r.quantity,
		Timestamp:      r.timestamp,
		Metadata:       metadata,
		IdempotencyKey: r.idempotencyKey,
	}
}

// FromSnapshot reconstructs a UsageRecord from a persistence snapshot.
//
// Does NOT re-run the quantity validation from NewUsageRecord — historical
// rows may contain values that current validation would reject.
//
// For persistence adapters only.
func FromSnapshot(s UsageRecordSnapshot) (*UsageRecord, error) {
	if s.ID == "" {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"usage record snapshot: ID must not be empty")
	}

	metadata := make(map[string]string, len(s.Metadata))
	for k, v := range s.Metadata {
		metadata[k] = v
	}

	return &UsageRecord{
		id:             s.ID,
		contractID:     s.ContractID,
		metricName:     s.MetricName,
		quantity:       s.Quantity,
		timestamp:      s.Timestamp,
		metadata:       metadata,
		idempotencyKey: s.IdempotencyKey,
	}, nil
}
