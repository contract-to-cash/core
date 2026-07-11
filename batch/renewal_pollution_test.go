package batch

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// isolatingRenewalRepo models a real database adapter's per-load isolation for
// the issue #151 (B1) pollution regression: FindByID materializes a FRESH
// aggregate from a snapshot (an independent instance, as a real adapter replays
// from history), while FindDueForRenewal hands back the shared stored pointers a
// batch iterates. Save fails on demand so the test can assert that a failed
// renewal never mutates the stored aggregate — the bug was that
// resolveInterval + RenewWithInterval ran on the FindDueForRenewal pointer
// OUTSIDE the transaction, leaving it advanced and carrying a dangling
// uncommitted renew event when the Save failed.
type isolatingRenewalRepo struct {
	mu        sync.Mutex
	clock     shared.Clock
	stored    map[shared.ContractID]*contract.ContractAggregate
	saveErr   error
	saveCalls int
}

func (r *isolatingRenewalRepo) Save(_ context.Context, _ *contract.ContractAggregate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveCalls++
	return r.saveErr
}

func (r *isolatingRenewalRepo) FindByID(_ context.Context, id shared.ContractID) (*contract.ContractAggregate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	agg, ok := r.stored[id]
	if !ok {
		return nil, fmt.Errorf("contract %s not found", id)
	}
	// Materialize an independent instance from a snapshot, mirroring a real
	// adapter's fresh replay so the caller cannot mutate the stored pointer.
	data, err := agg.MarshalSnapshot()
	if err != nil {
		return nil, err
	}
	clone := contract.NewContractAggregate(id, r.clock)
	if err := clone.LoadFromSnapshot(eventstore.Snapshot{State: data}); err != nil {
		return nil, err
	}
	return clone, nil
}

func (r *isolatingRenewalRepo) FindByAccountID(_ context.Context, _ shared.AccountID) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (r *isolatingRenewalRepo) FindExpiring(_ context.Context, _ time.Time) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (r *isolatingRenewalRepo) FindTrialsEndingBefore(_ context.Context, _ time.Time, _ int) ([]*contract.ContractAggregate, error) {
	return nil, nil
}

func (r *isolatingRenewalRepo) FindByIDAsOf(_ context.Context, _ shared.ContractID, _ time.Time) (*contract.ContractAggregate, error) {
	return nil, nil
}

func (r *isolatingRenewalRepo) FindDueForRenewal(_ context.Context, _ time.Time, _ int) ([]*contract.ContractAggregate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*contract.ContractAggregate, 0, len(r.stored))
	for _, agg := range r.stored {
		out = append(out, agg)
	}
	return out, nil
}

// TestContractRenewalProcessor_SaveFailure_DoesNotPolluteStoredAggregate is the
// issue #151 (B1) regression: when the renewal Save fails, the aggregate held by
// the repository (the one FindDueForRenewal returned) must remain untouched — no
// advanced period, no status change, and no dangling uncommitted renew event.
// Because the fix loads-mutates-saves a repository-loaded instance INSIDE the
// transaction, the failure only discards that throwaway instance.
func TestContractRenewalProcessor_SaveFailure_DoesNotPolluteStoredAggregate(t *testing.T) {
	agg := newActiveContract("c-pollute")
	id := agg.ContractID()

	// Capture the pre-renewal state of the stored aggregate.
	origPeriod := agg.CurrentPeriod()
	origStatus := agg.Status()
	origUncommitted := len(agg.UncommittedEvents())

	repo := &isolatingRenewalRepo{
		clock:   processorClock(),
		stored:  map[shared.ContractID]*contract.ContractAggregate{id: agg},
		saveErr: fmt.Errorf("simulated persistence failure"),
	}

	processor := NewContractRenewalProcessor(repo, nil, nil, processorClock(), nil, nil)

	result, err := processor.Process(context.Background(), BatchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Failed != 1 {
		t.Fatalf("Failed: got %d, want 1 (save must fail)", result.Failed)
	}
	if repo.saveCalls != 1 {
		t.Errorf("expected exactly 1 Save attempt, got %d", repo.saveCalls)
	}

	// The stored aggregate must be exactly as it was before the failed renewal.
	if agg.Status() != origStatus {
		t.Errorf("stored aggregate status polluted: got %s, want %s", agg.Status(), origStatus)
	}
	if agg.CurrentPeriod() != origPeriod {
		t.Errorf("stored aggregate period polluted: got %v, want %v", agg.CurrentPeriod(), origPeriod)
	}
	if got := len(agg.UncommittedEvents()); got != origUncommitted {
		t.Errorf("stored aggregate has dangling uncommitted events: got %d, want %d", got, origUncommitted)
	}
}
