package balance

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// BalanceApplication records the application of a credit to an invoice.
type BalanceApplication struct {
	ID             string
	BalanceEntryID shared.BalanceEntryID
	InvoiceID      shared.InvoiceID
	Amount         shared.Money
	AppliedAt      time.Time
}

// BalanceRefund records the refund (restoration) of a consumed credit back to
// its entry — e.g. when the invoice that consumed the credit is voided
// (issue #184). It is the audit-trail counterpart of BalanceApplication.
type BalanceRefund struct {
	ID             string
	BalanceEntryID shared.BalanceEntryID
	AccountID      shared.AccountID
	Amount         shared.Money
	RefundedAt     time.Time
	// InvoiceID is the voided invoice whose consumed credit this refund restores.
	// It makes void-triggered restoration idempotent: a double void / retry finds
	// the existing refund for the invoice and skips re-restoring. Empty for
	// refunds not tied to an invoice.
	InvoiceID shared.InvoiceID
	// ApplicationID links this refund to the specific BalanceApplication it
	// reverses, so restoration is idempotent at per-application granularity (an
	// invoice can consume several distinct credit entries).
	ApplicationID string
}
