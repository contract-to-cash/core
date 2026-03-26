package credit

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// CreditReason describes why a credit was issued.
type CreditReason string

const (
	CreditReasonProration        CreditReason = "proration"
	CreditReasonCancellation     CreditReason = "cancellation"
	CreditReasonManualAdjustment CreditReason = "manual_adjustment"
	CreditReasonRefundConversion CreditReason = "refund_conversion"
	CreditReasonGoodwill         CreditReason = "goodwill"
)

// CreditEntry represents a credit issued to an account.
type CreditEntry struct {
	id              shared.CreditEntryID
	accountID       shared.AccountID
	originalAmount  shared.Money
	remainingAmount shared.Money
	reason          CreditReason
	sourceType      string
	sourceID        string
	description     string
	expiresAt       *time.Time
	createdAt       time.Time
}

// NewCreditEntry creates a new CreditEntry with the given parameters.
// createdAt should be provided by the caller via Clock.Now().
func NewCreditEntry(accountID shared.AccountID, amount shared.Money, reason CreditReason, createdAt time.Time) *CreditEntry {
	return &CreditEntry{
		id:              shared.NewCreditEntryID(),
		accountID:       accountID,
		originalAmount:  amount,
		remainingAmount: amount,
		reason:          reason,
		createdAt:       createdAt,
	}
}

// ID returns the credit entry ID.
func (e *CreditEntry) ID() shared.CreditEntryID { return e.id }

// AccountID returns the account ID.
func (e *CreditEntry) AccountID() shared.AccountID { return e.accountID }

// OriginalAmount returns the original credit amount.
func (e *CreditEntry) OriginalAmount() shared.Money { return e.originalAmount }

// RemainingAmount returns the remaining credit amount.
func (e *CreditEntry) RemainingAmount() shared.Money { return e.remainingAmount }

// Reason returns the credit reason.
func (e *CreditEntry) Reason() CreditReason { return e.reason }

// SourceType returns the source type.
func (e *CreditEntry) SourceType() string { return e.sourceType }

// SourceID returns the source ID.
func (e *CreditEntry) SourceID() string { return e.sourceID }

// Description returns the description.
func (e *CreditEntry) Description() string { return e.description }

// ExpiresAt returns the expiration time, or nil if no expiration.
func (e *CreditEntry) ExpiresAt() *time.Time { return e.expiresAt }

// CreatedAt returns the creation time.
func (e *CreditEntry) CreatedAt() time.Time { return e.createdAt }

// IsExpired returns true if the credit has expired as of the given time.
func (e *CreditEntry) IsExpired(now time.Time) bool {
	if e.expiresAt == nil {
		return false
	}
	return now.After(*e.expiresAt)
}

// IsFullyConsumed returns true if the remaining amount is zero.
func (e *CreditEntry) IsFullyConsumed() bool {
	return e.remainingAmount.IsZero()
}

// Consume reduces the remaining amount by the given amount.
// Returns the actually consumed amount (may be less than requested if insufficient balance).
func (e *CreditEntry) Consume(amount shared.Money) (shared.Money, error) {
	available := e.remainingAmount
	consumed, err := available.Min(amount)
	if err != nil {
		return shared.Money{}, err
	}
	e.remainingAmount, err = e.remainingAmount.Subtract(consumed)
	if err != nil {
		return shared.Money{}, err
	}
	return consumed, nil
}
