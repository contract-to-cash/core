package tx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/contract-to-cash/core/application/tx"
)

func TestSaga_NoCompensations(t *testing.T) {
	s := tx.NewSaga()
	err := s.Compensate(context.Background())
	if err != nil {
		t.Fatalf("expected nil error for empty saga, got %v", err)
	}
}

func TestSaga_CompensationsRunInReverseOrder(t *testing.T) {
	s := tx.NewSaga()

	var order []int
	s.AddCompensation(func(ctx context.Context) error {
		order = append(order, 1)
		return nil
	})
	s.AddCompensation(func(ctx context.Context) error {
		order = append(order, 2)
		return nil
	})
	s.AddCompensation(func(ctx context.Context) error {
		order = append(order, 3)
		return nil
	})

	err := s.Compensate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 compensations, got %d", len(order))
	}
	// Should run in reverse: 3, 2, 1
	if order[0] != 3 || order[1] != 2 || order[2] != 1 {
		t.Fatalf("expected reverse order [3,2,1], got %v", order)
	}
}

func TestSaga_CompensateAttemptsAll(t *testing.T) {
	s := tx.NewSaga()

	err1 := errors.New("comp1 failed")
	err3 := errors.New("comp3 failed")

	var called []int
	s.AddCompensation(func(ctx context.Context) error {
		called = append(called, 1)
		return err1
	})
	s.AddCompensation(func(ctx context.Context) error {
		called = append(called, 2)
		return nil
	})
	s.AddCompensation(func(ctx context.Context) error {
		called = append(called, 3)
		return err3
	})

	err := s.Compensate(context.Background())
	// All compensations should have been attempted
	if len(called) != 3 {
		t.Fatalf("expected 3 compensations attempted, got %d", len(called))
	}
	// Error should contain both failures
	if err == nil {
		t.Fatal("expected error from failed compensations")
	}
	if !errors.Is(err, err3) {
		t.Fatalf("expected err3 in joined error, got %v", err)
	}
	if !errors.Is(err, err1) {
		t.Fatalf("expected err1 in joined error, got %v", err)
	}
}

func TestSaga_CompensatePassesContext(t *testing.T) {
	type ctxKey string
	key := ctxKey("test")
	ctx := context.WithValue(context.Background(), key, "value")

	s := tx.NewSaga()
	s.AddCompensation(func(receivedCtx context.Context) error {
		if receivedCtx.Value(key) != "value" {
			t.Fatal("context was not passed to compensation")
		}
		return nil
	})

	err := s.Compensate(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
