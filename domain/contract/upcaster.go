package contract

import (
	"encoding/json"
	"fmt"
	"time"

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
//
// It uses the STRICT conversion so an unrecognized legacy billing_cycle surfaces
// as an upcaster error instead of being silently rewritten as Monthly, which
// would corrupt the migrated interval (issue #162 L-4). Only the four historical
// cycle values (daily/weekly/monthly/yearly) were ever persisted, so a real
// stream never hits the error path; a garbage value means the payload is already
// corrupt and must fail loudly rather than replay as a wrong interval.
func marshalInterval(cycle pricing.BillingCycle) (json.RawMessage, error) {
	interval, ok := pricing.BillingCycleToIntervalStrict(cycle)
	if !ok {
		return nil, fmt.Errorf("upcaster: unknown legacy billing_cycle %q (expected daily, weekly, monthly, or yearly)", cycle)
	}
	return json.Marshal(interval)
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

// ContractCreatedIdempotencyKeyUpcaster migrates ContractCreatedEvent payloads
// from schema version 2 to version 3. Version 3 added the idempotency_key
// field (issue #159) so persistence adapters can enforce at-most-once contract
// creation.
//
// The key itself cannot be recovered for historical events — it was never
// recorded — so this upcaster only bumps SchemaVersion to 3 (idempotently,
// mirroring TrialEndedEventUpcaster): a missing idempotency_key deserializes
// to the empty string, which the aggregate's Apply tolerates. Replay of
// pre-#159 history therefore never fails. A v1 payload reaches v3 through the
// UpcasterChain fixpoint loop: ContractCreatedEventUpcaster raises it to v2
// (billing_cycle → interval), then this upcaster raises it to v3.
type ContractCreatedIdempotencyKeyUpcaster struct{}

// CanUpcast returns true ONLY for ContractCreatedEvent at exactly schema
// version 2. Matching the exact fromVersion (not <= 2) keeps the fixpoint chain
// order-independent (issue #197): were this upcaster to accept <= 2 and be
// applied before ContractCreatedEventUpcaster, a v1 payload would jump straight
// to v3 and SKIP the v1→v2 billing_cycle→interval migration entirely, leaving
// a legacy billing_cycle payload with no interval. Restricting to == 2 forces
// the chain to run 1→2 (ContractCreatedEventUpcaster) then 2→3 (here)
// regardless of registration order — mirroring ContractSuspendedEventUpcaster's
// exact-version guard.
func (u *ContractCreatedIdempotencyKeyUpcaster) CanUpcast(eventType eventstore.EventType, fromVersion int) bool {
	return eventType == EventTypeContractCreated && fromVersion == 2
}

// Upcast bumps a ContractCreatedEvent to schema version 3. It leaves the
// payload otherwise untouched: a missing idempotency_key deserializes to "",
// which Apply recognizes as "historical event, key never recorded".
func (u *ContractCreatedIdempotencyKeyUpcaster) Upcast(event eventstore.Event) (eventstore.Event, error) {
	event.SchemaVersion = 3
	return event, nil
}

// ContractCreatedMetadataUpcaster migrates ContractCreatedEvent payloads from
// schema version 3 to version 4. Version 4 added the optional metadata field
// (issue #219) carrying integrator-defined key-value pairs.
//
// Historical events never carried metadata, and none can be recovered, so this
// upcaster only bumps SchemaVersion to 4 (idempotently, mirroring
// ContractCreatedIdempotencyKeyUpcaster): a missing metadata field deserializes
// to a nil map, which the aggregate's Apply tolerates. Replay of pre-#219
// history therefore never fails. A v1 payload reaches v4 through the
// UpcasterChain fixpoint loop: 1→2 (billing_cycle → interval), 2→3
// (idempotency_key), then 3→4 (here).
type ContractCreatedMetadataUpcaster struct{}

// CanUpcast returns true ONLY for ContractCreatedEvent at exactly schema
// version 3. Matching the exact fromVersion (not <= 3) keeps the fixpoint chain
// order-independent (issue #197): were this upcaster to accept <= 3 and be
// applied first, a v1 payload would jump straight to v4 and SKIP the v1→v2
// billing_cycle→interval migration entirely. Restricting to == 3 forces the
// chain to run 1→2, 2→3, 3→4 regardless of registration order — mirroring
// ContractCreatedIdempotencyKeyUpcaster's exact-version guard.
func (u *ContractCreatedMetadataUpcaster) CanUpcast(eventType eventstore.EventType, fromVersion int) bool {
	return eventType == EventTypeContractCreated && fromVersion == 3
}

// Upcast bumps a ContractCreatedEvent to schema version 4. It leaves the
// payload otherwise untouched: a missing metadata field deserializes to nil,
// which Apply recognizes as "historical event, metadata never recorded".
func (u *ContractCreatedMetadataUpcaster) Upcast(event eventstore.Event) (eventstore.Event, error) {
	event.SchemaVersion = 4
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

// suspendedAtSet reports whether a raw JSON suspended_at field is present and
// carries a non-zero timestamp. A zero time.Time ("0001-01-01T00:00:00Z") means
// the anchor was never really recorded, so the upcaster substitutes the event's
// OccurredAt to keep the resume-time period extension well-defined.
func suspendedAtSet(raw map[string]json.RawMessage, key string) bool {
	v, ok := raw[key]
	if !ok {
		return false
	}
	var t time.Time
	if err := json.Unmarshal(v, &t); err != nil {
		return false
	}
	return !t.IsZero()
}

// ContractSuspendedEventUpcaster migrates historical ContractSuspendedEvent
// payloads (schema version 1) to version 2. Version 2 promoted the
// extend_contract flag onto the event and made suspended_at a reconstruction
// input for the resume-time period extension (issue #194).
//
// Legacy defaults:
//   - extend_contract: false. Pre-#194 suspensions never carried the flag and no
//     core path extended the billing period, so the historically-correct default
//     is "do not extend". A missing extend_contract already deserializes to
//     false; the upcaster writes an explicit false when absent (and never
//     overwrites an existing value), keeping the migrated payload self-describing.
//   - suspended_at: fall back to the event's OccurredAt when absent or zero.
//     Every real v1 payload already carried suspended_at, but defending the
//     resume-extension math against a zero SuspendedAt (which would otherwise
//     compute an enormous duration) is cheap. OccurredAt is the envelope
//     timestamp of the suspension, so it is the correct anchor.
type ContractSuspendedEventUpcaster struct{}

// CanUpcast returns true ONLY for ContractSuspendedEvent at exactly schema
// version 1. Matching the exact fromVersion (not <= 1) keeps the fixpoint chain
// order-independent: a freshly written v2 event is skipped regardless of where
// this upcaster sits in the chain.
func (u *ContractSuspendedEventUpcaster) CanUpcast(eventType eventstore.EventType, fromVersion int) bool {
	return eventType == EventTypeContractSuspended && fromVersion == 1
}

// Upcast bumps a ContractSuspendedEvent to schema version 2, ensuring
// extend_contract is present (default false) and suspended_at is populated
// (falling back to the event's OccurredAt when absent or zero-valued).
func (u *ContractSuspendedEventUpcaster) Upcast(event eventstore.Event) (eventstore.Event, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &raw); err != nil {
		return event, fmt.Errorf("upcaster: failed to unmarshal ContractSuspendedEvent: %w", err)
	}

	if _, ok := raw["extend_contract"]; !ok {
		raw["extend_contract"] = []byte("false")
	}

	if !suspendedAtSet(raw, "suspended_at") {
		fallback, err := json.Marshal(event.OccurredAt)
		if err != nil {
			return event, fmt.Errorf("upcaster: failed to marshal fallback suspended_at: %w", err)
		}
		raw["suspended_at"] = fallback
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return event, fmt.Errorf("upcaster: failed to marshal ContractSuspendedEvent: %w", err)
	}
	event.Data = data
	event.SchemaVersion = 2
	return event, nil
}

// NewContractUpcasterChain returns an UpcasterChain with all contract upcasters.
func NewContractUpcasterChain() *eventstore.UpcasterChain {
	return eventstore.NewUpcasterChain(
		&PriceChangedEventUpcaster{},
		&ContractCreatedEventUpcaster{},
		&ContractCreatedIdempotencyKeyUpcaster{},
		&ContractCreatedMetadataUpcaster{},
		&ContractRenewedEventUpcaster{},
		&TrialEndedEventUpcaster{},
		&ContractSuspendedEventUpcaster{},
	)
}
