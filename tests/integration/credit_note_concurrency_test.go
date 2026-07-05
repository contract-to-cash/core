package integration

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// serializingTxManager models the pessimistic invoice row lock a real database
// adapter takes in CreateCreditNote (SELECT ... FOR UPDATE, PR adapters#21): it
// serializes transactions so that a concurrent second caller only proceeds after
// the first has committed. The in-memory NoopTxManager runs closures inline with
// no locking, so this wrapper stands in for the adapter's lock in the concurrency
// regression test — the same role isolatingInvoiceRepo plays for the finalize
// optimistic-lock test (#130).
type serializingTxManager struct {
	mu    sync.Mutex
	inner tx.TxManager
}

func (m *serializingTxManager) RunInTx(ctx context.Context, fn func(context.Context, tx.Repos) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inner.RunInTx(ctx, fn)
}

// TestCreateCreditNote_ConcurrentIssuance_CumulativeCapEnforced is the #124
// regression test. Two goroutines each issue a 600 credit note against the same
// 1000 invoice. Serialized by the invoice row lock (modeled by
// serializingTxManager), the winner commits 600 and the loser then re-reads the
// now-600 aggregate, computes cumulative 1200 > 1000, and is rejected with
// business_rule_violation. Without the transactional cumulative cap both would
// pass their check-then-act and 1200 would be credited against a 1000 invoice.
func TestCreateCreditNote_ConcurrentIssuance_CumulativeCapEnforced(t *testing.T) {
	ctx := context.Background()
	clock := shared.FixedClock{FixedTime: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}

	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	creditNoteRepo := inmemory.NewInMemoryCreditNoteRepository()

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

	txMgr := &serializingTxManager{
		inner: tx.NewNoopTxManager(tx.Repos{
			Invoices:    invoiceRepo,
			CreditNotes: creditNoteRepo,
		}),
	}

	svc := service.NewCreditNoteService(
		invoiceRepo,
		creditNoteRepo,
		plugin.NewRegistry(),
		clock,
		service.WithCreditNoteTxManager(txMgr),
	)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Partial credit", moneyJPY(600), big.NewRat(0, 1), moneyJPY(0)),
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
			_, cerr := svc.CreateCreditNote(ctx, inv.ID(), invoice.CreditNoteReasonOther, items, "")
			mu.Lock()
			defer mu.Unlock()
			if cerr == nil {
				successes++
			} else {
				failures = append(failures, cerr)
			}
		}()
	}
	close(start)
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful credit note, got %d", successes)
	}
	if len(failures) != n-1 {
		t.Fatalf("expected %d rejected credit notes, got %d", n-1, len(failures))
	}
	for _, cerr := range failures {
		var domainErr *shared.DomainError
		if !errors.As(cerr, &domainErr) || domainErr.Code != shared.ErrCodeBusinessRule {
			t.Errorf("expected business_rule_violation for a losing issuance, got: %v", cerr)
		}
	}

	// Only the winner's 600 credit note must be persisted — total credited 600, not 1200.
	persisted, err := creditNoteRepo.FindByInvoiceID(ctx, inv.ID())
	if err != nil {
		t.Fatalf("FindByInvoiceID failed: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("expected exactly 1 persisted credit note, got %d", len(persisted))
	}
	if got := persisted[0].Total(); got.Amount().Cmp(moneyJPY(600).Amount()) != 0 {
		t.Errorf("persisted credit note total: got %s, want 600", got.Amount().RatString())
	}
}
