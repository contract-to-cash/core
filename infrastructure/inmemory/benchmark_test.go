package inmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

func makeBenchEvent(streamID string, version int, clock shared.Clock) eventstore.Event {
	data, _ := json.Marshal(map[string]interface{}{
		"contract_id":   streamID,
		"account_id":    "bench-account",
		"price_id":      "bench-price",
		"price":         map[string]interface{}{"amount": "9800", "currency": "JPY"},
		"billing_cycle": "monthly",
		"contract_type": "subscription",
		"auto_renew":    true,
		"created_at":    clock.Now().Format(time.RFC3339),
	})
	return eventstore.Event{
		ID:            shared.GenerateID(),
		StreamID:      streamID,
		Type:          "contract.created",
		Version:       version,
		SchemaVersion: 1,
		Data:          data,
		Metadata:      eventstore.EventMetadata{UserID: "bench-user"},
		OccurredAt:    clock.Now(),
	}
}

func BenchmarkEventStore_Append(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	ctx := context.Background()

	// Pre-allocate stores and events
	stores := make([]*InMemoryEventStore, b.N)
	streamIDs := make([]string, b.N)
	events := make([][]eventstore.Event, b.N)
	for i := 0; i < b.N; i++ {
		stores[i] = NewInMemoryEventStore(clock)
		streamIDs[i] = fmt.Sprintf("stream-%d", i)
		events[i] = []eventstore.Event{makeBenchEvent(streamIDs[i], 1, clock)}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = stores[i].Append(ctx, streamIDs[i], events[i], 0)
	}
}

func BenchmarkEventStore_Append_Batch10(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	ctx := context.Background()

	// Pre-allocate stores and event batches
	stores := make([]*InMemoryEventStore, b.N)
	streamIDs := make([]string, b.N)
	batches := make([][]eventstore.Event, b.N)
	for i := 0; i < b.N; i++ {
		stores[i] = NewInMemoryEventStore(clock)
		streamIDs[i] = fmt.Sprintf("stream-%d", i)
		batch := make([]eventstore.Event, 10)
		for j := range batch {
			batch[j] = makeBenchEvent(streamIDs[i], j+1, clock)
		}
		batches[i] = batch
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = stores[i].Append(ctx, streamIDs[i], batches[i], 0)
	}
}

func BenchmarkBalanceRepository_FindAvailable_100Entries(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	ctx := context.Background()

	repo := NewInMemoryBalanceRepository(clock)
	accountID := shared.AccountID("bench-account")

	// Populate 100 balance entries
	for i := 0; i < 100; i++ {
		entry, _ := balance.NewBalanceEntry(
			accountID,
			shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
			balance.BalanceReasonManualAdjustment,
			clock.Now(),
		)
		if err := repo.Save(ctx, entry); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entries, err := repo.FindAvailable(ctx, accountID, shared.CurrencyJPY)
		if err != nil {
			b.Fatal(err)
		}
		if len(entries) != 100 {
			b.Fatalf("expected 100 entries, got %d", len(entries))
		}
	}
}

func BenchmarkEventStore_Load_100Events(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	ctx := context.Background()

	store := NewInMemoryEventStore(clock)
	streamID := "bench-stream"
	for v := 1; v <= 100; v++ {
		evt := makeBenchEvent(streamID, v, clock)
		if err := store.Append(ctx, streamID, []eventstore.Event{evt}, v-1); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		events, err := store.Load(ctx, streamID)
		if err != nil {
			b.Fatal(err)
		}
		if len(events) != 100 {
			b.Fatalf("expected 100 events, got %d", len(events))
		}
	}
}

func BenchmarkEventStore_SaveSnapshot(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	ctx := context.Background()
	store := NewInMemoryEventStore(clock)

	state, _ := json.Marshal(map[string]interface{}{
		"contract_id": "bench-contract",
		"status":      "active",
		"price":       shared.NewMoney(big.NewRat(9800, 1), shared.CurrencyJPY),
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		snap := eventstore.Snapshot{
			StreamID:  "bench-stream",
			Version:   i + 1,
			State:     state,
			AsOf:      clock.Now(),
			CreatedAt: clock.Now(),
		}
		if err := store.SaveSnapshot(ctx, snap); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEventStore_LoadSnapshot(b *testing.B) {
	b.ReportAllocs()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	ctx := context.Background()
	store := NewInMemoryEventStore(clock)

	state, _ := json.Marshal(map[string]interface{}{
		"contract_id": "bench-contract",
		"status":      "active",
	})
	snap := eventstore.Snapshot{
		StreamID:  "bench-stream",
		Version:   100,
		State:     state,
		AsOf:      clock.Now(),
		CreatedAt: clock.Now(),
	}
	if err := store.SaveSnapshot(ctx, snap); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := store.LoadSnapshot(ctx, "bench-stream")
		if err != nil {
			b.Fatal(err)
		}
		if s == nil {
			b.Fatal("snapshot not found")
		}
	}
}
