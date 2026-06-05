package balance

import (
	"context"

	"github.com/contract-to-cash/core/domain/shared"
)

// Repository defines the persistence interface for credit entries.
type Repository interface {
	Save(ctx context.Context, entry *BalanceEntry) error
	FindByID(ctx context.Context, id shared.BalanceEntryID) (*BalanceEntry, error)

	// FindAvailable returns spendable credit entries for an account and currency,
	// excluding fully consumed and expired entries.
	//
	// Ordering contract (REQUIRED): entries MUST be returned in FIFO order, i.e.
	// ascending by creation time (oldest first). The billing pipeline consumes the
	// returned slice in order to apply credits FIFO and to honour expiry fairness
	// (see BillingService.applyBalances). A consumer-provided implementation that
	// returns entries in an unspecified order will silently break FIFO credit
	// consumption. The in-memory reference implementation sorts by createdAt asc.
	FindAvailable(ctx context.Context, accountID shared.AccountID, currency shared.Currency) ([]*BalanceEntry, error)
	GetBalance(ctx context.Context, accountID shared.AccountID, currency shared.Currency) (shared.Money, error)
	SaveApplication(ctx context.Context, app *BalanceApplication) error
	FindApplicationsByInvoice(ctx context.Context, invoiceID shared.InvoiceID) ([]*BalanceApplication, error)
	SaveRefund(ctx context.Context, refund *BalanceRefund) error

	// FindByAccountID returns all balance entries for an account and currency,
	// including fully consumed and expired entries, ordered by creation time.
	FindByAccountID(ctx context.Context, accountID shared.AccountID, currency shared.Currency) ([]*BalanceEntry, error)
}
