package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
)

// TestUpcaster_LegacyBillingCycleStream_ReplaysAsInterval persists a contract
// event stream in the pre-#111 on-disk format — where the billing interval was
// carried by the now-removed billing_cycle string fields — and verifies that
// replaying it through the repository (which runs the upcaster chain in
// LoadFromHistory) reconstructs the interval-based aggregate correctly.
//
// This guards the Event Sourcing invariant that historical events remain
// readable after the schema change (SchemaVersion + Upcaster, not twin fields).
func TestUpcaster_LegacyBillingCycleStream_ReplaysAsInterval(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)

	contractID := shared.NewContractID()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// 1. Legacy ContractCreatedEvent: only billing_cycle, no interval.
	createPayload := map[string]interface{}{
		"contract_id":   string(contractID),
		"account_id":    "acc-legacy",
		"price_id":      "price-legacy",
		"price":         map[string]interface{}{"amount": "3000/1", "currency": "JPY"},
		"base_price":    map[string]interface{}{"amount": "3000/1", "currency": "JPY"},
		"billing_cycle": "monthly",
		"contract_type": "subscription",
		"auto_renew":    true,
		"created_at":    base,
	}

	// 2. ContractActivatedEvent for a one-month initial period.
	activatePayload := map[string]interface{}{
		"contract_id":  string(contractID),
		"activated_at": base,
		"current_period": map[string]interface{}{
			"start": base,
			"end":   base.AddDate(0, 1, 0),
		},
	}

	// 3. Legacy ContractRenewedEvent: only old/new billing_cycle, no intervals.
	renewPayload := map[string]interface{}{
		"contract_id": string(contractID),
		"old_period": map[string]interface{}{
			"start": base,
			"end":   base.AddDate(0, 1, 0),
		},
		"new_period": map[string]interface{}{
			"start": base.AddDate(0, 1, 0),
			"end":   base.AddDate(1, 1, 0),
		},
		"old_price_id":      "price-legacy",
		"new_price_id":      "price-legacy",
		"price_changed":     false,
		"old_billing_cycle": "monthly",
		"new_billing_cycle": "yearly",
		"renewed_at":        base.AddDate(0, 1, 0),
	}

	events := []eventstore.Event{
		legacyEvent(t, contract.EventTypeContractCreated, 1, base, createPayload),
		legacyEvent(t, contract.EventTypeContractActivated, 2, base, activatePayload),
		legacyEvent(t, contract.EventTypeContractRenewed, 3, base.AddDate(0, 1, 0), renewPayload),
	}

	if err := eventStore.Append(ctx, string(contractID), events, 0); err != nil {
		t.Fatalf("Append legacy events failed: %v", err)
	}

	// Reload as of a point after all events — this replays through the upcaster.
	agg, err := contractRepo.FindByIDAsOf(ctx, contractID, base.AddDate(2, 0, 0))
	if err != nil {
		t.Fatalf("FindByIDAsOf failed: %v", err)
	}

	// The renewal upcast old/new billing_cycle → intervals, so the current
	// interval must be yearly.
	if !agg.GetInterval().Equals(pricing.Yearly()) {
		t.Errorf("expected interval yearly after replaying legacy stream, got %s", agg.GetInterval())
	}
	if agg.Status() != contract.ContractStatusActive {
		t.Errorf("expected active status, got %s", agg.Status())
	}
	// New billing period should span one year (from the yearly renewal).
	if got := agg.CurrentPeriod().End(); !got.Equal(base.AddDate(1, 1, 0)) {
		t.Errorf("expected period end %v, got %v", base.AddDate(1, 1, 0), got)
	}
}

// TestUpcaster_LegacyCreatedOnly_ReplaysMonthly verifies a stream with only a
// legacy create event recovers the monthly interval from billing_cycle.
func TestUpcaster_LegacyCreatedOnly_ReplaysMonthly(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)

	contractID := shared.NewContractID()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	createPayload := map[string]interface{}{
		"contract_id":   string(contractID),
		"account_id":    "acc-legacy",
		"price_id":      "price-legacy",
		"price":         map[string]interface{}{"amount": "1000/1", "currency": "JPY"},
		"base_price":    map[string]interface{}{"amount": "1000/1", "currency": "JPY"},
		"billing_cycle": "monthly",
		"contract_type": "subscription",
		"auto_renew":    true,
		"created_at":    base,
	}

	events := []eventstore.Event{
		legacyEvent(t, contract.EventTypeContractCreated, 1, base, createPayload),
	}
	if err := eventStore.Append(ctx, string(contractID), events, 0); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	agg, err := contractRepo.FindByIDAsOf(ctx, contractID, base.AddDate(1, 0, 0))
	if err != nil {
		t.Fatalf("FindByIDAsOf failed: %v", err)
	}
	if !agg.GetInterval().Equals(pricing.Monthly()) {
		t.Errorf("expected interval monthly from legacy billing_cycle, got %s", agg.GetInterval())
	}
}

// legacyEvent builds a persisted eventstore.Event at SchemaVersion 1 with the
// given JSON payload, mimicking a record written before the #111 schema change.
func legacyEvent(t *testing.T, typ eventstore.EventType, version int, occurredAt time.Time, payload map[string]interface{}) eventstore.Event {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal legacy %s payload: %v", typ, err)
	}
	return eventstore.Event{
		StreamID:      payloadStreamID(payload),
		Type:          typ,
		Version:       version,
		SchemaVersion: 1,
		Data:          data,
		OccurredAt:    occurredAt,
	}
}

func payloadStreamID(payload map[string]interface{}) string {
	if id, ok := payload["contract_id"].(string); ok {
		return id
	}
	return ""
}
