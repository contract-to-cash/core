package contract

import (
	"encoding/json"
	"fmt"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/eventstore"
)

// PriceChangedEventUpcaster converts v1 PriceChangedEvent (Money-based)
// to v2 format (PriceID-based). Historical events stored with SchemaVersion=1
// will have OldPrice/NewPrice Money fields but no PriceID fields.
// This upcaster sets SchemaVersion=2 so downstream consumers know the
// event has been migrated.
type PriceChangedEventUpcaster struct{}

// CanUpcast returns true for PriceChangedEvent at schema version <= 1.
// RaiseEvent always sets SchemaVersion=1, so v0 events never exist in practice.
// For v2 (new format) events that still have SchemaVersion=1, the Upcast method
// is idempotent: it only adds fields that are absent and preserves existing values.
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

// intervalFieldSet reports whether a JSON interval field is present and carries
// a real value (i.e. it exists and is not JSON null). BillingInterval marshals
// its zero value to null, so both an absent key and an explicit null mean the
// interval was never recorded and must be recovered from a legacy billing_cycle.
func intervalFieldSet(raw map[string]json.RawMessage, key string) bool {
	v, ok := raw[key]
	if !ok {
		return false
	}
	return string(v) != "null"
}

// legacyBillingCycle extracts a string billing_cycle value from a raw JSON field.
// Returns ("", false) if the key is absent or holds an empty string.
func legacyBillingCycle(raw map[string]json.RawMessage, key string) (pricing.BillingCycle, bool) {
	v, ok := raw[key]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil || s == "" {
		return "", false
	}
	return pricing.BillingCycle(s), true
}

// marshalInterval converts a BillingCycle to the canonical interval JSON
// ({"unit":...,"count":...}).
func marshalInterval(cycle pricing.BillingCycle) (json.RawMessage, error) {
	return json.Marshal(pricing.BillingCycleToInterval(cycle))
}

// ContractCreatedEventUpcaster migrates historical ContractCreatedEvent payloads
// that carried only the deprecated billing_cycle string (no interval) into the
// interval-based schema. It is idempotent: events that already have an interval
// are left untouched. SchemaVersion is bumped to 2.
type ContractCreatedEventUpcaster struct{}

// CanUpcast returns true for ContractCreatedEvent at schema version <= 1.
func (u *ContractCreatedEventUpcaster) CanUpcast(eventType eventstore.EventType, fromVersion int) bool {
	return eventType == EventTypeContractCreated && fromVersion <= 1
}

// Upcast fills interval from a legacy billing_cycle when interval is absent.
func (u *ContractCreatedEventUpcaster) Upcast(event eventstore.Event) (eventstore.Event, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &raw); err != nil {
		return event, fmt.Errorf("upcaster: failed to unmarshal ContractCreatedEvent: %w", err)
	}

	if !intervalFieldSet(raw, "interval") {
		if cycle, ok := legacyBillingCycle(raw, "billing_cycle"); ok {
			intervalJSON, err := marshalInterval(cycle)
			if err != nil {
				return event, fmt.Errorf("upcaster: failed to marshal interval: %w", err)
			}
			raw["interval"] = intervalJSON
		}
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return event, fmt.Errorf("upcaster: failed to marshal ContractCreatedEvent: %w", err)
	}
	event.Data = data
	event.SchemaVersion = 2
	return event, nil
}

// ContractRenewedEventUpcaster migrates historical ContractRenewedEvent payloads
// that carried only the deprecated old_billing_cycle/new_billing_cycle strings
// into the interval-based schema (old_interval/new_interval). It is idempotent
// and bumps SchemaVersion to 2.
type ContractRenewedEventUpcaster struct{}

// CanUpcast returns true for ContractRenewedEvent at schema version <= 1.
func (u *ContractRenewedEventUpcaster) CanUpcast(eventType eventstore.EventType, fromVersion int) bool {
	return eventType == EventTypeContractRenewed && fromVersion <= 1
}

// Upcast fills old_interval/new_interval from legacy billing-cycle fields when
// the interval fields are absent.
func (u *ContractRenewedEventUpcaster) Upcast(event eventstore.Event) (eventstore.Event, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &raw); err != nil {
		return event, fmt.Errorf("upcaster: failed to unmarshal ContractRenewedEvent: %w", err)
	}

	pairs := []struct{ intervalKey, cycleKey string }{
		{"old_interval", "old_billing_cycle"},
		{"new_interval", "new_billing_cycle"},
	}
	for _, p := range pairs {
		if intervalFieldSet(raw, p.intervalKey) {
			continue
		}
		cycle, ok := legacyBillingCycle(raw, p.cycleKey)
		if !ok {
			continue
		}
		intervalJSON, err := marshalInterval(cycle)
		if err != nil {
			return event, fmt.Errorf("upcaster: failed to marshal %s: %w", p.intervalKey, err)
		}
		raw[p.intervalKey] = intervalJSON
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return event, fmt.Errorf("upcaster: failed to marshal ContractRenewedEvent: %w", err)
	}
	event.Data = data
	event.SchemaVersion = 2
	return event, nil
}

// TrialEndedEventUpcaster migrates historical TrialEndedEvent payloads (schema
// version 1) to version 2. Version 2 added the current_period field so that a
// converted trial establishes an initial billing period (issue #146).
//
// The period itself cannot be filled at the JSON level: the billing interval
// needed to compute it lives on the earlier ContractCreatedEvent, not on this
// event. So this upcaster only bumps SchemaVersion to 2 (idempotently); the
// aggregate's Apply derives the period for legacy converted events from the
// replayed interval anchored at EndedAt. A non-converted legacy event needs no
// period. This keeps replay deterministic without corrupting the append-only
// history.
type TrialEndedEventUpcaster struct{}

// CanUpcast returns true for TrialEndedEvent at schema version <= 1.
func (u *TrialEndedEventUpcaster) CanUpcast(eventType eventstore.EventType, fromVersion int) bool {
	return eventType == EventTypeTrialEnded && fromVersion <= 1
}

// Upcast bumps a TrialEndedEvent to schema version 2. It leaves the payload
// otherwise untouched: a missing current_period deserializes to the zero
// DateRange, which Apply recognizes as "legacy" and fills deterministically.
func (u *TrialEndedEventUpcaster) Upcast(event eventstore.Event) (eventstore.Event, error) {
	event.SchemaVersion = 2
	return event, nil
}

// NewContractUpcasterChain returns an UpcasterChain with all contract upcasters.
func NewContractUpcasterChain() *eventstore.UpcasterChain {
	return eventstore.NewUpcasterChain(
		&PriceChangedEventUpcaster{},
		&ContractCreatedEventUpcaster{},
		&ContractRenewedEventUpcaster{},
		&TrialEndedEventUpcaster{},
	)
}
