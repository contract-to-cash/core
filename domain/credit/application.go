package credit

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// CreditApplication records the application of a credit to an invoice.
type CreditApplication struct {
	ID            string
	CreditEntryID shared.CreditEntryID
	InvoiceID     shared.InvoiceID
	Amount        shared.Money
	AppliedAt     time.Time
}

// CreditRefund records the refund of a credit.
type CreditRefund struct {
	ID            string
	CreditEntryID shared.CreditEntryID
	AccountID     shared.AccountID
	Amount        shared.Money
	RefundedAt    time.Time
}
