// Package query provides temporal query services for event-sourced aggregates.
package query

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// ContractHistoryEntry represents a single event in a contract's history.
type ContractHistoryEntry struct {
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	UserID     string          `json:"user_id"`
	Data       json.RawMessage `json:"data"`
}

// TemporalQueryService provides point-in-time and history queries
// against event-sourced aggregates.
type TemporalQueryService struct {
	eventStore eventstore.Store
	clock      shared.Clock
}

// NewTemporalQueryService creates a new TemporalQueryService.
func NewTemporalQueryService(eventStore eventstore.Store, clock shared.Clock) *TemporalQueryService {
	return &TemporalQueryService{
		eventStore: eventStore,
		clock:      clock,
	}
}

// GetContractAsOf reconstructs a contract aggregate as it existed at a specific point in time.
//
// Nonexistent-contract convention (INTENTIONAL, issue #246): a contractID with
// no events yields a ZERO-VALUE aggregate with a nil error — Store.LoadUntil
// returns (empty, nil) for an unknown stream, so "the contract did not exist
// yet at asOf" and "the contract never existed" are indistinguishable here and
// both reconstruct to an empty aggregate (Version 0, zero-value status). This
// deliberately differs from the repositories' FindByIDAsOf, which return a
// shared.DomainError with code shared.ErrCodeNotFound for a missing aggregate.
// Callers that need existence semantics should check the reconstructed
// aggregate's Version()/status or use the repository instead.
//
// OccurredAt-monotonicity assumption (review W7): events are bounded by
// OccurredAt (LoadUntil) while replay applies them in Version order. The
// snapshot consistency guard below assumes OccurredAt is monotonically
// non-decreasing in Version WITHIN a stream. If a stream contains interleaved
// BACKDATED events (a later-Version event with an earlier OccurredAt), the
// asOf cut can select a version-GAPPED subsequence (e.g. versions 1,2,4
// without 3) and the reconstruction applies that gapped sequence as-is —
// producing a state that never actually existed at asOf. Core aggregates stamp
// OccurredAt from a monotonic clock at RaiseEvent time, so this holds for
// events produced by this library; integrators importing historical events
// with hand-set OccurredAt values must preserve per-stream monotonicity.
func (s *TemporalQueryService) GetContractAsOf(ctx context.Context, contractID shared.ContractID, asOf time.Time) (*contract.ContractAggregate, error) {
	streamID := string(contractID)

	// Load events that OCCURRED at or before the requested time (OccurredAt-bounded).
	events, err := s.eventStore.LoadUntil(ctx, streamID, asOf)
	if err != nil {
		return nil, fmt.Errorf("failed to load events: %w", err)
	}

	// The highest event version within the asOf horizon. A snapshot may only be
	// used as an optimization if it does not cover any event beyond this horizon.
	var maxVersionAsOf int
	for _, e := range events {
		if e.Version > maxVersionAsOf {
			maxVersionAsOf = e.Version
		}
	}

	// Try to load a snapshot before the requested time.
	snapshot, err := s.eventStore.LoadSnapshotBefore(ctx, streamID, asOf)
	if err != nil {
		return nil, fmt.Errorf("failed to load snapshot: %w", err)
	}

	// Consistency guard (review W7): snapshots are selected by CreatedAt
	// (wall-clock) while events are bounded by OccurredAt. Only use the snapshot
	// if it does not cover events beyond the asOf event horizon; otherwise it
	// would leak state from events that occurred after asOf (possible with
	// backdated/out-of-order events). In that case replay from scratch.
	useSnapshot := snapshot != nil && snapshot.Version <= maxVersionAsOf

	agg := contract.NewContractAggregate(contractID, s.clock)

	if useSnapshot {
		if err = agg.LoadFromSnapshot(*snapshot); err != nil {
			return nil, fmt.Errorf("failed to load from snapshot: %w", err)
		}
		// Only replay events after the snapshot version.
		var afterSnapshot []eventstore.Event
		for _, e := range events {
			if e.Version > snapshot.Version {
				afterSnapshot = append(afterSnapshot, e)
			}
		}
		events = afterSnapshot
	}

	if err := agg.LoadFromHistory(events); err != nil {
		return nil, fmt.Errorf("failed to replay events: %w", err)
	}

	return agg, nil
}

// GetContractHistory returns the full event history for a contract.
func (s *TemporalQueryService) GetContractHistory(ctx context.Context, contractID shared.ContractID) ([]ContractHistoryEntry, error) {
	streamID := string(contractID)

	events, err := s.eventStore.Load(ctx, streamID)
	if err != nil {
		return nil, fmt.Errorf("failed to load events: %w", err)
	}

	entries := make([]ContractHistoryEntry, 0, len(events))
	for _, e := range events {
		entries = append(entries, ContractHistoryEntry{
			EventType:  string(e.Type),
			OccurredAt: e.OccurredAt,
			UserID:     e.Metadata.UserID,
			Data:       e.Data,
		})
	}

	return entries, nil
}
