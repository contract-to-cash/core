package inmemory

import (
	"context"
	"testing"
)

func TestInMemoryCheckpointStore_LoadDefaultZero(t *testing.T) {
	cp := NewInMemoryCheckpointStore()
	pos, err := cp.Load(context.Background(), "p")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if pos != 0 {
		t.Fatalf("expected 0 for unknown projection, got %d", pos)
	}
}

func TestInMemoryCheckpointStore_SaveLoadRoundTrip(t *testing.T) {
	cp := NewInMemoryCheckpointStore()
	ctx := context.Background()

	if err := cp.Save(ctx, "p", 42); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if err := cp.Save(ctx, "q", 7); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if pos, _ := cp.Load(ctx, "p"); pos != 42 {
		t.Fatalf("expected 42 for p, got %d", pos)
	}
	if pos, _ := cp.Load(ctx, "q"); pos != 7 {
		t.Fatalf("expected 7 for q, got %d", pos)
	}

	// Overwrite advances.
	if err := cp.Save(ctx, "p", 100); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if pos, _ := cp.Load(ctx, "p"); pos != 100 {
		t.Fatalf("expected 100 for p after overwrite, got %d", pos)
	}
}
