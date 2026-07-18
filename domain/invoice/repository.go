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
	//
	// Per-period uniqueness contract (issue #149): implementations MUST also
	// prevent two DISTINCT non-voided invoices from existing for the same
	// (contract_id, billing_period). BillingService.GenerateInvoice /
	// RegenerateInvoice re-check for a duplicate inside their tx.Run closure
	// through the transaction-scoped repo, but a check-then-insert of a NEW
	// invoice cannot raise an optimistic-lock conflict (there is no prior
	// version to compare), so under READ COMMITTED two concurrent
	// GenerateInvoice(contractID, samePeriod) calls can both pass the re-check
	// and both insert — producing two billable invoices for one period and a
	// downstream double charge. The storage backend must close that window:
	//
	//   - Postgres / MySQL (recommended): a PARTIAL UNIQUE INDEX on
	//     (contract_id, period_start, period_end) restricted to non-voided,
	//     non-proration invoices, e.g.
	//       CREATE UNIQUE INDEX ux_invoice_period
	//         ON invoices (contract_id, period_start, period_end)
	//         WHERE status <> 'voided'
	//           AND coalesce(metadata->>'invoice_type','') <> 'proration';
	//   - Read serialization: a SELECT ... FOR UPDATE / SERIALIZABLE guard that
	//     serializes the duplicate re-check against the concurrent insert.
	//
	// PRORATION and VOIDED invoices are EXEMPT: proration adjustments (see
	// Invoice.IsProration / InvoiceTypeProration) intentionally coexist with the
	// period's regular invoice, and void-and-recreate leaves the voided original
	// alongside its replacement. The constraint therefore ranges over non-voided,
	// non-proration invoices only. A regeneration replacement
	// (InvoiceTypeRegeneration) IS a regular period invoice and participates.
	// The exemption predicate is codified as
	// Invoice.ParticipatesInPeriodUniqueness (not voided, not proration, and a
	// non-zero billing period); implementations SHOULD delegate to it — the
	// infrastructure/inmemory reference and the BillingService duplicate-invoice
	// guards do — so the constraint's scope never drifts across layers
	// (issue #232).
	//
	// When the constraint fires, Save MUST return a shared.DomainError with code
	// shared.ErrCodeConflict so the losing GenerateInvoice caller surfaces a clean
	// "invoice already exists for this billing period" conflict rather than a raw
	// driver error. See infrastructure/inmemory for a reference implementation
	// that mirrors the partial unique index above.
	Save(ctx context.Context, invoice *Invoice) error

	// FindByID loads an invoice by its ID.
	//
	// Not-found convention (issue #197): implementations MUST return an error for
	// a missing invoice — a shared.DomainError with code shared.ErrCodeNotFound —
	// and MUST NOT return (nil, nil). Callers (PaymentService.ProcessPayment,
	// CreditNoteService) defend against a nil result regardless, but the typed
	// error is the contract. The infrastructure/inmemory implementation is the
	// reference.
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
