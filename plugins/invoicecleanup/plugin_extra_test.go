package invoicecleanup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

func newCancelledContract(t *testing.T, contractID shared.ContractID, clock shared.Clock) *contract.ContractAggregate {
	t.Helper()
	agg := contract.NewContractAggregate(contractID, clock)
	if err := agg.Create(contract.CreateContractCommand{
		AccountID:    shared.NewAccountID(),
		ContractType: contract.ContractTypeSubscription,
		BillingCycle: contract.BillingCycleMonthly,
		Price:        jpy(10000),
		BasePrice:    jpy(10000),
	}, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := agg.Activate(eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := agg.Cancel("test", eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	return agg
}

func TestInvoiceCleanupPlugin_Metadata(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	p := NewInvoiceCleanupPlugin(inmemory.NewInMemoryInvoiceRepository(clock))
	if p.Name() != "invoice-cleanup" {
		t.Errorf("Name() = %q", p.Name())
	}
	if p.Version() != "1.0.0" {
		t.Errorf("Version() = %q", p.Version())
	}
	if p.Priority() != plugin.PriorityNormal {
		t.Errorf("Priority() = %d, want %d", p.Priority(), plugin.PriorityNormal)
	}
}

func TestInvoiceCleanupPlugin_Initialize_PriorityOverride(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	p := NewInvoiceCleanupPlugin(inmemory.NewInMemoryInvoiceRepository(clock))

	if err := p.Initialize(context.Background(), plugin.Config{"priority": 7}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Priority() != 7 {
		t.Errorf("expected priority 7, got %d", p.Priority())
	}

	// Non-int is ignored.
	if err := p.Initialize(context.Background(), plugin.Config{"priority": "nope"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Priority() != 7 {
		t.Errorf("expected priority to stay 7 after non-int config, got %d", p.Priority())
	}
}

func TestInvoiceCleanupPlugin_Shutdown(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	p := NewInvoiceCleanupPlugin(inmemory.NewInMemoryInvoiceRepository(clock))
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("unexpected error from Shutdown: %v", err)
	}
}

func TestInvoiceCleanupPlugin_LeavesNonDraftFinalizedUntouched(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	repo := inmemory.NewInMemoryInvoiceRepository(clock)
	ctx := context.Background()

	contractID := shared.NewContractID()
	accountID := shared.NewAccountID()

	// An overdue invoice must be left untouched (requires human judgment).
	overdue, err := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(3000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusOverdue),
	)
	if err != nil {
		t.Fatalf("create overdue invoice: %v", err)
	}
	_ = repo.Save(ctx, overdue)

	agg := newCancelledContract(t, contractID, clock)
	p := NewInvoiceCleanupPlugin(repo)
	if err := p.OnContractCancel(plugin.NewContext(ctx), agg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := repo.FindByID(ctx, overdue.ID())
	if got.Status() != invoice.InvoiceStatusOverdue {
		t.Errorf("expected overdue invoice to remain overdue, got %s", got.Status())
	}
}

// errFindRepo returns an error from FindUnpaidByContract; other methods are unused.
type errFindRepo struct {
	invoice.Repository
	err error
}

func (r *errFindRepo) FindUnpaidByContract(ctx context.Context, contractID shared.ContractID) ([]*invoice.Invoice, error) {
	return nil, r.err
}

func TestInvoiceCleanupPlugin_FindError(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	sentinel := errors.New("db down")
	repo := &errFindRepo{err: sentinel}

	agg := newCancelledContract(t, shared.NewContractID(), clock)
	p := NewInvoiceCleanupPlugin(repo)

	err := p.OnContractCancel(plugin.NewContext(context.Background()), agg)
	if err == nil {
		t.Fatal("expected error from OnContractCancel, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected wrapped sentinel error, got %v", err)
	}
}

// errSaveRepo wraps a real repo but fails on Save, to exercise the save error path.
type errSaveRepo struct {
	*inmemory.InMemoryInvoiceRepository
	err error
}

func (r *errSaveRepo) Save(ctx context.Context, inv *invoice.Invoice) error {
	return r.err
}

func TestInvoiceCleanupPlugin_SaveError(t *testing.T) {
	clock := shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
	ctx := context.Background()
	inner := inmemory.NewInMemoryInvoiceRepository(clock)

	contractID := shared.NewContractID()
	draft, err := invoice.NewInvoice(
		shared.NewInvoiceID(), shared.NewAccountID(), contractID,
		jpy(10000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("create draft invoice: %v", err)
	}
	_ = inner.Save(ctx, draft)

	sentinel := errors.New("save failed")
	repo := &errSaveRepo{InMemoryInvoiceRepository: inner, err: sentinel}

	agg := newCancelledContract(t, contractID, clock)
	p := NewInvoiceCleanupPlugin(repo)

	err = p.OnContractCancel(plugin.NewContext(ctx), agg)
	if err == nil {
		t.Fatal("expected error from Save, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected wrapped sentinel error, got %v", err)
	}
}
