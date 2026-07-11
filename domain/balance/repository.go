package balance

import (
	"context"
	"time"

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

	// FindRefundsByInvoice returns all credit refunds recorded against an invoice.
	// The void-restoration flow (issue #184) uses it to skip applications whose
	// consumed credit was already restored, making a double void / transaction
	// retry idempotent. Results ordering is unspecified.
	FindRefundsByInvoice(ctx context.Context, invoiceID shared.InvoiceID) ([]*BalanceRefund, error)

	// FindByAccountID returns all balance entries for an account and currency,
	// including fully consumed and expired entries, ordered by creation time.
	FindByAccountID(ctx context.Context, accountID shared.AccountID, currency shared.Currency) ([]*BalanceEntry, error)

	// FindExpired returns entries whose expiry has passed as of `asOf` and
	// whose remaining amount is still non-zero — i.e. expired credit that has
	// not yet been forfeited by MarkExpired. Entries without an expiry and
	// fully consumed entries are excluded. Results are ordered by creation
	// time ascending for deterministic batch processing.
	//
	// This is the scan feeding batch.BalanceExpirationProcessor (issue #159),
	// the counterpart of contract.Repository.FindDueForRenewal for the credit
	// ledger.
	//
	// limit bounds the number of entries returned (issue #197): a positive limit
	// returns at most that many (oldest-created first, so repeated batch runs
	// drain the expired backlog deterministically); 0 (or negative) means "no
	// limit" and preserves the original unbounded behaviour. The expiration batch
	// threads BatchOptions.Limit here so a run does not load every expired row at
	// once.
	FindExpired(ctx context.Context, asOf time.Time, limit int) ([]*BalanceEntry, error)
}
