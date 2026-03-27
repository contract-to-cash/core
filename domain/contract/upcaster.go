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

// priceChangedEventV1 is the legacy format with Money fields only.
type priceChangedEventV1 struct {
	ContractID  json.RawMessage `json:"contract_id"`
	OldPrice    json.RawMessage `json:"old_price"`
	NewPrice    json.RawMessage `json:"new_price"`
	ChangedAt   json.RawMessage `json:"changed_at"`
	EffectiveAt json.RawMessage `json:"effective_at"`
	// v2 fields may be absent in old events
	OldPriceID json.RawMessage `json:"old_price_id,omitempty"`
	NewPriceID json.RawMessage `json:"new_price_id,omitempty"`
	Policy     json.RawMessage `json:"policy,omitempty"`
}

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
	if policyRaw, ok := raw["policy"]; !ok || string(policyRaw) == `""` || string(policyRaw) == `null` {
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
