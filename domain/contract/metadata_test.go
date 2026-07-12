package contract

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// Tests for integrator-defined contract metadata (issue #219): accepted on
// CreateContractCommand, carried on ContractCreatedEvent (schema v4), exposed
// via a defensively-copied getter, and backward compatible with pre-#219
// events and snapshots (no metadata → nil, replay never fails).

func TestCreate_CarriesMetadataOnEvent(t *testing.T) {
	agg := newTestAggregate()
	cmd := newTestCommand()
	cmd.Metadata = map[string]string{"creator_id": "user-42", "channel": "self-serve"}

	if err := agg.Create(cmd, newTestMetadata()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	got := agg.Metadata()
	if len(got) != 2 || got["creator_id"] != "user-42" || got["channel"] != "self-serve" {
		t.Errorf("aggregate Metadata() = %v, want creator_id=user-42 channel=self-serve", got)
	}

	events := agg.UncommittedEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	var payload struct {
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(events[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}
	if payload.Metadata["creator_id"] != "user-42" || payload.Metadata["channel"] != "self-serve" {
		t.Errorf("event metadata = %v, want creator_id=user-42 channel=self-serve", payload.Metadata)
	}
}

func TestCreate_Metadata_DefensiveCopies(t *testing.T) {
	agg := newTestAggregate()
	cmd := newTestCommand()
	src := map[string]string{"creator_id": "user-42"}
	cmd.Metadata = src

	if err := agg.Create(cmd, newTestMetadata()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Mutating the caller-owned command map after Create must not rewrite the
	// aggregate state or the raised event payload (intake defense).
	src["creator_id"] = "tampered"
	src["extra"] = "tampered"
	if got := agg.Metadata(); got["creator_id"] != "user-42" || len(got) != 1 {
		t.Errorf("command-map mutation leaked into aggregate: %v", got)
	}
	var payload struct {
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(agg.UncommittedEvents()[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}
	if payload.Metadata["creator_id"] != "user-42" || len(payload.Metadata) != 1 {
		t.Errorf("command-map mutation leaked into event payload: %v", payload.Metadata)
	}

	// Mutating the getter result must not affect the aggregate either.
	out := agg.Metadata()
	out["creator_id"] = "tampered"
	if got := agg.Metadata(); got["creator_id"] != "user-42" {
		t.Errorf("getter-result mutation leaked into aggregate: %v", got)
	}
}

func TestMetadata_EmptyByDefault(t *testing.T) {
	agg := newTestAggregate()
	if err := agg.Create(newTestCommand(), newTestMetadata()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	got := agg.Metadata()
	if got == nil {
		t.Fatal("Metadata() must never return nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty metadata by default, got %v", got)
	}
}

// TestLoadFromHistory_ReplaysMetadata proves that metadata written by Create
// survives event-sourced reconstruction: the raised events replay into a fresh
// aggregate with the metadata restored.
func TestLoadFromHistory_ReplaysMetadata(t *testing.T) {
	agg := newTestAggregate()
	cmd := newTestCommand()
	cmd.Metadata = map[string]string{"creator_id": "user-42"}
	if err := agg.Create(cmd, newTestMetadata()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	restored := NewContractAggregate(agg.ContractID(), newTestClock())
	if err := restored.LoadFromHistory(agg.UncommittedEvents()); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}
	if got := restored.Metadata(); got["creator_id"] != "user-42" || len(got) != 1 {
		t.Errorf("replay lost metadata: got %v", got)
	}
}

// TestLoadFromHistory_V3CreatedWithoutMetadata verifies that a v3 payload
// (idempotency_key-based, written between #159 and #219, no metadata) upcasts
// to v4 and replays with empty metadata — reconstruction from pre-#219 history
// must never fail.
func TestLoadFromHistory_V3CreatedWithoutMetadata(t *testing.T) {
	v3 := map[string]interface{}{
		"contract_id":     "c-meta-v3",
		"account_id":      "acc-3",
		"price_id":        "price-3",
		"idempotency_key": "idem-v3-1",
		"interval":        pricing.Monthly(),
		"contract_type":   "subscription",
		"auto_renew":      true,
		"created_at":      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data, _ := json.Marshal(v3)
	events := []eventstore.Event{
		{Type: EventTypeContractCreated, SchemaVersion: 3, Data: data},
	}

	agg := NewContractAggregate(shared.ContractID("c-meta-v3"), newTestClock())
	if err := agg.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed for v3 payload without metadata: %v", err)
	}
	if got := agg.Metadata(); len(got) != 0 {
		t.Errorf("historical event must replay with empty metadata, got %v", got)
	}
	if agg.IdempotencyKey() != "idem-v3-1" {
		t.Errorf("idempotency_key must survive the v3→v4 bump, got %q", agg.IdempotencyKey())
	}
	if !agg.GetInterval().Equals(pricing.Monthly()) {
		t.Errorf("interval must survive the v3→v4 bump, got %s", agg.GetInterval())
	}
	if !agg.AutoRenew() {
		t.Error("auto_renew must survive the v3→v4 bump")
	}
	if agg.Status() != ContractStatusDraft {
		t.Errorf("expected draft after replay, got %s", agg.Status())
	}
}

// TestLoadFromHistory_V1CreatedWithoutMetadata verifies that a v1 payload
// (billing_cycle-only, pre-#111) chains through all three ContractCreated
// upcasters (1→2→3→4) and replays with empty metadata and the interval
// recovered from billing_cycle.
func TestLoadFromHistory_V1CreatedWithoutMetadata(t *testing.T) {
	legacy := map[string]interface{}{
		"contract_id":   "c-meta-v1",
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

	agg := NewContractAggregate(shared.ContractID("c-meta-v1"), newTestClock())
	if err := agg.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed for v1 payload: %v", err)
	}
	if got := agg.Metadata(); len(got) != 0 {
		t.Errorf("historical event must replay with empty metadata, got %v", got)
	}
	if !agg.GetInterval().Equals(pricing.Monthly()) {
		t.Errorf("v1 billing_cycle must still upcast to interval, got %s", agg.GetInterval())
	}
}

func TestContractCreatedMetadataUpcaster_ChainToV4(t *testing.T) {
	u := &ContractCreatedMetadataUpcaster{}
	// Exact-version guard (issue #197): this upcaster only handles 3→4. Lower
	// versions are the earlier upcasters' job; a v1 payload reaches v4 through
	// the fixpoint chain, not by this upcaster jumping ahead (which would skip
	// the billing_cycle→interval migration).
	if u.CanUpcast(EventTypeContractCreated, 1) {
		t.Error("expected CanUpcast=false for ContractCreated v1 (exact-version guard)")
	}
	if u.CanUpcast(EventTypeContractCreated, 2) {
		t.Error("expected CanUpcast=false for ContractCreated v2 (exact-version guard)")
	}
	if !u.CanUpcast(EventTypeContractCreated, 3) {
		t.Error("expected CanUpcast=true for ContractCreated v3")
	}
	if u.CanUpcast(EventTypeContractCreated, 4) {
		t.Error("expected CanUpcast=false for ContractCreated v4 (already migrated)")
	}
	if u.CanUpcast(EventTypeContractRenewed, 3) {
		t.Error("expected CanUpcast=false for other event types")
	}

	// A v1 payload runs through the full chain and lands at v4 with the payload
	// migration (billing_cycle → interval) preserved and metadata absent (nil).
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
	if out.SchemaVersion != 4 {
		t.Errorf("expected chain to land at SchemaVersion 4, got %d", out.SchemaVersion)
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
		t.Errorf("v1→v2 interval migration must be preserved on the way to v4, got %s", created.Interval)
	}
	if created.Metadata != nil {
		t.Errorf("historical payload must deserialize with nil metadata, got %v", created.Metadata)
	}
}

func TestSnapshotRoundTrip_PreservesMetadata(t *testing.T) {
	agg := newTestAggregate()
	cmd := newTestCommand()
	cmd.Metadata = map[string]string{"creator_id": "user-42"}
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
	if got := restored.Metadata(); got["creator_id"] != "user-42" || len(got) != 1 {
		t.Errorf("snapshot round-trip lost metadata: got %v", got)
	}
}

func TestLoadFromSnapshot_LegacySnapshotWithoutMetadata(t *testing.T) {
	// A pre-#219 snapshot has no metadata field; restore must succeed with
	// empty metadata (mirroring event replay tolerance).
	legacy := map[string]interface{}{
		"schema_version": 3,
		"contract_id":    "c-snap-meta-legacy",
		"account_id":     "acc-1",
		"status":         "active",
		"contract_type":  "subscription",
		"interval":       pricing.Monthly(),
	}
	data, _ := json.Marshal(legacy)

	agg := NewContractAggregate(shared.ContractID("c-snap-meta-legacy"), newTestClock())
	if err := agg.LoadFromSnapshot(eventstore.Snapshot{State: data, Version: 5}); err != nil {
		t.Fatalf("LoadFromSnapshot failed for legacy snapshot: %v", err)
	}
	if got := agg.Metadata(); len(got) != 0 {
		t.Errorf("legacy snapshot must restore with empty metadata, got %v", got)
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected active, got %s", agg.Status())
	}
}
