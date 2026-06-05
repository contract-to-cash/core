package tx

import (
	"context"
	"testing"
)

type countingTxManager struct {
	calls int
	repos Repos
}

func (m *countingTxManager) RunInTx(ctx context.Context, fn func(context.Context, Repos) error) error {
	m.calls++
	return fn(ctx, m.repos)
}

func TestRun_OpensTransactionWhenNoneActive(t *testing.T) {
	mgr := &countingTxManager{}
	var gotReposInClosure bool
	err := Run(context.Background(), mgr, func(ctx context.Context, _ Repos) error {
		// Inside Run, the ctx must now advertise an active transaction.
		if _, ok := reposFromContext(ctx); ok {
			gotReposInClosure = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr.calls != 1 {
		t.Errorf("expected 1 RunInTx call, got %d", mgr.calls)
	}
	if !gotReposInClosure {
		t.Error("expected ctx inside Run to carry the active transaction repos")
	}
}

func TestRun_JoinsExistingTransaction(t *testing.T) {
	outer := &countingTxManager{}
	inner := &countingTxManager{}

	err := Run(context.Background(), outer, func(outerCtx context.Context, _ Repos) error {
		// Nested Run with a DIFFERENT manager must join, not open a new tx.
		return Run(outerCtx, inner, func(_ context.Context, _ Repos) error {
			return nil
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outer.calls != 1 {
		t.Errorf("expected 1 outer RunInTx call, got %d", outer.calls)
	}
	if inner.calls != 0 {
		t.Errorf("expected nested Run to join the outer tx (0 inner calls), got %d", inner.calls)
	}
}

func TestRun_NestedUsesOuterRepos(t *testing.T) {
	sentinel := Repos{}
	outer := &countingTxManager{repos: sentinel}
	inner := &countingTxManager{}

	var innerRepos Repos
	_ = Run(context.Background(), outer, func(outerCtx context.Context, _ Repos) error {
		return Run(outerCtx, inner, func(_ context.Context, repos Repos) error {
			innerRepos = repos
			return nil
		})
	})
	// The nested closure must receive the OUTER transaction's repos.
	if innerRepos != sentinel {
		t.Error("nested Run did not receive the outer transaction's repos")
	}
}
