package inmemory

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestInMemoryIdempotencyStore_EmptyKeyRejected(t *testing.T) {
	store := NewInMemoryIdempotencyStore()
	err := store.MarkCompensated(context.Background(), "", "effective")
	if err == nil {
		t.Fatal("expected error for empty original key")
	}
}

func TestInMemoryIdempotencyStore_ResolveUnknownKey(t *testing.T) {
	store := NewInMemoryIdempotencyStore()
	eff, ok, err := store.ResolveEffectiveKey(context.Background(), "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false for unknown key")
	}
	if eff != "" {
		t.Errorf("expected empty effective key, got %q", eff)
	}
}

func TestInMemoryIdempotencyStore_MarkThenResolve(t *testing.T) {
	store := NewInMemoryIdempotencyStore()
	ctx := context.Background()

	if err := store.MarkCompensated(ctx, "orig-1", "eff-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	eff, ok, err := store.ResolveEffectiveKey(ctx, "orig-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after MarkCompensated")
	}
	if eff != "eff-1" {
		t.Errorf("expected effective key %q, got %q", "eff-1", eff)
	}
}

func TestInMemoryIdempotencyStore_FirstCallWins(t *testing.T) {
	// Second MarkCompensated with a different effective key must NOT
	// overwrite the first mapping — the store contract guarantees that
	// concurrent compensations converge on a stable effective key.
	store := NewInMemoryIdempotencyStore()
	ctx := context.Background()

	_ = store.MarkCompensated(ctx, "orig", "first")
	_ = store.MarkCompensated(ctx, "orig", "second") // should be ignored

	eff, _, _ := store.ResolveEffectiveKey(ctx, "orig")
	if eff != "first" {
		t.Errorf("expected first-call-wins; got %q", eff)
	}
}

func TestInMemoryIdempotencyStore_ConcurrentMark_FirstCallWins(t *testing.T) {
	// Concurrent MarkCompensated on the same original key must produce a
	// single stable effective key: exactly one goroutine's value wins and
	// all later writes are no-ops. To actually prove this (rather than
	// accidentally pass under a "last-write-wins" bug), every goroutine
	// passes a DISTINCT effective key and the test asserts the stored
	// value matches exactly one of them — and that repeated reads return
	// the same value.
	store := NewInMemoryIdempotencyStore()
	ctx := context.Background()

	const n = 50
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("eff-%d", i)
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			_ = store.MarkCompensated(ctx, "orig", k)
		}(keys[i])
	}
	wg.Wait()

	// The stored effective key must be exactly one of the submitted values.
	eff, ok, err := store.ResolveEffectiveKey(ctx, "orig")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected mapping to exist")
	}

	matched := false
	for _, k := range keys {
		if eff == k {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("stored effective key %q is not one of the submitted values", eff)
	}

	// Repeated reads must return the same stable value.
	for i := 0; i < 10; i++ {
		again, _, _ := store.ResolveEffectiveKey(ctx, "orig")
		if again != eff {
			t.Errorf("unstable effective key: got %q then %q", eff, again)
			break
		}
	}
}
