package invoice

import (
	"context"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Repository defines the persistence interface for invoices.
type Repository interface {
	// Save persists an invoice.
	//
	// Concurrency contract (issue #130): implementations MUST protect the
	// load → state-check → save sequence performed by callers such as
	// BillingService.FinalizeInvoice against lost updates. Satisfy this in ONE
	// of two ways:
	//
	//   1. Optimistic locking (recommended): compare the invoice's
	//      LoadedVersion() against the stored version and return an error
	//      matching errors.Is(err, tx.ErrVersionConflict) when they differ.
	//      On success, persist Version() as the new stored version. This lets
	//      FinalizeInvoice retry the loser, which then re-reads the finalized
	//      row and is rejected with invalid_state_transition.
	//   2. Read serialization: take a row lock in FindByID (SELECT ... FOR
	//      UPDATE) or run at SERIALIZABLE isolation so a concurrent
	//      FinalizeInvoice blocks until the winner commits and then observes
	//      the finalized state.
	//
	// An implementation that does neither (unconditional last-writer-wins
	// upsert) allows two concurrent FinalizeInvoice calls to both succeed and
	// fire OnInvoiceIssued twice. See infrastructure/inmemory for a reference
	// optimistic-locking implementation and contract-to-cash/adapters#12 for
	// the adapter-side tracking issue.
	Save(ctx context.Context, invoice *Invoice) error
	FindByID(ctx context.Context, id shared.InvoiceID) (*Invoice, error)
	FindByContractID(ctx context.Context, contractID shared.ContractID) ([]*Invoice, error)
	FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*Invoice, error)
	FindOverdue(ctx context.Context) ([]*Invoice, error)
	FindByStatus(ctx context.Context, status InvoiceStatus) ([]*Invoice, error)
	FindByIDAsOf(ctx context.Context, id shared.InvoiceID, asOf time.Time) (*Invoice, error)
	FindByContractAndStatus(ctx context.Context, contractID shared.ContractID, status InvoiceStatus) ([]*Invoice, error)
	FindByContractAndPeriod(ctx context.Context, contractID shared.ContractID, period shared.DateRange) ([]*Invoice, error)
	FindUnpaidByContract(ctx context.Context, contractID shared.ContractID) ([]*Invoice, error)
}
