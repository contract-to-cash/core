package invoice

import (
	"context"

	"github.com/contract-to-cash/core/domain/shared"
)

// CreditNoteRepository defines the persistence interface for credit notes.
type CreditNoteRepository interface {
	Save(ctx context.Context, cn *CreditNote) error
	FindByID(ctx context.Context, id shared.CreditNoteID) (*CreditNote, error)
	FindByInvoiceID(ctx context.Context, invoiceID shared.InvoiceID) ([]*CreditNote, error)
	FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*CreditNote, error)
	FindByContractID(ctx context.Context, contractID shared.ContractID) ([]*CreditNote, error)
	FindByStatus(ctx context.Context, status CreditNoteStatus) ([]*CreditNote, error)
}
