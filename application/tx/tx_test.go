package tx_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
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

// RetryOnConflict must clamp a non-positive maxAttempts to 1 and still run fn
// exactly once. Returning nil without ever invoking fn (the previous behaviour)
// silently reported success while doing no work (issue #187).
func TestRetryOnConflict_ZeroAttemptsClampedToOne(t *testing.T) {
	attempts := 0
	err := tx.RetryOnConflict(0, func() error {
		attempts++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected fn to run once when maxAttempts=0, got %d", attempts)
	}
}

func TestRetryOnConflict_NegativeAttemptsClampedToOne(t *testing.T) {
	attempts := 0
	err := tx.RetryOnConflict(-5, func() error {
		attempts++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected fn to run once when maxAttempts is negative, got %d", attempts)
	}
}

// With the clamp, a non-positive maxAttempts still surfaces fn's error (rather
// than a bogus nil), because fn runs at least once.
func TestRetryOnConflict_ZeroAttemptsSurfacesError(t *testing.T) {
	sentinel := errors.New("boom")
	attempts := 0
	err := tx.RetryOnConflict(0, func() error {
		attempts++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected fn's error to surface, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}

func TestRetryOnConflict_OneAttemptRunsOnce(t *testing.T) {
	attempts := 0
	err := tx.RetryOnConflict(1, func() error {
		attempts++
		return tx.ErrVersionConflict
	})
	if !errors.Is(err, tx.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected exactly 1 attempt with maxAttempts=1 (no retry), got %d", attempts)
	}
}

// --- NoopTxManager detection and the non-atomic warning (issue #187) ---

func TestIsNoop(t *testing.T) {
	if !tx.IsNoop(tx.NewNoopTxManager(tx.Repos{})) {
		t.Fatal("expected default NoopTxManager to be detected as noop")
	}
	if !tx.IsNoop(tx.NewNoopTxManagerExplicit(tx.Repos{})) {
		t.Fatal("expected explicit NoopTxManager to be detected as noop")
	}
	if tx.IsNoop(fakeTxManager{}) {
		t.Fatal("a non-noop TxManager must not be detected as noop")
	}
}

func TestIsExplicitNoop(t *testing.T) {
	if tx.IsExplicitNoop(tx.NewNoopTxManager(tx.Repos{})) {
		t.Fatal("default NoopTxManager must NOT be reported as explicit")
	}
	if !tx.IsExplicitNoop(tx.NewNoopTxManagerExplicit(tx.Repos{})) {
		t.Fatal("expected explicit NoopTxManager to be reported as explicit")
	}
	if tx.IsExplicitNoop(fakeTxManager{}) {
		t.Fatal("a non-noop TxManager must not be reported as explicit noop")
	}
}

func TestWarnIfDefaultNoop_WarnsForDefaultNoop(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	tx.WarnIfDefaultNoop(logger, tx.NewNoopTxManager(tx.Repos{}), "TestComponent", "wire a TxManager")

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("expected a WARN-level log, got: %q", out)
	}
	if !strings.Contains(out, "NOT atomic") {
		t.Fatalf("expected the non-atomic warning text, got: %q", out)
	}
	if !strings.Contains(out, "TestComponent") {
		t.Fatalf("expected the component name in the log, got: %q", out)
	}
}

func TestWarnIfDefaultNoop_SilentForExplicitNoop(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	tx.WarnIfDefaultNoop(logger, tx.NewNoopTxManagerExplicit(tx.Repos{}), "TestComponent", "wire a TxManager")

	if buf.Len() != 0 {
		t.Fatalf("expected no log for an explicit NoopTxManager, got: %q", buf.String())
	}
}

func TestWarnIfDefaultNoop_SilentForRealTxManager(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	tx.WarnIfDefaultNoop(logger, fakeTxManager{}, "TestComponent", "wire a TxManager")

	if buf.Len() != 0 {
		t.Fatalf("expected no log for a real TxManager, got: %q", buf.String())
	}
}

// fakeTxManager is a non-noop TxManager used to prove IsNoop / WarnIfDefaultNoop
// only fire for NoopTxManager.
type fakeTxManager struct{}

func (fakeTxManager) RunInTx(ctx context.Context, fn func(context.Context, tx.Repos) error) error {
	return fn(ctx, tx.Repos{})
}

// --- Version-conflict encoding interoperability (issue #152) ---

func TestIsVersionConflict_Sentinel(t *testing.T) {
	if !tx.IsVersionConflict(tx.ErrVersionConflict) {
		t.Fatal("expected sentinel to be recognised as a version conflict")
	}
	if !tx.IsVersionConflict(fmt.Errorf("wrapped: %w", tx.ErrVersionConflict)) {
		t.Fatal("expected wrapped sentinel to be recognised")
	}
}

func TestIsVersionConflict_DomainErrorCode(t *testing.T) {
	// The event store returns this encoding (infrastructure/inmemory/event_store.go).
	de := shared.NewDomainError(shared.ErrCodeVersionConflict, "expected version 1 but stream is at 2")
	if !tx.IsVersionConflict(de) {
		t.Fatal("expected a version_conflict DomainError to be recognised")
	}
	if !tx.IsVersionConflict(fmt.Errorf("save failed: %w", de)) {
		t.Fatal("expected a wrapped version_conflict DomainError to be recognised")
	}
}

func TestIsVersionConflict_OtherDomainErrorNotMatched(t *testing.T) {
	de := shared.NewDomainError(shared.ErrCodeNotFound, "nope")
	if tx.IsVersionConflict(de) {
		t.Fatal("a non-version_conflict DomainError must not be treated as a conflict")
	}
	if tx.IsVersionConflict(errors.New("plain")) {
		t.Fatal("a plain error must not be treated as a conflict")
	}
}

// RetryOnConflict must retry when fn returns the event store's DomainError
// encoding, not only the sentinel — otherwise contract (event-sourced) save
// conflicts would silently fail to retry.
func TestRetryOnConflict_RetriesOnDomainErrorVersionConflict(t *testing.T) {
	attempts := 0
	err := tx.RetryOnConflict(5, func() error {
		attempts++
		if attempts < 3 {
			return shared.NewDomainError(shared.ErrCodeVersionConflict,
				fmt.Sprintf("expected version %d but stream is at %d", attempts-1, attempts))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
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
		Balances:  (balance.Repository)(nil),
	}
}
