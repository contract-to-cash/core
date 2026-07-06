package invoice

import (
	"fmt"
	"math/big"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// CreditNoteStatus represents the lifecycle status of a credit note.
type CreditNoteStatus string

const (
	CreditNoteStatusDraft    CreditNoteStatus = "draft"
	CreditNoteStatusIssued   CreditNoteStatus = "issued"
	CreditNoteStatusApplied  CreditNoteStatus = "applied"
	CreditNoteStatusRefunded CreditNoteStatus = "refunded"
	CreditNoteStatusVoided   CreditNoteStatus = "voided"
)

// CreditNoteReason represents the reason for issuing a credit note.
type CreditNoteReason string

const (
	CreditNoteReasonDuplicate             CreditNoteReason = "duplicate"
	CreditNoteReasonOrderChange           CreditNoteReason = "order_change"
	CreditNoteReasonCancellation          CreditNoteReason = "cancellation"
	CreditNoteReasonProductUnsatisfactory CreditNoteReason = "product_unsatisfactory"
	CreditNoteReasonOther                 CreditNoteReason = "other"
)

// CreditNoteItem represents a line-item-level adjustment on a credit note.
type CreditNoteItem struct {
	invoiceLineItemID string
	description       string
	amount            shared.Money
	taxRate           *big.Rat
	taxAmount         shared.Money
}

// NewCreditNoteItem creates a new CreditNoteItem.
// The taxRate pointer is defensively copied so mutations to the caller's
// *big.Rat do not leak into the item (see issue #96).
//
// The item amount must be positive. Because a CreditNoteItem is a value builder
// consumed exclusively by NewCreditNote (a bare item never becomes persisted
// state on its own), the non-positive / mixed-currency invariant is enforced
// there — NewCreditNote rejects any item whose amount is zero, negative, or in a
// currency other than the note's. This keeps the value builder allocation-free
// and error-free while still guaranteeing no nonsensical amount is persisted
// (issue #148).
func NewCreditNoteItem(invoiceLineItemID, description string, amount shared.Money, taxRate *big.Rat, taxAmount shared.Money) CreditNoteItem {
	var ownedTaxRate *big.Rat
	if taxRate != nil {
		ownedTaxRate = new(big.Rat).Set(taxRate)
	}
	return CreditNoteItem{
		invoiceLineItemID: invoiceLineItemID,
		description:       description,
		amount:            amount,
		taxRate:           ownedTaxRate,
		taxAmount:         taxAmount,
	}
}

func (i CreditNoteItem) InvoiceLineItemID() string { return i.invoiceLineItemID }
func (i CreditNoteItem) Description() string       { return i.description }
func (i CreditNoteItem) Amount() shared.Money      { return i.amount }

// TaxRate returns a defensive copy of the tax rate so callers cannot
// mutate the item's internal state (see issue #96).
func (i CreditNoteItem) TaxRate() *big.Rat {
	if i.taxRate == nil {
		return nil
	}
	return new(big.Rat).Set(i.taxRate)
}
func (i CreditNoteItem) TaxAmount() shared.Money { return i.taxAmount }

// CreditNote represents a credit note entity that adjusts a previously issued invoice.
type CreditNote struct {
	id           shared.CreditNoteID
	number       string
	invoiceID    shared.InvoiceID
	accountID    shared.AccountID
	contractID   shared.ContractID
	status       CreditNoteStatus
	reason       CreditNoteReason
	memo         string
	items        []CreditNoteItem
	subtotal     shared.Money
	taxAmount    shared.Money
	total        shared.Money
	creditAmount shared.Money
	refundAmount shared.Money
	issuedAt     *time.Time
	createdAt    time.Time

	// Optimistic-locking support (mirrors invoice.Invoice, issue #147).
	// version is bumped by EVERY state transition that changes persisted state
	// (Issue, Apply, Refund, Void). Without it, an issued credit note loaded by
	// two callers could be Apply()'d by one and Refund()'d by the other and both
	// succeed — crediting the account AND refunding the gateway while recording
	// only one outcome. loadedVersion records the version observed at load time;
	// a repository honoring the concurrency contract (see
	// CreditNoteRepository.Save) compares it against the stored version on Save
	// and rejects a mismatch with tx.ErrVersionConflict. A brand-new credit note
	// starts at version 0 / loadedVersion 0, so adapters that never populate
	// these fields keep working unchanged.
	version       int
	loadedVersion int
}

// CreditNoteOption is a functional option for NewCreditNote.
type CreditNoteOption func(*CreditNote)

// WithCreditNoteMemo sets the memo on the credit note.
func WithCreditNoteMemo(memo string) CreditNoteOption {
	return func(cn *CreditNote) { cn.memo = memo }
}

// WithCreditNoteNumber sets the display number on the credit note.
func WithCreditNoteNumber(number string) CreditNoteOption {
	return func(cn *CreditNote) { cn.number = number }
}

// NewCreditNote creates a new CreditNote in draft status.
// Returns an error if items is empty.
func NewCreditNote(
	id shared.CreditNoteID,
	invoiceID shared.InvoiceID,
	accountID shared.AccountID,
	contractID shared.ContractID,
	reason CreditNoteReason,
	items []CreditNoteItem,
	createdAt time.Time,
	opts ...CreditNoteOption,
) (*CreditNote, error) {
	if len(items) == 0 {
		return nil, shared.NewDomainError(shared.ErrCodeValidation, "credit note must have at least one item")
	}

	// All item amounts (and their taxes) must share a single currency. Mixing
	// currencies would otherwise silently corrupt the totals and let an
	// over-credit slip past the caller's "exceeds invoice total" guard, since
	// Money.GreaterThan returns false on a currency mismatch (see review #2).
	currency := items[0].amount.Currency()
	subtotal := shared.Zero(currency)
	taxAmount := shared.Zero(currency)

	for _, item := range items {
		if item.amount.Currency() != currency {
			return nil, shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
				fmt.Sprintf("credit note items must share a single currency: got %s and %s",
					currency, item.amount.Currency()))
		}
		// Guard the financial invariant at the aggregate boundary (issue #148):
		// a zero or negative item amount is meaningless and would let a bogus
		// CreditAmount slip past the "exceeds total" guard in Apply/Refund, since
		// Money.GreaterThan returns false on a currency mismatch and a negative
		// subtotal skews the total.
		if item.amount.IsNegative() || item.amount.IsZero() {
			return nil, shared.NewDomainError(shared.ErrCodeValidation,
				fmt.Sprintf("credit note item amount must be positive: got %s",
					item.amount.Amount().RatString()))
		}
		s, err := subtotal.Add(item.amount)
		if err != nil {
			return nil, err
		}
		subtotal = s

		// A zero-value (empty-currency) tax means "no tax"; normalise it to the
		// base currency so it contributes zero rather than triggering a mismatch.
		itemTax := item.taxAmount
		if itemTax.IsZero() {
			itemTax = shared.Zero(currency)
		}
		if itemTax.Currency() != currency {
			return nil, shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
				fmt.Sprintf("credit note item tax currency %s does not match item currency %s",
					itemTax.Currency(), currency))
		}
		ta, err := taxAmount.Add(itemTax)
		if err != nil {
			return nil, err
		}
		taxAmount = ta
	}

	total, err := subtotal.Add(taxAmount)
	if err != nil {
		return nil, err
	}

	// Defensively copy the caller's slice so a post-construction mutation
	// (items[i] = ... or append reusing the backing array) cannot rewrite the
	// note's persisted line items. Items() already returns a copy on read; this
	// closes the same hole on intake (issue #162 L-6).
	ownedItems := make([]CreditNoteItem, len(items))
	copy(ownedItems, items)

	cn := &CreditNote{
		id:           id,
		invoiceID:    invoiceID,
		accountID:    accountID,
		contractID:   contractID,
		status:       CreditNoteStatusDraft,
		reason:       reason,
		items:        ownedItems,
		subtotal:     subtotal,
		taxAmount:    taxAmount,
		total:        total,
		creditAmount: shared.Zero(currency),
		refundAmount: shared.Zero(currency),
		createdAt:    createdAt,
	}

	for _, opt := range opts {
		opt(cn)
	}

	return cn, nil
}

// Issue transitions the credit note from draft to issued.
func (cn *CreditNote) Issue(issuedAt time.Time) error {
	if cn.status != CreditNoteStatusDraft {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot issue credit note in status %s", cn.status))
	}
	cn.status = CreditNoteStatusIssued
	cn.issuedAt = &issuedAt
	cn.version++
	return nil
}

// validateAdjustmentAmount guards the currency and sign invariants shared by
// Apply and Refund. Money.GreaterThan silently returns false on a currency
// mismatch (see domain/shared/money.go), so an explicit currency check MUST
// precede the "exceeds total" comparison; otherwise a foreign-currency amount
// (e.g. USD 1,000,000 against a JPY note) would slip past it. A non-positive
// amount is likewise rejected: applying or refunding a zero or negative credit
// is meaningless and would persist a nonsensical CreditAmount/RefundAmount on
// the issued note. Mirrors payment.ValidateRefund (issue #148).
func (cn *CreditNote) validateAdjustmentAmount(amount shared.Money, label string) error {
	if amount.Currency() != cn.total.Currency() {
		return shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
			fmt.Sprintf("%s amount currency %s does not match credit note currency %s",
				label, amount.Currency(), cn.total.Currency()))
	}
	if amount.IsNegative() || amount.IsZero() {
		return shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("%s amount must be positive", label))
	}
	if amount.GreaterThan(cn.total) {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("%s amount %s exceeds total %s",
				label, amount.Amount().RatString(), cn.total.Amount().RatString()))
	}
	return nil
}

// Apply transitions the credit note from issued to applied (credit applied to account).
func (cn *CreditNote) Apply(creditAmount shared.Money) error {
	if cn.status != CreditNoteStatusIssued {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot apply credit note in status %s", cn.status))
	}
	if err := cn.validateAdjustmentAmount(creditAmount, "credit"); err != nil {
		return err
	}
	cn.creditAmount = creditAmount
	cn.status = CreditNoteStatusApplied
	// Bump the optimistic-locking version (issue #147): a concurrent Apply vs
	// Refund on the same issued credit note must not both persist. A compliant
	// repository rejects the second Save with tx.ErrVersionConflict.
	cn.version++
	return nil
}

// Refund transitions the credit note from issued to refunded (payment refund processed).
func (cn *CreditNote) Refund(refundAmount shared.Money) error {
	if cn.status != CreditNoteStatusIssued {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot refund credit note in status %s", cn.status))
	}
	if err := cn.validateAdjustmentAmount(refundAmount, "refund"); err != nil {
		return err
	}
	cn.refundAmount = refundAmount
	cn.status = CreditNoteStatusRefunded
	// Bump the optimistic-locking version (issue #147): see Apply for the
	// Apply-vs-Refund race this guards against.
	cn.version++
	return nil
}

// Void transitions the credit note to voided status.
// Only draft and issued credit notes can be voided.
func (cn *CreditNote) Void() error {
	if cn.status != CreditNoteStatusDraft && cn.status != CreditNoteStatusIssued {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot void credit note in status %s", cn.status))
	}
	cn.status = CreditNoteStatusVoided
	cn.version++
	return nil
}

// --- Getters ---

func (cn *CreditNote) ID() shared.CreditNoteID       { return cn.id }
func (cn *CreditNote) Number() string                { return cn.number }
func (cn *CreditNote) InvoiceID() shared.InvoiceID   { return cn.invoiceID }
func (cn *CreditNote) AccountID() shared.AccountID   { return cn.accountID }
func (cn *CreditNote) ContractID() shared.ContractID { return cn.contractID }
func (cn *CreditNote) Status() CreditNoteStatus      { return cn.status }
func (cn *CreditNote) Reason() CreditNoteReason      { return cn.reason }
func (cn *CreditNote) Memo() string                  { return cn.memo }
func (cn *CreditNote) Items() []CreditNoteItem {
	cp := make([]CreditNoteItem, len(cn.items))
	copy(cp, cn.items)
	return cp
}
func (cn *CreditNote) Subtotal() shared.Money     { return cn.subtotal }
func (cn *CreditNote) TaxAmount() shared.Money    { return cn.taxAmount }
func (cn *CreditNote) Total() shared.Money        { return cn.total }
func (cn *CreditNote) CreditAmount() shared.Money { return cn.creditAmount }
func (cn *CreditNote) RefundAmount() shared.Money { return cn.refundAmount }

// IssuedAt returns a defensive copy of the issued-at timestamp pointer so
// callers cannot mutate the credit note's internal state (see issue #96).
func (cn *CreditNote) IssuedAt() *time.Time {
	if cn.issuedAt == nil {
		return nil
	}
	v := *cn.issuedAt
	return &v
}
func (cn *CreditNote) CreatedAt() time.Time { return cn.createdAt }

// Version returns the current optimistic-locking version. It is incremented by
// every state transition that changes persisted state — Issue, Apply, Refund,
// and Void. See issue #147.
func (cn *CreditNote) Version() int { return cn.version }

// LoadedVersion returns the version observed when this credit note was loaded
// from persistence. Repository implementations compare it against the stored
// version on Save to detect a concurrent modification (issue #147).
func (cn *CreditNote) LoadedVersion() int { return cn.loadedVersion }

// SetVersion sets the version and records it as the loaded version.
// Repository implementations call this after a successful Save so that
// subsequent saves from the same pointer compare against the just-persisted
// version instead of a stale baseline.
//
// For initial reconstitution from persistence, prefer CreditNoteFromSnapshot,
// which restores version and loadedVersion atomically alongside all other
// fields.
func (cn *CreditNote) SetVersion(v int) {
	cn.version = v
	cn.loadedVersion = v
}
