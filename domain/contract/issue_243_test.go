package contract

import (
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
)

// --- Issue #243 item 1: ContractExpiredEvent clears pending/trial state ---

// A scheduled end-of-term price change is NOT consumed when the renewal path
// expires the contract (autoRenew=false), so before #243 an expired (terminal)
// contract kept reporting HasPendingChange()==true. The Apply path must clear
// pending/trial state the same way ContractCancelledEvent does.
func TestExpire_ClearsPendingPriceChange(t *testing.T) {
	agg := createActiveAggregate(t) // newTestCommand has AutoRenew=false
	meta := newTestMetadata()

	if err := agg.ChangePrice(shared.NewPriceID(), ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice(EndOfTerm) failed: %v", err)
	}
	if !agg.HasPendingChange() {
		t.Fatal("expected pending change after scheduling")
	}

	// autoRenew=false, so the renewal path expires the contract.
	if err := agg.RenewWithInterval(pricing.Monthly(), meta); err != nil {
		t.Fatalf("RenewWithInterval failed: %v", err)
	}
	if agg.Status() != ContractStatusExpired {
		t.Fatalf("expected status expired, got %s", agg.Status())
	}
	if agg.HasPendingChange() {
		t.Error("expected pending change cleared after expiry")
	}
	if agg.PendingPriceID() != nil {
		t.Error("expected PendingPriceID nil after expiry")
	}
}

// TestExpire_ClearsPendingPriceChange_OnReplay proves the clearing is
// replay-deterministic: rebuilding the aggregate from the raw event history
// yields the same cleared state as the live aggregate.
func TestExpire_ClearsPendingPriceChange_OnReplay(t *testing.T) {
	agg := createActiveAggregate(t)
	meta := newTestMetadata()
	if err := agg.ChangePrice(shared.NewPriceID(), ChangePolicyEndOfTerm, nil, meta); err != nil {
		t.Fatalf("ChangePrice(EndOfTerm) failed: %v", err)
	}
	if err := agg.RenewWithInterval(pricing.Monthly(), meta); err != nil {
		t.Fatalf("RenewWithInterval failed: %v", err)
	}

	rebuilt := NewContractAggregate(agg.ContractID(), newTestClock())
	if err := rebuilt.LoadFromHistory(agg.UncommittedEvents()); err != nil {
		t.Fatalf("LoadFromHistory failed: %v", err)
	}
	if rebuilt.Status() != ContractStatusExpired {
		t.Fatalf("expected replayed status expired, got %s", rebuilt.Status())
	}
	if rebuilt.HasPendingChange() {
		t.Error("expected replayed pending change cleared after expiry")
	}
	if rebuilt.PendingPriceID() != nil {
		t.Error("expected replayed PendingPriceID nil after expiry")
	}
	if rebuilt.TrialConfig() != nil {
		t.Error("expected replayed trial config nil after expiry")
	}
}

// TestApplyContractExpiredEvent_ClearsTerminalState exercises the Apply path
// directly (no command guard) to pin that expiry clears trialConfig and
// cancelAtPeriodEnd as well, mirroring the ContractCancelledEvent block.
func TestApplyContractExpiredEvent_ClearsTerminalState(t *testing.T) {
	agg := newTestAggregate()
	meta := newTestMetadata()
	if err := agg.Create(newTestCommand(), meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	// newTestClock is 2026-01-01, so a future trial end.
	if err := agg.StartTrial(TrialConfiguration{TrialEndDate: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}, meta); err != nil {
		t.Fatalf("StartTrial failed: %v", err)
	}
	if agg.TrialConfig() == nil {
		t.Fatal("expected trial config after StartTrial")
	}

	if err := agg.Apply(&ContractExpiredEvent{
		ContractID: agg.ContractID(),
		ExpiredAt:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Apply ContractExpiredEvent failed: %v", err)
	}

	if agg.Status() != ContractStatusExpired {
		t.Errorf("expected status expired, got %s", agg.Status())
	}
	if agg.TrialConfig() != nil {
		t.Error("expected trial config cleared after expiry")
	}
	if agg.PendingPriceID() != nil {
		t.Error("expected PendingPriceID nil after expiry")
	}
	if agg.CancelAtPeriodEnd() {
		t.Error("expected cancelAtPeriodEnd false after expiry")
	}
}

// --- Issue #243 item 2: Create validates ContractType ---

func TestCreate_UnknownContractTypeRejected(t *testing.T) {
	cases := []struct {
		name string
		ct   ContractType
	}{
		{"empty", ContractType("")},
		{"typo", ContractType("subscriptionly")},
		{"unsupported", ContractType("hybrid")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agg := newTestAggregate()
			cmd := newTestCommand()
			cmd.ContractType = tc.ct
			assertCode(t, agg.Create(cmd, newTestMetadata()), shared.ErrCodeValidation)
		})
	}
}

func TestCreate_KnownContractTypesAccepted(t *testing.T) {
	for i, ct := range []ContractType{ContractTypeOneTime, ContractTypeSubscription, ContractTypeUsageBased} {
		agg := NewContractAggregate(shared.ContractID("ct-accept-"+string(ct)), newTestClock())
		cmd := newTestCommand()
		cmd.IdempotencyKey = cmd.IdempotencyKey + "-" + string(ct)
		cmd.ContractType = ct
		if err := agg.Create(cmd, newTestMetadata()); err != nil {
			t.Errorf("case %d (%s): unexpected error: %v", i, ct, err)
		}
	}
}
