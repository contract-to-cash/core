package inmemory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// collect reads up to want events from ch (or until ch closes / timeout).
func collect(t *testing.T, ch <-chan eventstore.Event, want int, timeout time.Duration) []eventstore.Event {
	t.Helper()
	var got []eventstore.Event
	deadline := time.After(timeout)
	for len(got) < want {
		select {
		case e, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, e)
		case <-deadline:
			return got
		}
	}
	return got
}

// TestSubscribe_BurstNoLoss verifies that a burst far larger than the old 100-slot
// buffer loses no events for a consumer that drains at its own pace.
func TestSubscribe_BurstNoLoss(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := store.Subscribe(ctx, 0)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	const n = 1000 // > subscriberBufferSize (100)
	for i := 1; i <= n; i++ {
		e := makeEvent("stream-1", i, time.Unix(int64(i), 0).UTC())
		if err := store.Append(ctx, "stream-1", []eventstore.Event{e}, i-1); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}

	got := collect(t, ch, n, 5*time.Second)
	if len(got) != n {
		t.Fatalf("expected %d events (no loss), got %d", n, len(got))
	}
	for i, e := range got {
		if e.GlobalPosition != int64(i+1) {
			t.Fatalf("event %d out of order: GlobalPosition=%d", i, e.GlobalPosition)
		}
	}
}

// TestSubscribe_BackfillFromPosition verifies that events already stored are
// replayed when subscribing from an earlier position.
func TestSubscribe_BackfillFromPosition(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Append 5 events before subscribing.
	for i := 1; i <= 5; i++ {
		e := makeEvent("s", i, time.Unix(int64(i), 0).UTC())
		if err := store.Append(ctx, "s", []eventstore.Event{e}, i-1); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}

	// Subscribe from position 2: expect backfill of positions 3,4,5.
	ch, err := store.Subscribe(ctx, 2)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	got := collect(t, ch, 3, 2*time.Second)
	if len(got) != 3 {
		t.Fatalf("expected 3 backfilled events, got %d", len(got))
	}
	for i, e := range got {
		if want := int64(i + 3); e.GlobalPosition != want {
			t.Fatalf("backfill event %d: want position %d, got %d", i, want, e.GlobalPosition)
		}
	}
}

// TestSubscribe_BackfillLiveNoGap subscribes while events are being appended
// concurrently and asserts every event is delivered exactly once, in order —
// the backfill→live handover must not drop or duplicate an event.
func TestSubscribe_BackfillLiveNoGap(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const n = 500
	var wg sync.WaitGroup
	wg.Add(1)
	// Appender runs concurrently with Subscribe to exercise the handover race.
	go func() {
		defer wg.Done()
		for i := 1; i <= n; i++ {
			e := makeEvent("s", i, time.Unix(int64(i), 0).UTC())
			if err := store.Append(ctx, "s", []eventstore.Event{e}, i-1); err != nil {
				// Serialize appends via expectedVersion; retry not needed because
				// this is the only writer. A failure is a real bug.
				panic(fmt.Sprintf("append %d failed: %v", i, err))
			}
		}
	}()

	// Subscribe from 0 concurrently: backfill (whatever exists now) + live tail.
	ch, err := store.Subscribe(ctx, 0)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	got := collect(t, ch, n, 5*time.Second)
	wg.Wait()

	if len(got) != n {
		t.Fatalf("expected %d events with no gap, got %d", n, len(got))
	}
	seen := make(map[int64]bool, n)
	for i, e := range got {
		if e.GlobalPosition != int64(i+1) {
			t.Fatalf("event %d out of order or gapped: GlobalPosition=%d", i, e.GlobalPosition)
		}
		if seen[e.GlobalPosition] {
			t.Fatalf("duplicate delivery of position %d", e.GlobalPosition)
		}
		seen[e.GlobalPosition] = true
	}
}

// TestSubscribe_ContextCancelClosesChannel verifies that cancelling the context
// unregisters the subscriber and closes the channel (no leak).
func TestSubscribe_ContextCancelClosesChannel(t *testing.T) {
	store := NewInMemoryEventStore(shared.SystemClock{})
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := store.Subscribe(ctx, 0)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	cancel()

	// The channel must eventually close.
	select {
	case _, ok := <-ch:
		for ok {
			_, ok = <-ch
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel was not closed after context cancellation")
	}

	// Subscriber must be unregistered.
	store.mu.RLock()
	n := len(store.subscribers)
	store.mu.RUnlock()
	if n != 0 {
		t.Fatalf("expected 0 subscribers after cancel, got %d", n)
	}
}
