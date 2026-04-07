package contract

import (
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// buildEventHistory generates a realistic event history of the given size.
// It cycles through create -> activate -> suspend -> resume -> renew patterns.
func buildEventHistory(n int, clock shared.Clock) []eventstore.Event {
	contractID := shared.ContractID("bench-contract")
	now := clock.Now()
	events := make([]eventstore.Event, 0, n)

	// First event is always Created
	createdEvt := &ContractCreatedEvent{
		ContractID:   contractID,
		AccountID:    shared.AccountID("bench-account"),
		PriceID:      shared.PriceID("bench-price"),
		Price:        shared.NewMoney(big.NewRat(9800, 1), shared.CurrencyJPY),
		BasePrice:    shared.NewMoney(big.NewRat(9800, 1), shared.CurrencyJPY),
		BillingCycle: BillingCycleMonthly,
		ContractType: ContractTypeSubscription,
		AutoRenew:    true,
		CreatedAt:    now,
	}
	data, _ := json.Marshal(createdEvt)
	events = append(events, eventstore.Event{
		ID:            shared.GenerateID(),
		StreamID:      string(contractID),
		Type:          EventTypeContractCreated,
		Version:       1,
		SchemaVersion: 1,
		Data:          data,
		OccurredAt:    now,
	})

	if n <= 1 {
		return events
	}

	// Second event: activate
	periodStart := now.Add(time.Hour)
	periodEnd := periodStart.AddDate(0, 1, 0)
	period, _ := shared.NewDateRange(periodStart, periodEnd)
	activatedEvt := &ContractActivatedEvent{
		ContractID:    contractID,
		ActivatedAt:   periodStart,
		CurrentPeriod: period,
	}
	data, _ = json.Marshal(activatedEvt)
	events = append(events, eventstore.Event{
		ID:            shared.GenerateID(),
		StreamID:      string(contractID),
		Type:          EventTypeContractActivated,
		Version:       2,
		SchemaVersion: 1,
		Data:          data,
		OccurredAt:    periodStart,
	})

	// Fill remaining events with suspend/resume/renew cycle
	cycleEvents := []struct {
		builder func(int, time.Time) (eventstore.DomainEvent, eventstore.EventType)
	}{
		{func(v int, t time.Time) (eventstore.DomainEvent, eventstore.EventType) {
			return &ContractSuspendedEvent{
				ContractID:      contractID,
				SuspendedAt:     t,
				BillingBehavior: SuspensionBillingSkip,
				Reason:          "bench",
			}, EventTypeContractSuspended
		}},
		{func(v int, t time.Time) (eventstore.DomainEvent, eventstore.EventType) {
			return &ContractResumedEvent{
				ContractID: contractID,
				ResumedAt:  t,
			}, EventTypeContractResumed
		}},
	}

	for i := 2; i < n; i++ {
		t := now.Add(time.Duration(i) * time.Hour)
		cb := cycleEvents[i%len(cycleEvents)]
		domainEvt, evtType := cb.builder(i+1, t)
		data, _ = json.Marshal(domainEvt)
		events = append(events, eventstore.Event{
			ID:            shared.GenerateID(),
			StreamID:      string(contractID),
			Type:          evtType,
			Version:       i + 1,
			SchemaVersion: 1,
			Data:          data,
			OccurredAt:    t,
		})
	}

	return events
}

func BenchmarkLoadFromHistory_10Events(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	events := buildEventHistory(10, clock)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agg := NewContractAggregate("bench-contract", clock)
		if err := agg.LoadFromHistory(events); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadFromHistory_100Events(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	events := buildEventHistory(100, clock)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agg := NewContractAggregate("bench-contract", clock)
		if err := agg.LoadFromHistory(events); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadFromHistory_1000Events(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	events := buildEventHistory(1000, clock)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agg := NewContractAggregate("bench-contract", clock)
		if err := agg.LoadFromHistory(events); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalSnapshot(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	events := buildEventHistory(50, clock)
	agg := NewContractAggregate("bench-contract", clock)
	if err := agg.LoadFromHistory(events); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := agg.MarshalSnapshot()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadFromSnapshot(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	events := buildEventHistory(50, clock)
	agg := NewContractAggregate("bench-contract", clock)
	if err := agg.LoadFromHistory(events); err != nil {
		b.Fatal(err)
	}

	state, err := agg.MarshalSnapshot()
	if err != nil {
		b.Fatal(err)
	}
	snapshot := eventstore.Snapshot{
		StreamID:  "bench-contract",
		Version:   agg.Version(),
		State:     state,
		AsOf:      clock.Now(),
		CreatedAt: clock.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		restored := NewContractAggregate("bench-contract", clock)
		if err := restored.LoadFromSnapshot(snapshot); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadFromSnapshot_Then100Events(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	// Build 200 events total: snapshot at 100, then replay remaining 100
	allEvents := buildEventHistory(200, clock)
	snapshotAgg := NewContractAggregate("bench-contract", clock)
	if err := snapshotAgg.LoadFromHistory(allEvents[:100]); err != nil {
		b.Fatal(err)
	}
	state, err := snapshotAgg.MarshalSnapshot()
	if err != nil {
		b.Fatal(err)
	}
	snapshot := eventstore.Snapshot{
		StreamID:  "bench-contract",
		Version:   snapshotAgg.Version(),
		State:     state,
		AsOf:      clock.Now(),
		CreatedAt: clock.Now(),
	}
	remainingEvents := allEvents[100:]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agg := NewContractAggregate("bench-contract", clock)
		if err := agg.LoadFromSnapshot(snapshot); err != nil {
			b.Fatal(err)
		}
		if err := agg.LoadFromHistory(remainingEvents); err != nil {
			b.Fatal(err)
		}
	}
}
