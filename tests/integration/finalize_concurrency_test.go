package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// countingInvoiceIssuedPlugin counts OnInvoiceIssued invocations in a
// thread-safe way so a concurrent test can assert the hook fired exactly once.
type countingInvoiceIssuedPlugin struct {
	mu    sync.Mutex
	calls int
}

func (p *countingInvoiceIssuedPlugin) Name() string    { return "counting-invoice-issued" }
func (p *countingInvoiceIssuedPlugin) Version() string { return "1.0.0" }
func (p *countingInvoiceIssuedPlugin) Initialize(_ context.Context, _ plugin.Config) error {
	return nil
}
func (p *countingInvoiceIssuedPlugin) Shutdown(_ context.Context) error { return nil }
func (p *countingInvoiceIssuedPlugin) Priority() int                    { return 500 }

func (p *countingInvoiceIssuedPlugin) OnInvoiceIssued(_ *plugin.Context, _ *invoice.Invoice) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return nil
}

func (p *countingInvoiceIssuedPlugin) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestFinalizeInvoice_ConcurrentFinalize_SingleWinner_Integration is the #130
// end-to-end regression test. Many goroutines finalize the same draft invoice
// at once through BillingService.FinalizeInvoice, backed by the in-memory
// invoice repository, which enforces the optimistic-locking contract AND hands
// each read an independent snapshot copy (issue #152), as a real RDBMS
// would. Exactly one call must finalize the invoice and fire
// OnInvoiceIssued; every other call must be rejected with
// invalid_state_transition (the RetryOnConflict loser re-reads the finalized
// row). Without optimistic locking every caller would succeed and the metrics
// hook would fire N times.
func TestFinalizeInvoice_ConcurrentFinalize_SingleWinner_Integration(t *testing.T) {
	ctx := context.Background()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}

	inner := inmemory.NewInMemoryInvoiceRepository(clock)
	// The in-memory repository isolates reads natively (issue #152).
	isolated := inner

	spy := &countingInvoiceIssuedPlugin{}
	registry := plugin.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatalf("failed to register spy plugin: %v", err)
	}

	// Seed a draft invoice directly in the store.
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
		moneyJPY(10000), moneyJPY(0), moneyJPY(0),
	)
	if err != nil {
		t.Fatalf("NewInvoice failed: %v", err)
	}
	if err := inner.Save(ctx, inv); err != nil {
		t.Fatalf("seeding draft invoice failed: %v", err)
	}

	eventStore := inmemory.NewInMemoryEventStore(clock)
	svc := service.NewBillingService(
		inmemory.NewInMemoryContractRepository(eventStore, clock),
		isolated,
		inmemory.NewInMemoryUsageRepository(),
		balance.BalanceConfig{},
		inmemory.NewInMemoryPriceRepository(),
		inmemory.NewInMemoryProductRepository(),
		registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
	)

	const n = 8
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
			_, ferr := svc.FinalizeInvoice(ctx, inv.ID())
			mu.Lock()
			defer mu.Unlock()
			if ferr == nil {
				successes++
			} else {
				failures = append(failures, ferr)
			}
		}()
	}
	close(start)
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful finalize, got %d", successes)
	}
	if len(failures) != n-1 {
		t.Fatalf("expected %d rejected finalizes, got %d", n-1, len(failures))
	}
	for _, ferr := range failures {
		var domainErr *shared.DomainError
		if !errors.As(ferr, &domainErr) || domainErr.Code != shared.ErrCodeInvalidStateTransition {
			t.Errorf("expected invalid_state_transition for a losing finalize, got: %v", ferr)
		}
	}
	if got := spy.count(); got != 1 {
		t.Errorf("OnInvoiceIssued fired %d times, want exactly 1", got)
	}

	final, err := inner.FindByID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("final FindByID failed: %v", err)
	}
	if final.Status() != invoice.InvoiceStatusFinalized {
		t.Errorf("final status: got %s, want finalized", final.Status())
	}
}
