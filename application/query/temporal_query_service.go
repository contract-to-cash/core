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
func (s *TemporalQueryService) GetContractAsOf(ctx context.Context, contractID shared.ContractID, asOf time.Time) (*contract.ContractAggregate, error) {
	streamID := string(contractID)

	// Try to load a snapshot before the requested time
	snapshot, err := s.eventStore.LoadSnapshotBefore(ctx, streamID, asOf)
	if err != nil {
		return nil, fmt.Errorf("failed to load snapshot: %w", err)
	}

	agg := contract.NewContractAggregate(contractID, s.clock)

	if snapshot != nil {
		if err = agg.LoadFromSnapshot(*snapshot); err != nil {
			return nil, fmt.Errorf("failed to load from snapshot: %w", err)
		}
	}

	// Load events up to the requested time
	events, err := s.eventStore.LoadUntil(ctx, streamID, asOf)
	if err != nil {
		return nil, fmt.Errorf("failed to load events: %w", err)
	}

	// If we restored from snapshot, only replay events after the snapshot version
	if snapshot != nil {
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
