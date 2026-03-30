package invoice

import (
	"fmt"
	"math/big"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// InvoiceStatus represents the lifecycle status of an invoice.
type InvoiceStatus string

const (
	InvoiceStatusDraft       InvoiceStatus = "draft"
	InvoiceStatusFinalized   InvoiceStatus = "finalized"
	InvoiceStatusIssued      InvoiceStatus = "issued"
	InvoiceStatusPaid        InvoiceStatus = "paid"
	InvoiceStatusPartialPaid InvoiceStatus = "partial_paid"
	InvoiceStatusOverdue     InvoiceStatus = "overdue"
	InvoiceStatusVoided      InvoiceStatus = "voided"
	InvoiceStatusRefunded    InvoiceStatus = "refunded"
)

// LineItem represents a single line on an invoice.
type LineItem struct {
	id          string
	description string
	quantity    int64
	unitPrice   shared.Money
	amount      shared.Money
	taxRate     *big.Rat
	priceID     shared.PriceID
	metadata    map[string]string
}

// LineItemOption is a functional option for NewLineItem.
type LineItemOption func(*LineItem)

// WithPriceID sets the price ID on the line item for traceability.
func WithPriceID(id shared.PriceID) LineItemOption {
	return func(li *LineItem) {
		li.priceID = id
	}
}

// NewLineItem creates a new LineItem.
// Panics if quantity is negative, as this indicates a programming error.
func NewLineItem(id, description string, quantity int64, unitPrice, amount shared.Money, taxRate *big.Rat, opts ...LineItemOption) LineItem {
	if quantity < 0 {
		panic(fmt.Sprintf("line item quantity must not be negative: %d", quantity))
	}
	li := LineItem{
		id:          id,
		description: description,
		quantity:    quantity,
		unitPrice:   unitPrice,
		amount:      amount,
		taxRate:     taxRate,
		metadata:    make(map[string]string),
	}
	for _, opt := range opts {
		opt(&li)
	}
	return li
}

func (li LineItem) ID() string              { return li.id }
func (li LineItem) Description() string     { return li.description }
func (li LineItem) Quantity() int64         { return li.quantity }
func (li LineItem) UnitPrice() shared.Money { return li.unitPrice }
func (li LineItem) Amount() shared.Money    { return li.amount }
func (li LineItem) TaxRate() *big.Rat       { return li.taxRate }
func (li LineItem) PriceID() shared.PriceID { return li.priceID }
func (li LineItem) Metadata() map[string]string {
	cp := make(map[string]string, len(li.metadata))
	for k, v := range li.metadata {
		cp[k] = v
	}
	return cp
}

// Invoice represents an invoice entity.
type Invoice struct {
	id              shared.InvoiceID
	invoiceNumber   string
	accountID       shared.AccountID
	contractID      shared.ContractID
	lineItems       []LineItem
	subtotal        shared.Money
	taxAmount       shared.Money
	discountAmount  shared.Money
	total           shared.Money
	appliedBalance  shared.Money
	amountDue       shared.Money
	paidAmount      shared.Money
	balance         shared.Money
	status          InvoiceStatus
	billingPeriod   shared.DateRange
	issueDate       time.Time
	dueDate         time.Time
	paidAt          *time.Time
	paymentMethodID *string
	metadata        map[string]string
	allowPartialPay bool

	// Revision support
	originalInvoiceID *shared.InvoiceID
	revisionOf        *shared.InvoiceID
	voidReason        string
}

// InvoiceOption is a functional option for NewInvoice.
type InvoiceOption func(*Invoice)

// WithStatus sets the initial status of the invoice.
func WithStatus(s InvoiceStatus) InvoiceOption {
	return func(inv *Invoice) {
		inv.status = s
	}
}

// WithBillingPeriod sets the billing period of the invoice.
func WithBillingPeriod(p shared.DateRange) InvoiceOption {
	return func(inv *Invoice) {
		inv.billingPeriod = p
	}
}

// WithDueDate sets the due date of the invoice.
func WithDueDate(t time.Time) InvoiceOption {
	return func(inv *Invoice) {
		inv.dueDate = t
	}
}

// WithAppliedBalance sets the applied balance amount and recalculates amountDue and balance.
func WithAppliedBalance(c shared.Money) InvoiceOption {
	return func(inv *Invoice) {
		inv.appliedBalance = c
		amountDue, err := inv.total.Subtract(c)
		if err == nil {
			inv.amountDue = amountDue
			inv.balance = amountDue
		}
	}
}

// WithAmountDue sets the amount due.
func WithAmountDue(a shared.Money) InvoiceOption {
	return func(inv *Invoice) {
		inv.amountDue = a
	}
}

// WithAllowPartialPayment sets whether partial payment is allowed.
func WithAllowPartialPayment(allow bool) InvoiceOption {
	return func(inv *Invoice) {
		inv.allowPartialPay = allow
	}
}

// WithLineItems sets the line items.
func WithLineItems(items []LineItem) InvoiceOption {
	return func(inv *Invoice) {
		inv.lineItems = items
	}
}

// WithInvoiceNumber sets the invoice number.
func WithInvoiceNumber(n string) InvoiceOption {
	return func(inv *Invoice) {
		inv.invoiceNumber = n
	}
}

// NewInvoice creates a new Invoice with the given required fields and options.
func NewInvoice(
	id shared.InvoiceID,
	accountID shared.AccountID,
	contractID shared.ContractID,
	subtotal shared.Money,
	discountAmount shared.Money,
	taxAmount shared.Money,
	opts ...InvoiceOption,
) *Invoice {
	// total = subtotal - discountAmount + taxAmount
	afterDiscount, _ := subtotal.Subtract(discountAmount)
	total, _ := afterDiscount.Add(taxAmount)

	inv := &Invoice{
		id:             id,
		accountID:      accountID,
		contractID:     contractID,
		subtotal:       subtotal,
		discountAmount: discountAmount,
		taxAmount:      taxAmount,
		total:          total,
		appliedBalance: shared.Zero(subtotal.Currency()),
		amountDue:      total,
		paidAmount:     shared.Zero(subtotal.Currency()),
		balance:        total,
		status:         InvoiceStatusDraft,
		issueDate:      time.Time{}, // set via WithIssueDate option or by caller
		metadata:       make(map[string]string),
	}

	for _, opt := range opts {
		opt(inv)
	}

	return inv
}

// Finalize transitions the invoice from draft to finalized.
func (inv *Invoice) Finalize() error {
	if inv.status != InvoiceStatusDraft {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot finalize invoice in status %s", inv.status))
	}
	inv.status = InvoiceStatusFinalized
	return nil
}

// ValidatePayment checks if a payment of the given amount can be accepted
// without modifying the invoice state. This is useful for pre-charge validation.
func (inv *Invoice) ValidatePayment(amount shared.Money) error {
	if inv.status != InvoiceStatusFinalized && inv.status != InvoiceStatusIssued &&
		inv.status != InvoiceStatusPartialPaid && inv.status != InvoiceStatusOverdue {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot record payment: invoice status is %s", inv.status))
	}

	newPaid, err := inv.paidAmount.Add(amount)
	if err != nil {
		return err
	}

	if newPaid.GreaterThan(inv.amountDue) {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("payment of %s would exceed amount due %s (already paid %s)",
				amount.Amount().RatString(), inv.amountDue.Amount().RatString(), inv.paidAmount.Amount().RatString()))
	}

	return nil
}

// RecordPayment records a payment against this invoice.
// Only finalized or issued invoices can accept payments.
// The payment amount must not cause the total paid to exceed the amount due.
func (inv *Invoice) RecordPayment(amount shared.Money, paidAt time.Time) error {
	if err := inv.ValidatePayment(amount); err != nil {
		return err
	}

	newPaid, err := inv.paidAmount.Add(amount)
	if err != nil {
		return err
	}
	inv.paidAmount = newPaid

	newBalance, err := inv.amountDue.Subtract(inv.paidAmount)
	if err != nil {
		return err
	}
	inv.balance = newBalance

	inv.paidAt = &paidAt

	if inv.balance.IsZero() {
		inv.status = InvoiceStatusPaid
	} else {
		inv.status = InvoiceStatusPartialPaid
	}

	return nil
}

// --- Getters ---

func (inv *Invoice) ID() shared.InvoiceID          { return inv.id }
func (inv *Invoice) InvoiceNumber() string         { return inv.invoiceNumber }
func (inv *Invoice) AccountID() shared.AccountID   { return inv.accountID }
func (inv *Invoice) ContractID() shared.ContractID { return inv.contractID }
func (inv *Invoice) LineItems() []LineItem {
	cp := make([]LineItem, len(inv.lineItems))
	copy(cp, inv.lineItems)
	return cp
}
func (inv *Invoice) Subtotal() shared.Money          { return inv.subtotal }
func (inv *Invoice) TaxAmount() shared.Money         { return inv.taxAmount }
func (inv *Invoice) DiscountAmount() shared.Money    { return inv.discountAmount }
func (inv *Invoice) Total() shared.Money             { return inv.total }
func (inv *Invoice) AppliedBalance() shared.Money    { return inv.appliedBalance }
func (inv *Invoice) AmountDue() shared.Money         { return inv.amountDue }
func (inv *Invoice) PaidAmount() shared.Money        { return inv.paidAmount }
func (inv *Invoice) Balance() shared.Money           { return inv.balance }
func (inv *Invoice) Status() InvoiceStatus           { return inv.status }
func (inv *Invoice) BillingPeriod() shared.DateRange { return inv.billingPeriod }
func (inv *Invoice) IssueDate() time.Time            { return inv.issueDate }
func (inv *Invoice) DueDate() time.Time              { return inv.dueDate }
func (inv *Invoice) PaidAt() *time.Time              { return inv.paidAt }
func (inv *Invoice) Metadata() map[string]string {
	cp := make(map[string]string, len(inv.metadata))
	for k, v := range inv.metadata {
		cp[k] = v
	}
	return cp
}

// WithIssueDate sets the issue date.
func WithIssueDate(t time.Time) InvoiceOption {
	return func(inv *Invoice) {
		inv.issueDate = t
	}
}
func (inv *Invoice) AllowPartialPay() bool { return inv.allowPartialPay }

// Void transitions the invoice to voided status.
// Only Draft and Finalized invoices can be voided.
func (inv *Invoice) Void() error {
	if inv.status != InvoiceStatusDraft && inv.status != InvoiceStatusFinalized {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot void invoice in status %s", inv.status))
	}
	inv.status = InvoiceStatusVoided
	return nil
}

// VoidWithReason transitions the invoice to voided status with an explicit reason.
// Unlike Void(), this can be used on issued, paid, partial_paid, and overdue invoices
// (e.g., when a Credit Note has been issued).
// Cannot void already voided or refunded invoices.
func (inv *Invoice) VoidWithReason(reason string) error {
	if reason == "" {
		return shared.NewDomainError(shared.ErrCodeValidation, "void reason must not be empty")
	}
	if inv.status == InvoiceStatusVoided || inv.status == InvoiceStatusRefunded {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot void invoice in status %s", inv.status))
	}
	inv.status = InvoiceStatusVoided
	inv.voidReason = reason
	return nil
}

// OriginalInvoiceID returns the original invoice ID (for reissued invoices).
func (inv *Invoice) OriginalInvoiceID() *shared.InvoiceID { return inv.originalInvoiceID }

// RevisionOf returns the invoice ID this invoice is a revision of.
func (inv *Invoice) RevisionOf() *shared.InvoiceID { return inv.revisionOf }

// VoidReason returns the reason this invoice was voided.
func (inv *Invoice) VoidReason() string { return inv.voidReason }

// SetRevisionOf sets the revision link to the original invoice.
// This is used after invoice creation to link a replacement to its original.
func (inv *Invoice) SetRevisionOf(id shared.InvoiceID) {
	inv.revisionOf = &id
}

// PaymentMethodID returns the invoice-level payment method ID override.
func (inv *Invoice) PaymentMethodID() *string { return inv.paymentMethodID }

// WithPaymentMethodID sets the payment method ID on the invoice.
func WithPaymentMethodID(id *string) InvoiceOption {
	return func(inv *Invoice) {
		inv.paymentMethodID = id
	}
}

// WithOriginalInvoiceID sets the original invoice ID (for reissued invoices).
func WithOriginalInvoiceID(id shared.InvoiceID) InvoiceOption {
	return func(inv *Invoice) {
		inv.originalInvoiceID = &id
	}
}

// WithRevisionOf sets the revision link to the original invoice.
func WithRevisionOf(id shared.InvoiceID) InvoiceOption {
	return func(inv *Invoice) {
		inv.revisionOf = &id
	}
}
