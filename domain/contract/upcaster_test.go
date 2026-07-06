package contract

import (
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

func TestPriceChangedEventUpcaster_CanUpcast(t *testing.T) {
	u := &PriceChangedEventUpcaster{}

	if !u.CanUpcast(EventTypePriceChanged, 1) {
		t.Error("expected CanUpcast=true for PriceChanged v1")
	}
	if u.CanUpcast(EventTypePriceChanged, 2) {
		t.Error("expected CanUpcast=false for PriceChanged v2")
	}
	if u.CanUpcast(EventTypeContractCancelled, 1) {
		t.Error("expected CanUpcast=false for ContractCancelled")
	}
}

func TestPriceChangedEventUpcaster_Upcast_V1ToV2(t *testing.T) {
	u := &PriceChangedEventUpcaster{}

	// Simulate a v1 event with only Money fields (no PriceID, no Policy)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	v1Data := map[string]interface{}{
		"contract_id":  "test-contract-001",
		"old_price":    map[string]interface{}{"amount": "1000", "currency": "JPY"},
		"new_price":    map[string]interface{}{"amount": "2000", "currency": "JPY"},
		"changed_at":   now,
		"effective_at": now,
	}
	data, err := json.Marshal(v1Data)
	if err != nil {
		t.Fatalf("failed to marshal v1 data: %v", err)
	}

	event := eventstore.Event{
		Type:          EventTypePriceChanged,
		SchemaVersion: 1,
		Data:          data,
	}

	result, err := u.Upcast(event)
	if err != nil {
		t.Fatalf("Upcast failed: %v", err)
	}

	// Verify schema version bumped
	if result.SchemaVersion != 2 {
		t.Errorf("expected SchemaVersion=2, got %d", result.SchemaVersion)
	}

	// Verify policy was added
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(result.Data, &raw); err != nil {
		t.Fatalf("failed to unmarshal upcasted data: %v", err)
	}

	var policy string
	if err := json.Unmarshal(raw["policy"], &policy); err != nil {
		t.Fatalf("failed to unmarshal policy: %v", err)
	}
	if policy != string(ChangePolicyImmediate) {
		t.Errorf("expected policy=%s, got %s", ChangePolicyImmediate, policy)
	}

	// Verify PriceID fields were added
	var oldPriceID string
	if err := json.Unmarshal(raw["old_price_id"], &oldPriceID); err != nil {
		t.Fatalf("failed to unmarshal old_price_id: %v", err)
	}
	var newPriceID string
	if err := json.Unmarshal(raw["new_price_id"], &newPriceID); err != nil {
		t.Fatalf("failed to unmarshal new_price_id: %v", err)
	}

	// Verify legacy fields preserved
	if _, ok := raw["old_price"]; !ok {
		t.Error("expected old_price to be preserved")
	}
	if _, ok := raw["new_price"]; !ok {
		t.Error("expected new_price to be preserved")
	}
}

func TestPriceChangedEventUpcaster_Upcast_AlreadyV2(t *testing.T) {
	u := &PriceChangedEventUpcaster{}

	// V2 event with PriceID and Policy already set
	v2Data := map[string]interface{}{
		"contract_id":  "test-contract-001",
		"old_price_id": "price-old",
		"new_price_id": "price-new",
		"policy":       "immediate",
		"changed_at":   time.Now(),
	}
	data, _ := json.Marshal(v2Data)

	event := eventstore.Event{
		Type:          EventTypePriceChanged,
		SchemaVersion: 1, // even if marked v1, if fields exist they should be preserved
		Data:          data,
	}

	result, err := u.Upcast(event)
	if err != nil {
		t.Fatalf("Upcast failed: %v", err)
	}

	// Verify existing policy is NOT overwritten
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(result.Data, &raw)
	var policy string
	_ = json.Unmarshal(raw["policy"], &policy)
	if policy != "immediate" {
		t.Errorf("expected existing policy to be preserved, got %s", policy)
	}
}

func TestContractUpcasterChain_Integration(t *testing.T) {
	chain := NewContractUpcasterChain()

	// V1 PriceChangedEvent through the chain
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	oldPrice := shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY)
	newPrice := shared.NewMoney(new(big.Rat).SetInt64(2000), shared.CurrencyJPY)

	v1Event := PriceChangedEvent{
		ContractID:  shared.ContractID("test-contract-001"),
		OldPrice:    oldPrice,
		NewPrice:    newPrice,
		ChangedAt:   now,
		EffectiveAt: now,
	}
	data, _ := json.Marshal(v1Event)

	event := eventstore.Event{
		Type:          EventTypePriceChanged,
		SchemaVersion: 1,
		Data:          data,
	}

	result, err := chain.Upcast(event)
	if err != nil {
		t.Fatalf("chain Upcast failed: %v", err)
	}

	if result.SchemaVersion != 2 {
		t.Errorf("expected SchemaVersion=2, got %d", result.SchemaVersion)
	}

	// Deserialize the upcasted event and verify it can be applied
	domainEvent, err := contractEventRegistry.Deserialize(result.Type, result.Data)
	if err != nil {
		t.Fatalf("deserialize upcasted event failed: %v", err)
	}

	pce, ok := domainEvent.(*PriceChangedEvent)
	if !ok {
		t.Fatalf("expected *PriceChangedEvent, got %T", domainEvent)
	}

	if pce.Policy != ChangePolicyImmediate {
		t.Errorf("expected policy=%s, got %s", ChangePolicyImmediate, pce.Policy)
	}

	// Legacy Money fields should still be present
	if pce.OldPrice.Amount().Cmp(new(big.Rat).SetInt64(1000)) != 0 {
		t.Error("expected OldPrice=1000 preserved")
	}
	if pce.NewPrice.Amount().Cmp(new(big.Rat).SetInt64(2000)) != 0 {
		t.Error("expected NewPrice=2000 preserved")
	}

	// Verify the event can be applied to aggregate
	agg := newTestAggregate()
	_ = agg.Apply(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		Price:        oldPrice,
		BasePrice:    oldPrice,
		Interval:     pricing.Monthly(),
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	if err := agg.Apply(pce); err != nil {
		t.Fatalf("Apply upcasted event failed: %v", err)
	}
}

func TestLoadFromHistory_UpcastsV1PriceChangedEvent(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Build a v1 PriceChangedEvent (no PriceID, no Policy fields)
	v1PriceChanged := map[string]interface{}{
		"contract_id":  "test-contract-001",
		"old_price":    map[string]interface{}{"amount": "1000/1", "currency": "JPY"},
		"new_price":    map[string]interface{}{"amount": "2000/1", "currency": "JPY"},
		"changed_at":   now,
		"effective_at": now,
	}
	v1Data, _ := json.Marshal(v1PriceChanged)

	createData, _ := json.Marshal(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		PriceID:      shared.PriceID("price-001"),
		Price:        shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY),
		Interval:     pricing.Monthly(),
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	events := []eventstore.Event{
		{Type: EventTypeContractCreated, SchemaVersion: 1, Data: createData},
		{Type: EventTypePriceChanged, SchemaVersion: 1, Data: v1Data},
	}

	agg := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := agg.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory with v1 PriceChangedEvent failed: %v", err)
	}

	// The v1 event had no NewPriceID, so upcaster fills it with "".
	// But the aggregate should NOT have a corrupted priceID from Create.
	// After upcaster, the PriceChangedEvent Apply handler sets priceID to e.NewPriceID.
	// For v1 events the PriceID will be "" - this is expected legacy behavior.
	// The important thing is that LoadFromHistory didn't crash.
	if agg.Version() != 2 {
		t.Errorf("expected version 2, got %d", agg.Version())
	}
}

func TestContractCreatedEventUpcaster_BillingCycleToInterval(t *testing.T) {
	u := &ContractCreatedEventUpcaster{}

	if !u.CanUpcast(EventTypeContractCreated, 1) {
		t.Error("expected CanUpcast=true for ContractCreated v1")
	}
	if u.CanUpcast(EventTypeContractCreated, 2) {
		t.Error("expected CanUpcast=false for ContractCreated v2")
	}

	// Historical payload carrying only billing_cycle, no interval.
	legacy := map[string]interface{}{
		"contract_id":   "c1",
		"account_id":    "acc-1",
		"price_id":      "price-1",
		"billing_cycle": "yearly",
		"contract_type": "subscription",
		"created_at":    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data, _ := json.Marshal(legacy)
	event := eventstore.Event{Type: EventTypeContractCreated, SchemaVersion: 1, Data: data}

	result, err := u.Upcast(event)
	if err != nil {
		t.Fatalf("Upcast failed: %v", err)
	}
	if result.SchemaVersion != 2 {
		t.Errorf("expected SchemaVersion=2, got %d", result.SchemaVersion)
	}

	domainEvent, err := contractEventRegistry.Deserialize(result.Type, result.Data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}
	created, ok := domainEvent.(*ContractCreatedEvent)
	if !ok {
		t.Fatalf("expected *ContractCreatedEvent, got %T", domainEvent)
	}
	if !created.Interval.Equals(pricing.Yearly()) {
		t.Errorf("expected interval yearly from billing_cycle, got %s", created.Interval)
	}
}

func TestContractCreatedEventUpcaster_PreservesExistingInterval(t *testing.T) {
	u := &ContractCreatedEventUpcaster{}

	// Payload already carries an interval (quarterly); billing_cycle is empty/absent.
	// The upcaster must NOT overwrite the richer interval.
	created := &ContractCreatedEvent{
		ContractID:   shared.ContractID("c1"),
		AccountID:    shared.AccountID("acc-1"),
		Interval:     pricing.Quarterly(),
		ContractType: ContractTypeSubscription,
		CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data, _ := json.Marshal(created)
	event := eventstore.Event{Type: EventTypeContractCreated, SchemaVersion: 1, Data: data}

	result, err := u.Upcast(event)
	if err != nil {
		t.Fatalf("Upcast failed: %v", err)
	}

	domainEvent, _ := contractEventRegistry.Deserialize(result.Type, result.Data)
	got, ok := domainEvent.(*ContractCreatedEvent)
	if !ok {
		t.Fatalf("expected *ContractCreatedEvent, got %T", domainEvent)
	}
	if !got.Interval.Equals(pricing.Quarterly()) {
		t.Errorf("expected interval preserved as quarterly, got %s", got.Interval)
	}
}

func TestContractRenewedEventUpcaster_BillingCyclesToIntervals(t *testing.T) {
	u := &ContractRenewedEventUpcaster{}

	if !u.CanUpcast(EventTypeContractRenewed, 1) {
		t.Error("expected CanUpcast=true for ContractRenewed v1")
	}

	legacy := map[string]interface{}{
		"contract_id":       "c1",
		"old_price_id":      "price-A",
		"new_price_id":      "price-A",
		"price_changed":     false,
		"old_billing_cycle": "monthly",
		"new_billing_cycle": "yearly",
		"renewed_at":        time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	data, _ := json.Marshal(legacy)
	event := eventstore.Event{Type: EventTypeContractRenewed, SchemaVersion: 1, Data: data}

	result, err := u.Upcast(event)
	if err != nil {
		t.Fatalf("Upcast failed: %v", err)
	}
	if result.SchemaVersion != 2 {
		t.Errorf("expected SchemaVersion=2, got %d", result.SchemaVersion)
	}

	domainEvent, err := contractEventRegistry.Deserialize(result.Type, result.Data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}
	renewed, ok := domainEvent.(*ContractRenewedEvent)
	if !ok {
		t.Fatalf("expected *ContractRenewedEvent, got %T", domainEvent)
	}
	if !renewed.OldInterval.Equals(pricing.Monthly()) {
		t.Errorf("expected old interval monthly, got %s", renewed.OldInterval)
	}
	if !renewed.NewInterval.Equals(pricing.Yearly()) {
		t.Errorf("expected new interval yearly, got %s", renewed.NewInterval)
	}
}

// TestLoadFromHistory_UpcastsLegacyBillingCyclePayloads verifies that a full
// event stream persisted with only the deprecated billing_cycle fields replays
// correctly into the interval-based aggregate.
func TestLoadFromHistory_UpcastsLegacyBillingCyclePayloads(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	createLegacy := map[string]interface{}{
		"contract_id":   "test-contract-001",
		"account_id":    "acc-001",
		"price_id":      "price-001",
		"price":         map[string]interface{}{"amount": "1000/1", "currency": "JPY"},
		"base_price":    map[string]interface{}{"amount": "1000/1", "currency": "JPY"},
		"billing_cycle": "monthly",
		"contract_type": "subscription",
		"auto_renew":    true,
		"created_at":    now,
	}
	createData, _ := json.Marshal(createLegacy)

	activated := &ContractActivatedEvent{
		ContractID:    shared.ContractID("test-contract-001"),
		ActivatedAt:   now,
		CurrentPeriod: mustDateRange(t, now, now.AddDate(0, 1, 0)),
	}
	activatedData, _ := json.Marshal(activated)

	renewLegacy := map[string]interface{}{
		"contract_id":       "test-contract-001",
		"old_period":        map[string]interface{}{"start": now.AddDate(0, 1, 0), "end": now.AddDate(0, 2, 0)},
		"new_period":        map[string]interface{}{"start": now.AddDate(0, 2, 0), "end": now.AddDate(1, 2, 0)},
		"old_price_id":      "price-001",
		"new_price_id":      "price-001",
		"price_changed":     false,
		"old_billing_cycle": "monthly",
		"new_billing_cycle": "yearly",
		"renewed_at":        now.AddDate(0, 2, 0),
	}
	renewData, _ := json.Marshal(renewLegacy)

	events := []eventstore.Event{
		{Type: EventTypeContractCreated, SchemaVersion: 1, Data: createData},
		{Type: EventTypeContractActivated, SchemaVersion: 1, Data: activatedData},
		{Type: EventTypeContractRenewed, SchemaVersion: 1, Data: renewData},
	}

	agg := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := agg.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory with legacy billing_cycle payloads failed: %v", err)
	}

	// After creation the interval should have been recovered as monthly, then
	// updated to yearly by the renewal.
	if !agg.GetInterval().Equals(pricing.Yearly()) {
		t.Errorf("expected interval yearly after legacy replay, got %s", agg.GetInterval())
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected active after legacy replay, got %s", agg.Status())
	}
}

func mustDateRange(t *testing.T, start, end time.Time) shared.DateRange {
	t.Helper()
	r, err := shared.NewDateRange(start, end)
	if err != nil {
		t.Fatalf("NewDateRange failed: %v", err)
	}
	return r
}

func TestTrialEndedEventUpcaster_CanUpcast(t *testing.T) {
	u := &TrialEndedEventUpcaster{}

	if !u.CanUpcast(EventTypeTrialEnded, 1) {
		t.Error("expected CanUpcast=true for TrialEnded v1")
	}
	if u.CanUpcast(EventTypeTrialEnded, 2) {
		t.Error("expected CanUpcast=false for TrialEnded v2")
	}
	if u.CanUpcast(EventTypeContractCancelled, 1) {
		t.Error("expected CanUpcast=false for ContractCancelled")
	}
}

func TestTrialEndedEventUpcaster_BumpsSchemaVersion(t *testing.T) {
	u := &TrialEndedEventUpcaster{}

	// Legacy v1 payload: converted trial with no current_period field.
	legacy := map[string]interface{}{
		"contract_id": "test-contract-001",
		"ended_at":    time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		"converted":   true,
	}
	data, _ := json.Marshal(legacy)
	event := eventstore.Event{Type: EventTypeTrialEnded, SchemaVersion: 1, Data: data}

	result, err := u.Upcast(event)
	if err != nil {
		t.Fatalf("Upcast failed: %v", err)
	}
	if result.SchemaVersion != 2 {
		t.Errorf("expected SchemaVersion=2, got %d", result.SchemaVersion)
	}

	// The legacy payload still deserializes; current_period is the zero value,
	// which Apply recognizes and fills from the interval.
	domainEvent, err := contractEventRegistry.Deserialize(result.Type, result.Data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}
	ended, ok := domainEvent.(*TrialEndedEvent)
	if !ok {
		t.Fatalf("expected *TrialEndedEvent, got %T", domainEvent)
	}
	if !ended.Converted {
		t.Error("expected converted=true preserved")
	}
	if !ended.CurrentPeriod.IsZero() {
		t.Error("expected legacy current_period to be zero (filled at Apply)")
	}
}

// TestLoadFromHistory_UpcastsLegacyTrialEndedConverted verifies issue #146's
// replay semantics: a historical converted TrialEndedEvent with no
// current_period replays into an Active contract with a non-zero billing period,
// derived deterministically from the interval anchored at EndedAt.
func TestLoadFromHistory_UpcastsLegacyTrialEndedConverted(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	trialEnd := now.AddDate(0, 0, 14) // 2026-01-15

	createData, _ := json.Marshal(&ContractCreatedEvent{
		ContractID:   shared.ContractID("test-contract-001"),
		AccountID:    shared.AccountID("acc-001"),
		PriceID:      shared.PriceID("price-001"),
		Price:        shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(new(big.Rat).SetInt64(1000), shared.CurrencyJPY),
		Interval:     pricing.Monthly(),
		ContractType: ContractTypeSubscription,
		CreatedAt:    now,
	})

	trialStartData, _ := json.Marshal(&TrialStartedEvent{
		ContractID:  shared.ContractID("test-contract-001"),
		TrialConfig: TrialConfiguration{TrialEndDate: trialEnd, AutoConvert: true},
		StartedAt:   now,
	})

	// Legacy v1 TrialEndedEvent: converted, but WITHOUT current_period.
	legacyTrialEnded := map[string]interface{}{
		"contract_id": "test-contract-001",
		"ended_at":    trialEnd,
		"converted":   true,
	}
	trialEndData, _ := json.Marshal(legacyTrialEnded)

	events := []eventstore.Event{
		{Type: EventTypeContractCreated, SchemaVersion: 1, Data: createData},
		{Type: EventTypeTrialStarted, SchemaVersion: 1, Data: trialStartData},
		{Type: EventTypeTrialEnded, SchemaVersion: 1, Data: trialEndData},
	}

	agg := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := agg.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory with legacy TrialEndedEvent failed: %v", err)
	}

	if agg.Status() != ContractStatusActive {
		t.Errorf("expected active after legacy replay, got %s", agg.Status())
	}
	if agg.TrialConfig() != nil {
		t.Error("expected trialConfig cleared after trial end")
	}
	if agg.CurrentPeriod().IsZero() {
		t.Fatal("expected non-zero derived billing period for legacy converted trial")
	}
	// Derived deterministically: [EndedAt, EndedAt + 1 month).
	if !agg.CurrentPeriod().Start().Equal(trialEnd) {
		t.Errorf("expected derived period start %s, got %s", trialEnd, agg.CurrentPeriod().Start())
	}
	if !agg.CurrentPeriod().End().Equal(trialEnd.AddDate(0, 1, 0)) {
		t.Errorf("expected derived period end %s, got %s", trialEnd.AddDate(0, 1, 0), agg.CurrentPeriod().End())
	}

	// Replay must be deterministic: a second replay yields the same period.
	agg2 := NewContractAggregate(shared.ContractID("test-contract-001"), newTestClock())
	if err := agg2.LoadFromHistory(events); err != nil {
		t.Fatalf("second LoadFromHistory failed: %v", err)
	}
	if !agg2.CurrentPeriod().Equals(agg.CurrentPeriod()) {
		t.Errorf("replay not deterministic: %s vs %s", agg.CurrentPeriod(), agg2.CurrentPeriod())
	}
}

// TestNewEventsStampedCurrent_SkipUpcaster is the core replay-safety proof for
// issue #153. Two claims:
//  1. Events raised today carry their true (current) schema version, not a
//     hardcoded 1. ContractCreatedEvent is at v3 since issue #159
//     (idempotency_key); the other versioned events are at v2.
//  2. An event stamped with its current version passes through the contract
//     upcaster chain UNTOUCHED — every upcaster's CanUpcast requires
//     fromVersion below the current version, so a current event is skipped.
//     This is what makes a future non-idempotent upcaster safe: it can never
//     re-run against a freshly written event.
func TestNewEventsStampedCurrent_SkipUpcaster(t *testing.T) {
	// 1. Raising a ContractCreatedEvent through the aggregate stamps SchemaVersion 3.
	agg := newTestAggregate()
	if err := agg.Create(newTestCommand(), newTestMetadata()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	created := agg.UncommittedEvents()
	if len(created) != 1 || created[0].Type != EventTypeContractCreated {
		t.Fatalf("expected 1 ContractCreatedEvent, got %+v", created)
	}
	if created[0].SchemaVersion != 3 {
		t.Errorf("new ContractCreatedEvent must be stamped SchemaVersion 3, got %d", created[0].SchemaVersion)
	}

	// 2. Each versioned event type, stamped with its current version, must pass
	// through the chain untouched.
	chain := NewContractUpcasterChain()
	cases := []struct {
		name    string
		etype   eventstore.EventType
		version int
		event   eventstore.DomainEvent
	}{
		{"ContractCreated", EventTypeContractCreated, 3, &ContractCreatedEvent{ContractID: "c1", IdempotencyKey: "idem-1", Interval: pricing.Monthly(), CreatedAt: time.Now()}},
		{"PriceChanged", EventTypePriceChanged, 2, &PriceChangedEvent{ContractID: "c1", OldPriceID: "a", NewPriceID: "b", Policy: ChangePolicyImmediate}},
		{"TrialEnded", EventTypeTrialEnded, 2, &TrialEndedEvent{ContractID: "c1", Converted: true}},
		{"ContractRenewed", EventTypeContractRenewed, 2, &ContractRenewedEvent{ContractID: "c1", OldInterval: pricing.Monthly(), NewInterval: pricing.Yearly()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sv, ok := tc.event.(eventstore.SchemaVersioned)
			if !ok {
				t.Fatalf("%s must implement SchemaVersioned", tc.name)
			}
			if sv.CurrentSchemaVersion() != tc.version {
				t.Fatalf("%s CurrentSchemaVersion = %d, want %d", tc.name, sv.CurrentSchemaVersion(), tc.version)
			}

			data, _ := json.Marshal(tc.event)
			in := eventstore.Event{Type: tc.etype, SchemaVersion: tc.version, Data: data}
			out, err := chain.Upcast(in)
			if err != nil {
				t.Fatalf("Upcast failed: %v", err)
			}
			if out.SchemaVersion != tc.version {
				t.Errorf("current-version event must remain v%d (upcaster skipped), got %d", tc.version, out.SchemaVersion)
			}
			if string(out.Data) != string(in.Data) {
				t.Errorf("current-version event payload must be untouched by upcaster\n in: %s\nout: %s", in.Data, out.Data)
			}
		})
	}
}

func TestUpcasterChain_SkipsNonPriceEvents(t *testing.T) {
	chain := NewContractUpcasterChain()

	data, _ := json.Marshal(&ContractCancelledEvent{
		ContractID:  shared.ContractID("test-contract-001"),
		CancelledAt: time.Now(),
		Reason:      "test",
	})

	event := eventstore.Event{
		Type:          EventTypeContractCancelled,
		SchemaVersion: 1,
		Data:          data,
	}

	result, err := chain.Upcast(event)
	if err != nil {
		t.Fatalf("Upcast failed: %v", err)
	}

	// Should be unchanged
	if result.SchemaVersion != 1 {
		t.Errorf("expected SchemaVersion=1 (unchanged), got %d", result.SchemaVersion)
	}
}
