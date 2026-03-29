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

// BalanceRefund records the refund of a credit.
type BalanceRefund struct {
	ID             string
	BalanceEntryID shared.BalanceEntryID
	AccountID      shared.AccountID
	Amount         shared.Money
	RefundedAt     time.Time
}
