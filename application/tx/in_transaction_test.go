package tx

import (
	"context"
	"testing"
)

// TestInTransaction pins the InTransaction probe: false outside any Run, true
// inside a Run closure (where a nested Run would join), true inside a joined
// nested Run, and false again once the transaction has exited.
func TestInTransaction(t *testing.T) {
	ctx := context.Background()
	if InTransaction(ctx) {
		t.Fatal("InTransaction must be false on a bare context")
	}

	mgr := NewNoopTxManagerExplicit(Repos{})
	err := Run(ctx, mgr, func(txCtx context.Context, _ Repos) error {
		if !InTransaction(txCtx) {
			t.Error("InTransaction must be true inside a Run closure")
		}
		// A nested Run joins the outer transaction; the probe stays true.
		return Run(txCtx, mgr, func(nestedCtx context.Context, _ Repos) error {
			if !InTransaction(nestedCtx) {
				t.Error("InTransaction must be true inside a joined nested Run")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if InTransaction(ctx) {
		t.Fatal("InTransaction must remain false on the original context after Run returns")
	}
}
