package balance

import (
	"context"

	"github.com/contract-to-cash/core/domain/shared"
)

// Repository defines the persistence interface for credit entries.
type Repository interface {
	Save(ctx context.Context, entry *BalanceEntry) error
	FindByID(ctx context.Context, id shared.BalanceEntryID) (*BalanceEntry, error)
	FindAvailable(ctx context.Context, accountID shared.AccountID, currency shared.Currency) ([]*BalanceEntry, error)
	GetBalance(ctx context.Context, accountID shared.AccountID, currency shared.Currency) (shared.Money, error)
	SaveApplication(ctx context.Context, app *BalanceApplication) error
	FindApplicationsByInvoice(ctx context.Context, invoiceID shared.InvoiceID) ([]*BalanceApplication, error)
	SaveRefund(ctx context.Context, refund *BalanceRefund) error

	// FindByAccountID returns all balance entries for an account and currency,
	// including fully consumed and expired entries, ordered by creation time.
	FindByAccountID(ctx context.Context, accountID shared.AccountID, currency shared.Currency) ([]*BalanceEntry, error)
}
