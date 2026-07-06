package integration

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// isolatingCreditNoteRepo mirrors a real RDBMS's per-transaction snapshot
// isolation for the issue #151 (M4) regression: every FindBy* returns a fresh
// snapshot-round-tripped clone and Save stores an independent copy, so
// concurrent IssueCreditNote callers never share a single *CreditNote pointer
// whose status/version would be mutated out from under them (this also keeps the
// -race detector clean). The inner in-memory repo enforces the optimistic-lock
// version check (issue #147), so the losing Save still surfaces
// tx.ErrVersionConflict, driving the service's RetryOnConflict path.
type isolatingCreditNoteRepo struct {
	inner invoice.CreditNoteRepository
}

func cloneCreditNote(cn *invoice.CreditNote) (*invoice.CreditNote, error) {
	return invoice.CreditNoteFromSnapshot(cn.ToSnapshot())
}

func (r *isolatingCreditNoteRepo) Save(ctx context.Context, cn *invoice.CreditNote) error {
	// Store the caller's pointer directly so the optimistic-lock comparison in
	// the inner repo sees the entity's real LoadedVersion (a snapshot clone would
	// reset loadedVersion to version and spuriously conflict). Isolation between
	// concurrent readers is provided by FindByID, which hands out clones.
	return r.inner.Save(ctx, cn)
}

func (r *isolatingCreditNoteRepo) FindByID(ctx context.Context, id shared.CreditNoteID) (*invoice.CreditNote, error) {
	cn, err := r.inner.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return cloneCreditNote(cn)
}

func (r *isolatingCreditNoteRepo) cloneList(cns []*invoice.CreditNote) ([]*invoice.CreditNote, error) {
	out := make([]*invoice.CreditNote, 0, len(cns))
	for _, cn := range cns {
		clone, err := cloneCreditNote(cn)
		if err != nil {
			return nil, err
		}
		out = append(out, clone)
	}
	return out, nil
}

func (r *isolatingCreditNoteRepo) FindByInvoiceID(ctx context.Context, id shared.InvoiceID) ([]*invoice.CreditNote, error) {
	cns, err := r.inner.FindByInvoiceID(ctx, id)
	if err != nil {
		return nil, err
	}
	return r.cloneList(cns)
}

func (r *isolatingCreditNoteRepo) FindByAccountID(ctx context.Context, id shared.AccountID) ([]*invoice.CreditNote, error) {
	cns, err := r.inner.FindByAccountID(ctx, id)
	if err != nil {
		return nil, err
	}
	return r.cloneList(cns)
}

func (r *isolatingCreditNoteRepo) FindByContractID(ctx context.Context, id shared.ContractID) ([]*invoice.CreditNote, error) {
	cns, err := r.inner.FindByContractID(ctx, id)
	if err != nil {
		return nil, err
	}
	return r.cloneList(cns)
}

func (r *isolatingCreditNoteRepo) FindByStatus(ctx context.Context, status invoice.CreditNoteStatus) ([]*invoice.CreditNote, error) {
	cns, err := r.inner.FindByStatus(ctx, status)
	if err != nil {
		return nil, err
	}
	return r.cloneList(cns)
}

// countingCreditNoteIssuedHook counts OnCreditNoteIssued invocations to prove the
// hook fires exactly once even under concurrent IssueCreditNote calls.
type countingCreditNoteIssuedHook struct {
	mu    sync.Mutex
	count int
}

func (h *countingCreditNoteIssuedHook) Name() string    { return "counting-cn-issued" }
func (h *countingCreditNoteIssuedHook) Version() string { return "1.0.0" }
func (h *countingCreditNoteIssuedHook) Initialize(_ context.Context, _ plugin.Config) error {
	return nil
}
func (h *countingCreditNoteIssuedHook) Shutdown(_ context.Context) error { return nil }
func (h *countingCreditNoteIssuedHook) Priority() int                    { return 500 }
func (h *countingCreditNoteIssuedHook) OnCreditNoteIssued(_ *plugin.Context, _ *invoice.CreditNote) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	return nil
}

func (h *countingCreditNoteIssuedHook) calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// TestIssueCreditNote_ConcurrentIssuance_HookFiresOnce is the issue #151 (M4)
// regression. Two goroutines race to issue the SAME draft credit note. Because
// IssueCreditNote now loads-checks-transitions-saves inside a transaction with
// RetryOnConflict, exactly one goroutine wins (persists the issued note and fires
// OnCreditNoteIssued once) and the other gets a clean domain error — either a
// version conflict that, on retry, re-reads the now-issued note and is rejected
// with invalid_state_transition, or an immediate invalid_state_transition. Before
// the fix both goroutines could load the draft, both transition, and both fire
// the hook, double-posting the downstream ledger.
func TestIssueCreditNote_ConcurrentIssuance_HookFiresOnce(t *testing.T) {
	ctx := context.Background()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	creditNoteRepo := &isolatingCreditNoteRepo{inner: inmemory.NewInMemoryCreditNoteRepository()}

	// Seed an issued 1000 invoice (credit-note eligible).
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
		moneyJPY(1000), moneyJPY(0), moneyJPY(0),
		invoice.WithStatus(invoice.InvoiceStatusIssued),
	)
	if err != nil {
		t.Fatalf("NewInvoice failed: %v", err)
	}
	if err := invoiceRepo.Save(ctx, inv); err != nil {
		t.Fatalf("seeding invoice failed: %v", err)
	}

	hook := &countingCreditNoteIssuedHook{}
	reg := plugin.NewRegistry()
	if regErr := reg.Register(hook); regErr != nil {
		t.Fatalf("registry.Register: %v", regErr)
	}

	svc := service.NewCreditNoteService(invoiceRepo, creditNoteRepo, reg, clock)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Partial credit", moneyJPY(600), big.NewRat(0, 1), moneyJPY(0)),
	}
	cn, err := svc.CreateCreditNote(ctx, inv.ID(), invoice.CreditNoteReasonOther, items, "")
	if err != nil {
		t.Fatalf("CreateCreditNote failed: %v", err)
	}

	const n = 2
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	var failures []error

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, ierr := svc.IssueCreditNote(ctx, cn.ID())
			mu.Lock()
			defer mu.Unlock()
			if ierr == nil {
				successes++
			} else {
				failures = append(failures, ierr)
			}
		}()
	}
	close(start)
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful issuance, got %d", successes)
	}
	if len(failures) != n-1 {
		t.Fatalf("expected %d rejected issuance(s), got %d", n-1, len(failures))
	}
	for _, ferr := range failures {
		var domainErr *shared.DomainError
		if !errors.As(ferr, &domainErr) {
			t.Errorf("expected a clean DomainError for the loser, got %T: %v", ferr, ferr)
			continue
		}
		if domainErr.Code != shared.ErrCodeInvalidStateTransition {
			t.Errorf("expected invalid_state_transition for the loser, got %s: %v", domainErr.Code, ferr)
		}
	}

	// The hook must have fired exactly once (no double ledger posting).
	if got := hook.calls(); got != 1 {
		t.Errorf("expected OnCreditNoteIssued to fire exactly once, got %d", got)
	}

	// The persisted credit note must be issued exactly once.
	persisted, err := creditNoteRepo.FindByID(ctx, cn.ID())
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if persisted.Status() != invoice.CreditNoteStatusIssued {
		t.Errorf("expected persisted credit note to be issued, got %s", persisted.Status())
	}
}
