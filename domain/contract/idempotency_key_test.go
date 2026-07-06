package contract

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// Tests for the creation idempotency key (issue #159): Create validates
// presence, the key is carried on ContractCreatedEvent (schema v3), and
// historical events / snapshots without the key still replay.

func TestCreate_EmptyIdempotencyKey_Rejected(t *testing.T) {
	agg := newTestAggregate()
	cmd := newTestCommand()
	cmd.IdempotencyKey = ""

	err := agg.Create(cmd, newTestMetadata())
	if err == nil {
		t.Fatal("expected validation error for empty IdempotencyKey, got nil")
	}
	var de *shared.DomainError
	if !errors.As(err, &de) || de.Code != shared.ErrCodeValidation {
		t.Errorf("expected DomainError %s, got %v", shared.ErrCodeValidation, err)
	}
	if agg.Status() != "" {
		t.Errorf("aggregate must stay uncreated on rejected Create, status=%s", agg.Status())
	}
	if len(agg.UncommittedEvents()) != 0 {
		t.Errorf("no event must be raised on rejected Create, got %d", len(agg.UncommittedEvents()))
	}
}

func TestCreate_CarriesIdempotencyKeyOnEvent(t *testing.T) {
	agg := newTestAggregate()
	cmd := newTestCommand()
	cmd.IdempotencyKey = "idem-carry-1"

	if err := agg.Create(cmd, newTestMetadata()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if got := agg.IdempotencyKey(); got != "idem-carry-1" {
		t.Errorf("aggregate IdempotencyKey = %q, want idem-carry-1", got)
	}

	events := agg.UncommittedEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	var payload struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.Unmarshal(events[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}
	if payload.IdempotencyKey != "idem-carry-1" {
		t.Errorf("event idempotency_key = %q, want idem-carry-1", payload.IdempotencyKey)
	}
}

// TestLoadFromHistory_V1CreatedWithoutKey verifies that a v1 payload
// (billing_cycle-only, pre-#111, no idempotency_key) chains through
// ContractCreatedEventUpcaster (→v2) and ContractCreatedIdempotencyKeyUpcaster
// (→v3) and replays with an empty key.
func TestLoadFromHistory_V1CreatedWithoutKey(t *testing.T) {
	legacy := map[string]interface{}{
		"contract_id":   "c-idem-v1",
		"account_id":    "acc-1",
		"price_id":      "price-1",
		"billing_cycle": "monthly",
		"contract_type": "subscription",
		"created_at":    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data, _ := json.Marshal(legacy)
	events := []eventstore.Event{
		{Type: EventTypeContractCreated, SchemaVersion: 1, Data: data},
	}

	agg := NewContractAggregate(shared.ContractID("c-idem-v1"), newTestClock())
	if err := agg.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed for v1 payload without idempotency_key: %v", err)
	}
	if agg.Status() != ContractStatusDraft {
		t.Errorf("expected draft after replay, got %s", agg.Status())
	}
	if agg.IdempotencyKey() != "" {
		t.Errorf("historical event must replay with empty key, got %q", agg.IdempotencyKey())
	}
	if !agg.GetInterval().Equals(pricing.Monthly()) {
		t.Errorf("v1 billing_cycle must still upcast to interval, got %s", agg.GetInterval())
	}
}

// TestLoadFromHistory_V2CreatedWithoutKey verifies that a v2 payload
// (interval-based, written between #153 and #159, no idempotency_key) upcasts
// to v3 and replays with an empty key.
func TestLoadFromHistory_V2CreatedWithoutKey(t *testing.T) {
	v2 := map[string]interface{}{
		"contract_id":   "c-idem-v2",
		"account_id":    "acc-2",
		"price_id":      "price-2",
		"interval":      pricing.Yearly(),
		"contract_type": "subscription",
		"auto_renew":    true,
		"created_at":    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data, _ := json.Marshal(v2)
	events := []eventstore.Event{
		{Type: EventTypeContractCreated, SchemaVersion: 2, Data: data},
	}

	agg := NewContractAggregate(shared.ContractID("c-idem-v2"), newTestClock())
	if err := agg.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed for v2 payload without idempotency_key: %v", err)
	}
	if agg.IdempotencyKey() != "" {
		t.Errorf("historical event must replay with empty key, got %q", agg.IdempotencyKey())
	}
	if !agg.GetInterval().Equals(pricing.Yearly()) {
		t.Errorf("interval must survive the v2→v3 bump, got %s", agg.GetInterval())
	}
	if !agg.AutoRenew() {
		t.Error("auto_renew must survive the v2→v3 bump")
	}
}

func TestContractCreatedIdempotencyKeyUpcaster_ChainToV3(t *testing.T) {
	u := &ContractCreatedIdempotencyKeyUpcaster{}
	if !u.CanUpcast(EventTypeContractCreated, 1) {
		t.Error("expected CanUpcast=true for ContractCreated v1")
	}
	if !u.CanUpcast(EventTypeContractCreated, 2) {
		t.Error("expected CanUpcast=true for ContractCreated v2")
	}
	if u.CanUpcast(EventTypeContractCreated, 3) {
		t.Error("expected CanUpcast=false for ContractCreated v3")
	}
	if u.CanUpcast(EventTypeContractRenewed, 2) {
		t.Error("expected CanUpcast=false for other event types")
	}

	// A v1 payload runs through the full chain and lands at v3 with the
	// payload migration (billing_cycle → interval) from the v1→v2 upcaster
	// preserved.
	legacy := map[string]interface{}{
		"contract_id":   "c1",
		"billing_cycle": "yearly",
		"contract_type": "subscription",
	}
	data, _ := json.Marshal(legacy)
	chain := NewContractUpcasterChain()
	out, err := chain.Upcast(eventstore.Event{Type: EventTypeContractCreated, SchemaVersion: 1, Data: data})
	if err != nil {
		t.Fatalf("chain Upcast failed: %v", err)
	}
	if out.SchemaVersion != 3 {
		t.Errorf("expected chain to land at SchemaVersion 3, got %d", out.SchemaVersion)
	}
	domainEvent, err := contractEventRegistry.Deserialize(out.Type, out.Data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}
	created, ok := domainEvent.(*ContractCreatedEvent)
	if !ok {
		t.Fatalf("expected *ContractCreatedEvent, got %T", domainEvent)
	}
	if !created.Interval.Equals(pricing.Yearly()) {
		t.Errorf("v1→v2 interval migration must be preserved on the way to v3, got %s", created.Interval)
	}
	if created.IdempotencyKey != "" {
		t.Errorf("historical payload must deserialize with empty key, got %q", created.IdempotencyKey)
	}
}

func TestSnapshotRoundTrip_PreservesIdempotencyKey(t *testing.T) {
	agg := newTestAggregate()
	cmd := newTestCommand()
	cmd.IdempotencyKey = "idem-snapshot-1"
	if err := agg.Create(cmd, newTestMetadata()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	data, err := agg.MarshalSnapshot()
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}
	restored := NewContractAggregate(agg.ContractID(), newTestClock())
	if err := restored.LoadFromSnapshot(eventstore.Snapshot{State: data, Version: agg.Version()}); err != nil {
		t.Fatalf("LoadFromSnapshot: %v", err)
	}
	if restored.IdempotencyKey() != "idem-snapshot-1" {
		t.Errorf("snapshot round-trip lost idempotency key: got %q", restored.IdempotencyKey())
	}
}

func TestLoadFromSnapshot_LegacySnapshotWithoutKey(t *testing.T) {
	// A pre-#159 snapshot has no idempotency_key field; restore must succeed
	// with an empty key (mirroring event replay tolerance).
	legacy := map[string]interface{}{
		"schema_version": 2,
		"contract_id":    "c-snap-legacy",
		"account_id":     "acc-1",
		"status":         "active",
		"contract_type":  "subscription",
		"interval":       pricing.Monthly(),
	}
	data, _ := json.Marshal(legacy)

	agg := NewContractAggregate(shared.ContractID("c-snap-legacy"), newTestClock())
	if err := agg.LoadFromSnapshot(eventstore.Snapshot{State: data, Version: 5}); err != nil {
		t.Fatalf("LoadFromSnapshot failed for legacy snapshot: %v", err)
	}
	if agg.IdempotencyKey() != "" {
		t.Errorf("legacy snapshot must restore with empty key, got %q", agg.IdempotencyKey())
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected active, got %s", agg.Status())
	}
}
