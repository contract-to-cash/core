package contract

import (
	"encoding/json"
	"fmt"

	"github.com/contract-to-cash/core/eventstore"
)

// PriceChangedEventUpcaster converts v1 PriceChangedEvent (Money-based)
// to v2 format (PriceID-based). Historical events stored with SchemaVersion=1
// will have OldPrice/NewPrice Money fields but no PriceID fields.
// This upcaster sets SchemaVersion=2 so downstream consumers know the
// event has been migrated.
type PriceChangedEventUpcaster struct{}

// CanUpcast returns true for PriceChangedEvent at schema version 1.
func (u *PriceChangedEventUpcaster) CanUpcast(eventType eventstore.EventType, fromVersion int) bool {
	return eventType == EventTypePriceChanged && fromVersion <= 1
}

// Upcast migrates v1 PriceChangedEvent to v2 by adding default Policy
// and empty PriceID fields if absent.
func (u *PriceChangedEventUpcaster) Upcast(event eventstore.Event) (eventstore.Event, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &raw); err != nil {
		return event, fmt.Errorf("upcaster: failed to unmarshal PriceChangedEvent: %w", err)
	}

	// Add policy="immediate" if absent or empty (v1 events were always immediate)
	var existingPolicy string
	if policyRaw, ok := raw["policy"]; ok {
		_ = json.Unmarshal(policyRaw, &existingPolicy)
	}
	if existingPolicy == "" {
		policyJSON, _ := json.Marshal(string(ChangePolicyImmediate))
		raw["policy"] = policyJSON
	}

	// Add empty PriceID fields if absent
	if _, ok := raw["old_price_id"]; !ok {
		raw["old_price_id"] = []byte(`""`)
	}
	if _, ok := raw["new_price_id"]; !ok {
		raw["new_price_id"] = []byte(`""`)
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return event, fmt.Errorf("upcaster: failed to marshal PriceChangedEvent: %w", err)
	}

	event.Data = data
	event.SchemaVersion = 2
	return event, nil
}

// NewContractUpcasterChain returns an UpcasterChain with all contract upcasters.
func NewContractUpcasterChain() *eventstore.UpcasterChain {
	return eventstore.NewUpcasterChain(
		&PriceChangedEventUpcaster{},
	)
}
