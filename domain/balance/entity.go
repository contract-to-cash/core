package balance

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// BalanceReason describes why a credit was issued.
type BalanceReason string

const (
	BalanceReasonProration        BalanceReason = "proration"
	BalanceReasonCancellation     BalanceReason = "cancellation"
	BalanceReasonManualAdjustment BalanceReason = "manual_adjustment"
	BalanceReasonRefundConversion BalanceReason = "refund_conversion"
	BalanceReasonGoodwill         BalanceReason = "goodwill"
)

// BalanceSourceType describes the origin of a credit entry.
type BalanceSourceType string

const (
	BalanceSourceTypeProration        BalanceSourceType = "proration"
	BalanceSourceTypeManual           BalanceSourceType = "manual"
	BalanceSourceTypeRefundConversion BalanceSourceType = "refund_conversion"
)

// BalanceEntry represents a credit issued to an account.
type BalanceEntry struct {
	id              shared.BalanceEntryID
	accountID       shared.AccountID
	originalAmount  shared.Money
	remainingAmount shared.Money
	reason          BalanceReason
	sourceType      BalanceSourceType
	sourceID        string
	description     string
	expiresAt       *time.Time
	createdAt       time.Time
	version         int // incremented on each Consume(); used for optimistic locking
	loadedVersion   int // version at load time; compared on save for conflict detection
}

// NewBalanceEntry creates a new BalanceEntry with the given parameters.
// createdAt should be provided by the caller via Clock.Now().
//
// The amount must not be negative. A negative credit is actively dangerous: it
// would be applied as a debit — during invoice generation
// RemainingAmount().Min(remaining) would return a negative amount and inflate
// the invoice's amount due — so it is rejected at construction (issue #148).
// Zero is permitted: a zero-amount entry is inert (nothing is ever consumed from
// it, and it contributes nothing to available balance), and Consume relies on
// being able to represent a fully-consumed / zero-balance entry. Persistence
// adapters that rebuild an entry from a stored row use FromSnapshot, which
// deliberately bypasses this guard so replay of historically valid entries is
// never blocked.
func NewBalanceEntry(accountID shared.AccountID, amount shared.Money, reason BalanceReason, createdAt time.Time) (*BalanceEntry, error) {
	if amount.IsNegative() {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"balance entry amount must not be negative")
	}
	return &BalanceEntry{
		id:              shared.NewBalanceEntryID(),
		accountID:       accountID,
		originalAmount:  amount,
		remainingAmount: amount,
		reason:          reason,
		createdAt:       createdAt,
	}, nil
}

// ID returns the credit entry ID.
func (e *BalanceEntry) ID() shared.BalanceEntryID { return e.id }

// AccountID returns the account ID.
func (e *BalanceEntry) AccountID() shared.AccountID { return e.accountID }

// OriginalAmount returns the original credit amount.
func (e *BalanceEntry) OriginalAmount() shared.Money { return e.originalAmount }

// RemainingAmount returns the remaining credit amount.
func (e *BalanceEntry) RemainingAmount() shared.Money { return e.remainingAmount }

// Reason returns the credit reason.
func (e *BalanceEntry) Reason() BalanceReason { return e.reason }

// SourceType returns the source type.
func (e *BalanceEntry) SourceType() BalanceSourceType { return e.sourceType }

// SetSourceType sets the source type. Used by construction helpers and
// by callers that need to augment a BalanceEntry after NewBalanceEntry.
//
// For loading from persistence, prefer FromSnapshot which restores all
// fields atomically.
func (e *BalanceEntry) SetSourceType(st BalanceSourceType) { e.sourceType = st }

// SourceID returns the source ID.
func (e *BalanceEntry) SourceID() string { return e.sourceID }

// Description returns the description.
func (e *BalanceEntry) Description() string { return e.description }

// ExpiresAt returns a defensive copy of the expiration time pointer, or
// nil if no expiration is set. Mutating the returned pointer does NOT
// affect the entry's internal state (see issue #96).
func (e *BalanceEntry) ExpiresAt() *time.Time {
	if e.expiresAt == nil {
		return nil
	}
	v := *e.expiresAt
	return &v
}

// SetExpiresAt sets the expiration time. Used by construction helpers and
// by callers that need to augment a BalanceEntry after NewBalanceEntry.
// The pointer is defensively copied so callers may safely mutate their
// own *time.Time after this call (see issue #96). Passing nil clears the
// expiration.
//
// For loading from persistence, prefer FromSnapshot which restores all
// fields atomically.
func (e *BalanceEntry) SetExpiresAt(t *time.Time) {
	if t == nil {
		e.expiresAt = nil
		return
	}
	v := *t
	e.expiresAt = &v
}

// CreatedAt returns the creation time.
func (e *BalanceEntry) CreatedAt() time.Time { return e.createdAt }

// IsExpired returns true if the credit has expired as of the given time.
func (e *BalanceEntry) IsExpired(now time.Time) bool {
	if e.expiresAt == nil {
		return false
	}
	return now.After(*e.expiresAt)
}

// IsFullyConsumed returns true if the remaining amount is zero.
func (e *BalanceEntry) IsFullyConsumed() bool {
	return e.remainingAmount.IsZero()
}

// Version returns the current version for optimistic locking.
func (e *BalanceEntry) Version() int { return e.version }

// SetVersion sets the version and records it as the loaded version.
// Repository implementations call this after a successful save to sync the
// loaded version with the stored version, so that subsequent saves from the
// same pointer do not trigger a false version-conflict error.
//
// For initial reconstitution from persistence, prefer FromSnapshot which
// restores version atomically alongside all other fields.
func (e *BalanceEntry) SetVersion(v int) {
	e.version = v
	e.loadedVersion = v
}

// LoadedVersion returns the version at the time the entity was loaded.
// Repository implementations compare this against the stored version on save.
func (e *BalanceEntry) LoadedVersion() int { return e.loadedVersion }

// Consume reduces the remaining amount by the given amount and increments the version.
// Returns the actually consumed amount (may be less than requested if insufficient balance).
func (e *BalanceEntry) Consume(amount shared.Money) (shared.Money, error) {
	// Guard the financial invariant: a negative amount would subtract a
	// negative below and inflate the remaining balance (create credit).
	if amount.IsNegative() {
		return shared.Money{}, shared.NewDomainError(shared.ErrCodeValidation,
			"consume amount must not be negative")
	}
	available := e.remainingAmount
	consumed, err := available.Min(amount)
	if err != nil {
		return shared.Money{}, err
	}
	e.remainingAmount, err = e.remainingAmount.Subtract(consumed)
	if err != nil {
		return shared.Money{}, err
	}
	if !consumed.IsZero() {
		e.version++
	}
	return consumed, nil
}
