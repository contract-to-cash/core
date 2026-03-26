package credit

import (
	"context"

	"github.com/contract-to-cash/core/domain/shared"
)

// Repository defines the persistence interface for credit entries.
type Repository interface {
	Save(ctx context.Context, entry *CreditEntry) error
	FindByID(ctx context.Context, id shared.CreditEntryID) (*CreditEntry, error)
	FindAvailable(ctx context.Context, accountID shared.AccountID, currency shared.Currency) ([]*CreditEntry, error)
	GetBalance(ctx context.Context, accountID shared.AccountID, currency shared.Currency) (shared.Money, error)
	SaveApplication(ctx context.Context, app *CreditApplication) error
	FindApplicationsByInvoice(ctx context.Context, invoiceID shared.InvoiceID) ([]*CreditApplication, error)
	SaveRefund(ctx context.Context, refund *CreditRefund) error
}
