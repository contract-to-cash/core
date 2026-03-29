package tx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/credit"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
)

// --- NoopTxManager ---

func TestNoopTxManager_CommitOnNilReturn(t *testing.T) {
	repos := tx.Repos{}
	mgr := tx.NewNoopTxManager(repos)

	called := false
	err := mgr.RunInTx(context.Background(), func(ctx context.Context, r tx.Repos) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !called {
		t.Fatal("closure was not called")
	}
}

func TestNoopTxManager_RollbackOnError(t *testing.T) {
	repos := tx.Repos{}
	mgr := tx.NewNoopTxManager(repos)

	testErr := errors.New("test error")
	err := mgr.RunInTx(context.Background(), func(ctx context.Context, r tx.Repos) error {
		return testErr
	})
	if !errors.Is(err, testErr) {
		t.Fatalf("expected test error, got %v", err)
	}
}

func TestNoopTxManager_PassesRepos(t *testing.T) {
	repos := tx.Repos{
		// Fields are interface types, nil is valid for this test
	}
	mgr := tx.NewNoopTxManager(repos)

	err := mgr.RunInTx(context.Background(), func(ctx context.Context, r tx.Repos) error {
		// Verify the repos are passed through
		if r != repos {
			t.Fatal("repos were not passed through")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNoopTxManager_PassesContext(t *testing.T) {
	type ctxKey string
	key := ctxKey("test")
	ctx := context.WithValue(context.Background(), key, "value")

	mgr := tx.NewNoopTxManager(tx.Repos{})

	err := mgr.RunInTx(ctx, func(receivedCtx context.Context, r tx.Repos) error {
		if receivedCtx.Value(key) != "value" {
			t.Fatal("context was not passed through")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- RetryOnConflict ---

func TestRetryOnConflict_SucceedsFirstTry(t *testing.T) {
	attempts := 0
	err := tx.RetryOnConflict(3, func() error {
		attempts++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}

func TestRetryOnConflict_SucceedsAfterRetries(t *testing.T) {
	attempts := 0
	err := tx.RetryOnConflict(3, func() error {
		attempts++
		if attempts < 3 {
			return tx.ErrVersionConflict
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryOnConflict_ExhaustsRetries(t *testing.T) {
	attempts := 0
	err := tx.RetryOnConflict(3, func() error {
		attempts++
		return tx.ErrVersionConflict
	})
	if !errors.Is(err, tx.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryOnConflict_NonConflictErrorNotRetried(t *testing.T) {
	otherErr := errors.New("other error")
	attempts := 0
	err := tx.RetryOnConflict(3, func() error {
		attempts++
		return otherErr
	})
	if !errors.Is(err, otherErr) {
		t.Fatalf("expected other error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt (no retry), got %d", attempts)
	}
}

// --- Repos struct compile-time check ---

func TestRepos_HasExpectedFields(t *testing.T) {
	// This test verifies that the Repos struct has the expected fields.
	// If a field is added/removed, this test will fail at compile time.
	_ = tx.Repos{
		Contracts: (contract.Repository)(nil),
		Invoices:  (invoice.Repository)(nil),
		Payments:  (payment.Repository)(nil),
		Credits:   (credit.Repository)(nil),
	}
}
