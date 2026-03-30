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
func NewCreditNoteItem(invoiceLineItemID, description string, amount shared.Money, taxRate *big.Rat, taxAmount shared.Money) CreditNoteItem {
	return CreditNoteItem{
		invoiceLineItemID: invoiceLineItemID,
		description:       description,
		amount:            amount,
		taxRate:           taxRate,
		taxAmount:         taxAmount,
	}
}

func (i CreditNoteItem) InvoiceLineItemID() string { return i.invoiceLineItemID }
func (i CreditNoteItem) Description() string       { return i.description }
func (i CreditNoteItem) Amount() shared.Money      { return i.amount }
func (i CreditNoteItem) TaxRate() *big.Rat         { return i.taxRate }
func (i CreditNoteItem) TaxAmount() shared.Money   { return i.taxAmount }

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
// Panics if items is empty, as a credit note without items is a programming error.
func NewCreditNote(
	id shared.CreditNoteID,
	invoiceID shared.InvoiceID,
	accountID shared.AccountID,
	contractID shared.ContractID,
	reason CreditNoteReason,
	items []CreditNoteItem,
	createdAt time.Time,
	opts ...CreditNoteOption,
) *CreditNote {
	if len(items) == 0 {
		panic("credit note must have at least one item")
	}

	currency := items[0].amount.Currency()
	subtotal := shared.Zero(currency)
	taxAmount := shared.Zero(currency)

	for _, item := range items {
		s, _ := subtotal.Add(item.amount)
		subtotal = s
		ta, _ := taxAmount.Add(item.taxAmount)
		taxAmount = ta
	}

	total, _ := subtotal.Add(taxAmount)

	cn := &CreditNote{
		id:           id,
		invoiceID:    invoiceID,
		accountID:    accountID,
		contractID:   contractID,
		status:       CreditNoteStatusDraft,
		reason:       reason,
		items:        items,
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

	return cn
}

// Issue transitions the credit note from draft to issued.
func (cn *CreditNote) Issue(issuedAt time.Time) error {
	if cn.status != CreditNoteStatusDraft {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot issue credit note in status %s", cn.status))
	}
	cn.status = CreditNoteStatusIssued
	cn.issuedAt = &issuedAt
	return nil
}

// Apply transitions the credit note from issued to applied (credit applied to account).
func (cn *CreditNote) Apply(creditAmount shared.Money) error {
	if cn.status != CreditNoteStatusIssued {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot apply credit note in status %s", cn.status))
	}
	if creditAmount.GreaterThan(cn.total) {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("credit amount %s exceeds total %s",
				creditAmount.Amount().RatString(), cn.total.Amount().RatString()))
	}
	cn.creditAmount = creditAmount
	cn.status = CreditNoteStatusApplied
	return nil
}

// Refund transitions the credit note from issued to refunded (payment refund processed).
func (cn *CreditNote) Refund(refundAmount shared.Money) error {
	if cn.status != CreditNoteStatusIssued {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot refund credit note in status %s", cn.status))
	}
	if refundAmount.GreaterThan(cn.total) {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("refund amount %s exceeds total %s",
				refundAmount.Amount().RatString(), cn.total.Amount().RatString()))
	}
	cn.refundAmount = refundAmount
	cn.status = CreditNoteStatusRefunded
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
func (cn *CreditNote) IssuedAt() *time.Time       { return cn.issuedAt }
func (cn *CreditNote) CreatedAt() time.Time       { return cn.createdAt }
