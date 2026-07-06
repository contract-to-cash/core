package invoice

import (
	"context"

	"github.com/contract-to-cash/core/domain/shared"
)

// CreditNoteRepository defines the persistence interface for credit notes.
type CreditNoteRepository interface {
	// Save persists a credit note.
	//
	// Concurrency contract (issue #147): implementations MUST protect the
	// load → state-check → save sequence performed by callers such as
	// CreditNoteService.ApplyCreditNote / RefundCreditNote against lost updates.
	// An issued credit note loaded by two callers must not be Apply()'d by one
	// and Refund()'d by the other with both saves succeeding — that would credit
	// the account AND refund the gateway while persisting only one outcome.
	// Satisfy this in ONE of two ways:
	//
	//   1. Optimistic locking (recommended): compare the credit note's
	//      LoadedVersion() against the stored version and return an error
	//      matching errors.Is(err, tx.ErrVersionConflict) when they differ. On
	//      success, persist Version() as the new stored version. This lets the
	//      loser of two concurrent transitions be deterministically rejected;
	//      on retry it re-reads the now-terminal credit note and is rejected
	//      with invalid_state_transition.
	//   2. Read serialization: take a row lock in FindByID (SELECT ... FOR
	//      UPDATE) or run at SERIALIZABLE isolation so a concurrent transition
	//      blocks until the winner commits and then observes the new state.
	//
	// An implementation that does neither (unconditional last-writer-wins
	// upsert) allows the Apply-vs-Refund race above. See
	// infrastructure/inmemory for a reference optimistic-locking implementation
	// and contract-to-cash/adapters#30 for the adapter-side tracking issue.
	//
	// The first save of a given ID has no stored version to compare against and
	// always succeeds, so callers constructing a fresh credit note
	// (LoadedVersion 0) are unaffected.
	Save(ctx context.Context, cn *CreditNote) error
	FindByID(ctx context.Context, id shared.CreditNoteID) (*CreditNote, error)
	FindByInvoiceID(ctx context.Context, invoiceID shared.InvoiceID) ([]*CreditNote, error)
	FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*CreditNote, error)
	FindByContractID(ctx context.Context, contractID shared.ContractID) ([]*CreditNote, error)
	FindByStatus(ctx context.Context, status CreditNoteStatus) ([]*CreditNote, error)
}
