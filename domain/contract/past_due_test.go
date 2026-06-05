package contract

import (
	"errors"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

// These tests cover the dunning state transitions that were previously
// unreachable: Active -> PastDue (payment failure) and PastDue -> Active
// (payment recovery). See architecture.md §4.3 and review #1.

func TestMarkPastDue_FromActive(t *testing.T) {
	agg := createActiveAggregate(t)

	if err := agg.MarkPastDue("payment failed", newTestMetadata()); err != nil {
		t.Fatalf("MarkPastDue failed: %v", err)
	}
	if agg.Status() != ContractStatusPastDue {
		t.Errorf("expected status past_due, got %s", agg.Status())
	}
}

func TestMarkPastDue_FromNonActive_Rejected(t *testing.T) {
	agg := newTestAggregate()
	if err := agg.Create(newTestCommand(), newTestMetadata()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	// Draft contract cannot be marked past due.
	err := agg.MarkPastDue("payment failed", newTestMetadata())
	if err == nil {
		t.Fatal("expected error marking draft contract past due")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) || domErr.Code != shared.ErrCodeInvalidStateTransition {
		t.Errorf("expected invalid_state_transition, got %v", err)
	}
}

func TestRecoverFromPastDue_BackToActive(t *testing.T) {
	agg := createActiveAggregate(t)
	if err := agg.MarkPastDue("payment failed", newTestMetadata()); err != nil {
		t.Fatalf("MarkPastDue failed: %v", err)
	}

	if err := agg.RecoverFromPastDue(newTestMetadata()); err != nil {
		t.Fatalf("RecoverFromPastDue failed: %v", err)
	}
	if agg.Status() != ContractStatusActive {
		t.Errorf("expected status active, got %s", agg.Status())
	}
}

func TestRecoverFromPastDue_FromActive_Rejected(t *testing.T) {
	agg := createActiveAggregate(t)
	if err := agg.RecoverFromPastDue(newTestMetadata()); err == nil {
		t.Fatal("expected error recovering an already-active contract")
	}
}

// PastDue -> Suspended and PastDue -> Cancelled were already permitted by the
// Suspend/Cancel guards; assert they remain reachable now that PastDue exists.
func TestPastDue_CanBeSuspended(t *testing.T) {
	agg := createActiveAggregate(t)
	if err := agg.MarkPastDue("payment failed", newTestMetadata()); err != nil {
		t.Fatalf("MarkPastDue failed: %v", err)
	}
	if err := agg.Suspend(SuspensionConfiguration{Reason: "max retries"}, newTestMetadata()); err != nil {
		t.Fatalf("Suspend from past_due failed: %v", err)
	}
	if agg.Status() != ContractStatusSuspended {
		t.Errorf("expected suspended, got %s", agg.Status())
	}
}

func TestPastDue_CanBeCancelled(t *testing.T) {
	agg := createActiveAggregate(t)
	if err := agg.MarkPastDue("payment failed", newTestMetadata()); err != nil {
		t.Fatalf("MarkPastDue failed: %v", err)
	}
	if err := agg.Cancel("non-payment", newTestMetadata()); err != nil {
		t.Fatalf("Cancel from past_due failed: %v", err)
	}
	if agg.Status() != ContractStatusCancelled {
		t.Errorf("expected cancelled, got %s", agg.Status())
	}
}

// TestPastDue_SurvivesEventReplay ensures the new events rebuild correctly from
// history (registry + Apply parity).
func TestPastDue_SurvivesEventReplay(t *testing.T) {
	agg := createActiveAggregate(t)
	if err := agg.MarkPastDue("payment failed", newTestMetadata()); err != nil {
		t.Fatalf("MarkPastDue failed: %v", err)
	}
	if err := agg.RecoverFromPastDue(newTestMetadata()); err != nil {
		t.Fatalf("RecoverFromPastDue failed: %v", err)
	}

	events := agg.UncommittedEvents()

	rebuilt := NewContractAggregate(agg.ContractID(), newTestClock())
	if err := rebuilt.LoadFromHistory(events); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}
	if rebuilt.Status() != ContractStatusActive {
		t.Errorf("expected replayed status active, got %s", rebuilt.Status())
	}
}
