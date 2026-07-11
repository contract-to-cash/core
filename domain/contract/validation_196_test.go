package contract

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// --- Item 5: UnscheduleChange terminal guard + Cancel clears pending state ---

func TestCancel_ClearsPendingPriceChange(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()

	// Schedule an end-of-term price change so pendingPriceID is set.
	if err := agg.ChangePrice(shared.NewPriceID(), ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice(EndOfTerm) failed: %v", err)
	}
	if !agg.HasPendingChange() {
		t.Fatal("expected pending change after scheduling")
	}

	if err := agg.Cancel("done", meta); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if agg.HasPendingChange() {
		t.Error("expected pending change cleared after Cancel")
	}
	if agg.PendingPriceID() != nil {
		t.Error("expected PendingPriceID nil after Cancel")
	}
}

func TestCancel_FromTrialing_ClearsTrialConfig(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()
	if err := agg.Create(newTestCommand(), meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	// newTestClock is 2026-01-01, so a future trial end.
	if err := agg.StartTrial(TrialConfiguration{TrialEndDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}, meta); err != nil {
		t.Fatalf("StartTrial failed: %v", err)
	}
	if err := agg.Cancel("bail", meta); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if agg.TrialConfig() != nil {
		t.Error("expected trial config cleared after cancelling a trialing contract")
	}
}

func TestUnscheduleChange_RejectedOnTerminalContract(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()
	if err := agg.ChangePrice(shared.NewPriceID(), ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice failed: %v", err)
	}
	if err := agg.Cancel("done", meta); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	err := agg.UnscheduleChange("noop", meta)
	if err == nil {
		t.Fatal("expected error unscheduling change on cancelled contract")
	}
	var de *shared.DomainError
	if !errors.As(err, &de) || de.Code != shared.ErrCodeInvalidStateTransition {
		t.Errorf("expected invalid_state_transition, got %v", err)
	}
}

// --- Item 7: Create constructor/command validation ---

func TestCreate_Validation(t *testing.T) {
	base := func() CreateContractCommand { return newTestCommand() }

	t.Run("nil clock is rejected instead of panicking", func(t *testing.T) {
		agg := NewContractAggregate(shared.ContractID("nilclock-1"), nil)
		err := agg.Create(base(), newTestMetadata())
		assertCode(t, err, shared.ErrCodeValidation)
	})

	t.Run("empty AccountID rejected", func(t *testing.T) {
		agg := newTestAggregate()
		cmd := base()
		cmd.AccountID = ""
		assertCode(t, agg.Create(cmd, newTestMetadata()), shared.ErrCodeValidation)
	})

	t.Run("neither PriceID nor Price rejected", func(t *testing.T) {
		agg := newTestAggregate()
		cmd := base()
		cmd.PriceID = ""
		cmd.Price = shared.Zero(shared.CurrencyJPY)
		assertCode(t, agg.Create(cmd, newTestMetadata()), shared.ErrCodeValidation)
	})

	t.Run("PriceID set with zero Price is accepted", func(t *testing.T) {
		agg := newTestAggregate()
		cmd := base()
		cmd.PriceID = shared.NewPriceID()
		cmd.Price = shared.Zero(shared.CurrencyJPY)
		cmd.BasePrice = shared.Zero(shared.CurrencyJPY)
		if err := agg.Create(cmd, newTestMetadata()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Price/BasePrice currency mismatch rejected", func(t *testing.T) {
		agg := newTestAggregate()
		cmd := base()
		cmd.Price = shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
		cmd.BasePrice = shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyUSD)
		assertCode(t, agg.Create(cmd, newTestMetadata()), shared.ErrCodeCurrencyMismatch)
	})

	t.Run("valid command still succeeds", func(t *testing.T) {
		agg := newTestAggregate()
		if err := agg.Create(base(), newTestMetadata()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// --- Item 8: legacy snapshot strict billing_cycle ---

func TestLoadFromSnapshot_LegacyBillingCycle(t *testing.T) {
	// Build a legacy snapshot payload: no interval field, only billing_cycle.
	buildLegacy := func(cycle string) eventstore.Snapshot {
		state := contractSnapshotState{
			ContractID:   shared.ContractID("leg-1"),
			AccountID:    shared.AccountID("acc-1"),
			Status:       ContractStatusActive,
			ContractType: ContractTypeSubscription,
			// Interval left zero so LoadFromSnapshot takes the legacy branch.
			Price:     newTestMoney(),
			BasePrice: newTestMoney(),
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal to map: %v", err)
		}
		delete(m, "interval")
		if cycle != "__absent__" {
			m["billing_cycle"] = json.RawMessage(`"` + cycle + `"`)
		}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("re-marshal: %v", err)
		}
		return eventstore.Snapshot{StreamID: "leg-1", Version: 3, State: raw}
	}

	t.Run("valid monthly cycle loads", func(t *testing.T) {
		agg := NewContractAggregate(shared.ContractID("leg-1"), newTestClock())
		if err := agg.LoadFromSnapshot(buildLegacy("monthly")); err != nil {
			t.Fatalf("LoadFromSnapshot failed: %v", err)
		}
		if !agg.GetInterval().Equals(pricing.Monthly()) {
			t.Errorf("expected monthly interval, got %v", agg.GetInterval())
		}
	})

	t.Run("unknown cycle fails loudly", func(t *testing.T) {
		agg := NewContractAggregate(shared.ContractID("leg-1"), newTestClock())
		err := agg.LoadFromSnapshot(buildLegacy("fortnightly"))
		assertCode(t, err, shared.ErrCodeValidation)
	})

	t.Run("absent cycle fails loudly", func(t *testing.T) {
		agg := NewContractAggregate(shared.ContractID("leg-1"), newTestClock())
		err := agg.LoadFromSnapshot(buildLegacy("__absent__"))
		assertCode(t, err, shared.ErrCodeValidation)
	})
}

// --- Item 10: StartTrial validation ---

func TestStartTrial_Validation(t *testing.T) {
	// newTestClock is fixed at 2026-01-01.
	future := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	past := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)

	newDraft := func() *ContractAggregate {
		agg := newTestAggregate()
		if err := agg.Create(newTestCommand(), newTestMetadata()); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		return agg
	}

	t.Run("zero trial end date rejected", func(t *testing.T) {
		assertCode(t, newDraft().StartTrial(TrialConfiguration{}, newTestMetadata()), shared.ErrCodeValidation)
	})
	t.Run("past trial end date rejected", func(t *testing.T) {
		assertCode(t, newDraft().StartTrial(TrialConfiguration{TrialEndDate: past}, newTestMetadata()), shared.ErrCodeValidation)
	})
	t.Run("negative reminder day rejected", func(t *testing.T) {
		assertCode(t, newDraft().StartTrial(TrialConfiguration{TrialEndDate: future, ConversionReminderDays: []int{3, -1}}, newTestMetadata()), shared.ErrCodeValidation)
	})
	t.Run("valid future config accepted", func(t *testing.T) {
		if err := newDraft().StartTrial(TrialConfiguration{TrialEndDate: future, ConversionReminderDays: []int{3}}, newTestMetadata()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func assertCode(t *testing.T, err error, want shared.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	var de *shared.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected *shared.DomainError, got %T (%v)", err, err)
	}
	if de.Code != want {
		t.Errorf("expected code %s, got %s", want, de.Code)
	}
}
