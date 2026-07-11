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

// MetadataKeyInvoiceType is the metadata key under which the billing pipeline
// records which flow produced an invoice. The (contract_id, billing_period)
// uniqueness contract documented on Repository.Save keys off this value:
// proration invoices are exempt because they intentionally coexist with the
// period's regular invoice.
const MetadataKeyInvoiceType = "invoice_type"

const (
	// InvoiceTypeProration marks a proration adjustment invoice. Proration
	// invoices are exempt from the per-period uniqueness constraint.
	InvoiceTypeProration = "proration"
	// InvoiceTypeRegeneration marks a void-and-recreate replacement invoice.
	// It is a regular period invoice and DOES participate in uniqueness.
	InvoiceTypeRegeneration = "regeneration"
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
// Returns an error if quantity is negative.
// The taxRate pointer is defensively copied so mutations to the caller's
// *big.Rat do not leak into the line item (see issue #96).
func NewLineItem(id, description string, quantity int64, unitPrice, amount shared.Money, taxRate *big.Rat, opts ...LineItemOption) (LineItem, error) {
	if quantity < 0 {
		return LineItem{}, shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("line item quantity must not be negative: %d", quantity))
	}
	var ownedTaxRate *big.Rat
	if taxRate != nil {
		ownedTaxRate = new(big.Rat).Set(taxRate)
	}
	li := LineItem{
		id:          id,
		description: description,
		quantity:    quantity,
		unitPrice:   unitPrice,
		amount:      amount,
		taxRate:     ownedTaxRate,
		metadata:    make(map[string]string),
	}
	for _, opt := range opts {
		opt(&li)
	}
	return li, nil
}

func (li LineItem) ID() string              { return li.id }
func (li LineItem) Description() string     { return li.description }
func (li LineItem) Quantity() int64         { return li.quantity }
func (li LineItem) UnitPrice() shared.Money { return li.unitPrice }
func (li LineItem) Amount() shared.Money    { return li.amount }

// TaxRate returns a defensive copy of the tax rate so callers cannot
// mutate the line item's internal state (see issue #96).
func (li LineItem) TaxRate() *big.Rat {
	if li.taxRate == nil {
		return nil
	}
	return new(big.Rat).Set(li.taxRate)
}
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

	// Revision support: two-level linking for void-and-recreate workflows.
	// originalInvoiceID points to the root of the revision chain (first invoice).
	// revisionOf points to the direct parent (previous version in the chain).
	// Example: Inv-1 → Inv-2 → Inv-3
	//   Inv-2: originalInvoiceID=Inv-1, revisionOf=Inv-1
	//   Inv-3: originalInvoiceID=Inv-1, revisionOf=Inv-2
	originalInvoiceID *shared.InvoiceID
	revisionOf        *shared.InvoiceID
	voidReason        string
	refundReason      string

	// Optimistic-locking support (mirrors balance.BalanceEntry, issue #130 / #147).
	// version is bumped by EVERY state transition that changes persisted state
	// (Finalize, MarkIssued, MarkOverdue, RecordPayment, Void, VoidWithReason,
	// MarkRefunded, and the revision-chain link setters SetRevisionOf /
	// SetOriginalInvoiceID).
	// loadedVersion records the version observed when the invoice was loaded from
	// persistence; repositories compare it against the stored version on Save to
	// reject a check-then-act race (tx.ErrVersionConflict). A brand-new invoice
	// starts at version 0 / loadedVersion 0, so callers and adapters that never
	// populate these fields keep working unchanged.
	version       int
	loadedVersion int

	// optErr captures a deferred validation error produced by a functional
	// option. Options cannot return errors, so an option that detects an
	// invariant violation (e.g. WithAppliedBalance encountering a currency
	// mismatch) records it here; NewInvoice checks it after applying all options
	// and returns it instead of silently leaving amountDue/balance inconsistent
	// (issue #148). The first error wins.
	optErr error
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
//
// If the applied balance is in a different currency than the invoice total, the
// subtraction fails; rather than silently swallowing the error (which would
// leave appliedBalance set to a foreign amount while amountDue/balance kept the
// original total — an inconsistent invoice), the error is recorded on the
// invoice and surfaced by NewInvoice (issue #148).
func WithAppliedBalance(c shared.Money) InvoiceOption {
	return func(inv *Invoice) {
		// Sign/magnitude invariants (issue #188): a negative applied balance would
		// inflate amountDue above the total, and an applied balance greater than
		// the total would drive amountDue negative — both leave an invoice that
		// cannot be settled correctly.
		if c.IsNegative() {
			if inv.optErr == nil {
				inv.optErr = shared.NewDomainError(shared.ErrCodeValidation,
					fmt.Sprintf("applied balance must not be negative: %s", c.Amount().RatString()))
			}
			return
		}
		inv.appliedBalance = c
		amountDue, err := inv.total.Subtract(c)
		if err != nil {
			if inv.optErr == nil {
				inv.optErr = shared.NewDomainErrorWithCause(shared.ErrCodeCurrencyMismatch,
					"currency mismatch between invoice total and applied balance", err)
			}
			return
		}
		// A negative amountDue means the applied balance exceeded the total.
		if amountDue.IsNegative() {
			if inv.optErr == nil {
				inv.optErr = shared.NewDomainError(shared.ErrCodeValidation,
					fmt.Sprintf("applied balance %s must not exceed invoice total %s",
						c.Amount().RatString(), inv.total.Amount().RatString()))
			}
			return
		}
		inv.amountDue = amountDue
		inv.balance = amountDue
	}
}

// WithAmountDue sets the amount due (and keeps balance in sync).
//
// The amount due must be non-negative and must not exceed the invoice total
// (issue #188); a negative or inflated amountDue is unsettleable / over-billing.
// The "exceeds total" comparison uses Money.GreaterThanStrict so a foreign
// currency surfaces as a mismatch error rather than silently reading as "not
// greater" (issue #196).
//
// Like WithAppliedBalance, this also updates balance: balance tracks
// amountDue − paidAmount, and paidAmount is zero at construction, so a fresh
// invoice whose amountDue is overridden here must carry the matching balance.
// Previously only amountDue was set, leaving balance stuck at the full total —
// an invoice that reported settled would still show a non-zero balance
// (issue #196).
func WithAmountDue(a shared.Money) InvoiceOption {
	return func(inv *Invoice) {
		if a.IsNegative() {
			if inv.optErr == nil {
				inv.optErr = shared.NewDomainError(shared.ErrCodeValidation,
					fmt.Sprintf("amount due must not be negative: %s", a.Amount().RatString()))
			}
			return
		}
		exceeds, err := a.GreaterThanStrict(inv.total)
		if err != nil {
			if inv.optErr == nil {
				inv.optErr = shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
					fmt.Sprintf("currency mismatch between amount due (%s) and invoice total (%s)",
						a.Currency(), inv.total.Currency()))
			}
			return
		}
		if exceeds {
			if inv.optErr == nil {
				inv.optErr = shared.NewDomainError(shared.ErrCodeValidation,
					fmt.Sprintf("amount due %s must not exceed invoice total %s",
						a.Amount().RatString(), inv.total.Amount().RatString()))
			}
			return
		}
		inv.amountDue = a
		inv.balance = a
	}
}

// WithAllowPartialPayment sets whether partial payment is allowed.
func WithAllowPartialPayment(allow bool) InvoiceOption {
	return func(inv *Invoice) {
		inv.allowPartialPay = allow
	}
}

// WithLineItems sets the line items.
//
// The slice is defensively copied so a post-construction mutation of the
// caller's slice (items[i] = ... or an append reusing the backing array) cannot
// rewrite the invoice's persisted line items. LineItems() already returns a copy
// on read; this closes the same hole on intake (issue #162 L-6).
func WithLineItems(items []LineItem) InvoiceOption {
	return func(inv *Invoice) {
		inv.lineItems = make([]LineItem, len(items))
		copy(inv.lineItems, items)
	}
}

// WithInvoiceNumber sets the invoice number.
func WithInvoiceNumber(n string) InvoiceOption {
	return func(inv *Invoice) {
		inv.invoiceNumber = n
	}
}

// NewInvoice creates a new Invoice with the given required fields and options.
// Returns an error if currency mismatch is detected between subtotal, discountAmount, and taxAmount.
func NewInvoice(
	id shared.InvoiceID,
	accountID shared.AccountID,
	contractID shared.ContractID,
	subtotal shared.Money,
	discountAmount shared.Money,
	taxAmount shared.Money,
	opts ...InvoiceOption,
) (*Invoice, error) {
	// Sign/magnitude invariants (issue #188): an invoice's monetary components
	// must be non-negative and the discount must not exceed the subtotal. Without
	// these guards a buggy plugin (or caller) can produce a negative or inverted
	// total — e.g. subtotal ¥100 + discount ¥200 yields Total() = -100, which then
	// makes ValidatePayment reject EVERY payment ("would exceed amount due -100"),
	// leaving a permanently unsettleable invoice.
	if subtotal.IsNegative() {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("invoice subtotal must not be negative: %s", subtotal.Amount().RatString()))
	}
	if discountAmount.IsNegative() {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("invoice discount amount must not be negative: %s", discountAmount.Amount().RatString()))
	}
	if taxAmount.IsNegative() {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("invoice tax amount must not be negative: %s", taxAmount.Amount().RatString()))
	}

	// total = subtotal - discountAmount + taxAmount
	afterDiscount, err := subtotal.Subtract(discountAmount)
	if err != nil {
		return nil, shared.NewDomainErrorWithCause(shared.ErrCodeCurrencyMismatch,
			"currency mismatch between subtotal and discount amount", err)
	}
	// discount ≤ subtotal. Currency parity is already guaranteed by the Subtract
	// above, so a negative afterDiscount can only mean the discount exceeds the
	// subtotal.
	if afterDiscount.IsNegative() {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("invoice discount amount %s must not exceed subtotal %s",
				discountAmount.Amount().RatString(), subtotal.Amount().RatString()))
	}
	total, err := afterDiscount.Add(taxAmount)
	if err != nil {
		return nil, shared.NewDomainErrorWithCause(shared.ErrCodeCurrencyMismatch,
			"currency mismatch between subtotal (after discount) and tax amount", err)
	}

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

	// Surface any deferred error recorded by a functional option (issue #148).
	if inv.optErr != nil {
		return nil, inv.optErr
	}

	return inv, nil
}

// Finalize transitions the invoice from draft to finalized.
//
// It bumps the optimistic-locking version so that two concurrently loaded
// copies cannot both be finalized: a repository honoring the concurrency
// contract (see Repository.Save) rejects the second Save with
// tx.ErrVersionConflict, guaranteeing OnInvoiceIssued fires at most once per
// finalization (issue #130).
func (inv *Invoice) Finalize() error {
	if inv.status != InvoiceStatusDraft {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot finalize invoice in status %s", inv.status))
	}
	inv.status = InvoiceStatusFinalized
	inv.version++
	return nil
}

// MarkIssued transitions the invoice from finalized to issued.
//
// "Issued" records that the finalized invoice has been delivered to the
// customer (rendered/sent through the consumer's invoice-generation adapter,
// see docs/internals/metrics-invoicegen.md). The core never calls this itself
// — delivery is out of core scope — but provides the transition so that
// delivery flows do not have to reach the status via persistence-adapter
// snapshots only (issue #159; same defect class as the refunded fix in #99).
//
// Only Finalized invoices can be issued: a draft has not been confirmed, and
// paid / partial_paid / overdue / voided / refunded invoices are already past
// the delivery point. ValidatePayment / RecordPayment accept issued invoices,
// and VoidWithReason can void them, so downstream flows are unchanged.
//
// Like Finalize, it bumps the optimistic-locking version so two concurrently
// loaded copies cannot both be issued: a repository honoring the concurrency
// contract rejects the second Save with tx.ErrVersionConflict (issue #147).
func (inv *Invoice) MarkIssued() error {
	if inv.status != InvoiceStatusFinalized {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot issue invoice in status %s", inv.status))
	}
	inv.status = InvoiceStatusIssued
	inv.version++
	return nil
}

// MarkOverdue transitions the invoice to overdue once its due date has passed.
//
// The caller supplies the current time (via shared.Clock — never time.Now()
// directly) and the transition is only permitted when `now` is strictly after
// the due date; an invoice with no due date set cannot become overdue. The
// core never calls this itself: detecting overdue invoices is a scheduling
// concern (the consumer's dunning batch / scheduler finds candidates, e.g. via
// Repository.FindOverdue, and applies the transition). This closes the gap
// where the overdue status was reachable only via persistence-adapter
// snapshots (issue #159; same defect class as the refunded fix in #99).
//
// Allowed source states are Finalized and Issued — the unpaid, pre-collection
// states. Partial_paid is deliberately NOT transitioned: it already encodes
// that money was collected, and RecordPayment resolves an overdue invoice to
// paid/partial_paid the same way it does a finalized one (ValidatePayment
// accepts overdue), so flipping partial_paid to overdue would lose payment
// state for no downstream benefit.
//
// Like Finalize, it bumps the optimistic-locking version (issue #147).
func (inv *Invoice) MarkOverdue(now time.Time) error {
	if inv.status != InvoiceStatusFinalized && inv.status != InvoiceStatusIssued {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot mark invoice overdue in status %s", inv.status))
	}
	if inv.dueDate.IsZero() {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			"cannot mark invoice overdue: no due date is set")
	}
	if !now.After(inv.dueDate) {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("cannot mark invoice overdue: due date %s has not passed",
				inv.dueDate.Format(time.RFC3339)))
	}
	inv.status = InvoiceStatusOverdue
	inv.version++
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

	// Guard the financial invariant (issue #148): a negative payment would
	// decrease paidAmount, inflate the balance, and could roll a Paid invoice
	// back to partial_paid — this slips through when allowPartialPay is set,
	// because the "leaves a balance" guard below only rejects underpayment, not
	// negative payment. Zero is permitted: a zero-amount invoice (e.g. one fully
	// covered by a discount or credit) is settled by a zero-amount payment, which
	// is exactly what PaymentService.ProcessPayment submits when it defaults the
	// amount to AmountDue(). A currency mismatch is surfaced by paidAmount.Add.
	if amount.IsNegative() {
		return shared.NewDomainError(shared.ErrCodeValidation,
			"payment amount must not be negative")
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

	// Partial-payment opt-in (design-decisions 3.1): unless allowPartialPay is
	// set, a payment must settle the full amount due in one go. A payment that
	// would leave a non-zero balance (newPaid < amountDue) is rejected.
	if !inv.allowPartialPay && inv.amountDue.GreaterThan(newPaid) {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("partial payment not allowed: payment of %s leaves a balance on amount due %s",
				amount.Amount().RatString(), inv.amountDue.Amount().RatString()))
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

	// Bump the optimistic-locking version so two concurrently loaded copies
	// cannot both record a payment: a repository honoring the concurrency
	// contract rejects the second Save with tx.ErrVersionConflict, preventing
	// silent under-reporting of paidAmount (issue #147).
	inv.version++

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

// PaidAt returns a defensive copy of the paid-at timestamp pointer so
// callers cannot mutate the invoice's internal state (see issue #96).
func (inv *Invoice) PaidAt() *time.Time {
	if inv.paidAt == nil {
		return nil
	}
	v := *inv.paidAt
	return &v
}
func (inv *Invoice) Metadata() map[string]string {
	cp := make(map[string]string, len(inv.metadata))
	for k, v := range inv.metadata {
		cp[k] = v
	}
	return cp
}

// IsProration reports whether the invoice is a proration adjustment, tagged via
// MetadataKeyInvoiceType. Proration invoices are exempt from the per-period
// uniqueness constraint documented on Repository.Save because they are designed
// to coexist with the billing period's regular invoice.
func (inv *Invoice) IsProration() bool {
	return inv.metadata[MetadataKeyInvoiceType] == InvoiceTypeProration
}

// WithIssueDate sets the issue date.
func WithIssueDate(t time.Time) InvoiceOption {
	return func(inv *Invoice) {
		inv.issueDate = t
	}
}
func (inv *Invoice) AllowPartialPay() bool { return inv.allowPartialPay }

// Version returns the current optimistic-locking version. It is incremented by
// every state transition that changes persisted state — Finalize, MarkIssued,
// MarkOverdue, RecordPayment, Void, VoidWithReason, MarkRefunded, and the
// revision-chain link setters SetRevisionOf / SetOriginalInvoiceID. See issues
// #130 and #147.
func (inv *Invoice) Version() int { return inv.version }

// LoadedVersion returns the version observed when this invoice was loaded from
// persistence. Repository implementations compare it against the stored version
// on Save to detect a concurrent modification (issue #130).
func (inv *Invoice) LoadedVersion() int { return inv.loadedVersion }

// SetVersion sets the version and records it as the loaded version.
// Repository implementations call this after a successful Save so that
// subsequent saves from the same pointer compare against the just-persisted
// version instead of a stale baseline.
//
// For initial reconstitution from persistence, prefer InvoiceFromSnapshot,
// which restores version and loadedVersion atomically alongside all other
// fields.
func (inv *Invoice) SetVersion(v int) {
	inv.version = v
	inv.loadedVersion = v
}

// Void transitions the invoice to voided status.
// Only Draft and Finalized invoices can be voided.
//
// Like Finalize, it bumps the optimistic-locking version so a concurrent Void
// vs RecordPayment race cannot both persist: the second Save is rejected with
// tx.ErrVersionConflict under a compliant repository (issue #147).
func (inv *Invoice) Void() error {
	if inv.status != InvoiceStatusDraft && inv.status != InvoiceStatusFinalized {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot void invoice in status %s", inv.status))
	}
	inv.status = InvoiceStatusVoided
	inv.version++
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
	// Bump the optimistic-locking version (issue #147): a VoidWithReason racing
	// a RecordPayment on the same loaded version must not silently lose the
	// payment. A compliant repository rejects the second Save.
	inv.version++
	return nil
}

// MarkRefunded transitions the invoice to refunded status with an explicit reason.
//
// This is the in-core mutator that produces the InvoiceStatusRefunded state,
// which was previously reachable only via InvoiceFromSnapshot (persistence
// adapters) — see issue #99. The upstream trigger (a CreditNote fully applied
// to the invoice, a Payment refunded in full via the PaymentGateway, or a
// manual admin action) is deliberately left to the caller: this method only
// performs the in-core state transition and does not orchestrate any
// cross-service refund workflow (issue #99 Non-goals).
//
// Allowed source states are Paid and PartialPaid: a refund only makes sense
// once money has actually been collected against the invoice. Every other
// state (including Voided) is rejected with invalid_state_transition:
//   - Draft / Finalized / Issued / Overdue have no collected payment to refund.
//   - Voided is a distinct terminal state for invoices that were cancelled
//     (typically via void-and-recreate) rather than paid then refunded; a
//     voided invoice never validly collected funds, so voided → refunded is
//     meaningless. This mirrors VoidWithReason, which likewise treats voided
//     and refunded as separate terminal states and refuses to cross between
//     them.
//   - Refunded is already terminal, so this also guards against double refund.
//
// Like Finalize, it bumps the optimistic-locking version so two concurrently
// loaded copies cannot both be refunded: a repository honoring the concurrency
// contract rejects the second Save with tx.ErrVersionConflict (issue #130).
func (inv *Invoice) MarkRefunded(reason string) error {
	if reason == "" {
		return shared.NewDomainError(shared.ErrCodeValidation, "refund reason must not be empty")
	}
	if inv.status != InvoiceStatusPaid && inv.status != InvoiceStatusPartialPaid {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot refund invoice in status %s", inv.status))
	}
	inv.status = InvoiceStatusRefunded
	inv.refundReason = reason
	inv.version++
	return nil
}

// RefundReason returns the reason this invoice was marked refunded.
func (inv *Invoice) RefundReason() string { return inv.refundReason }

// OriginalInvoiceID returns a defensive copy of the original invoice ID
// pointer (for reissued invoices). Mutating the returned pointer does NOT
// affect the invoice's internal state (see issue #96).
func (inv *Invoice) OriginalInvoiceID() *shared.InvoiceID {
	if inv.originalInvoiceID == nil {
		return nil
	}
	v := *inv.originalInvoiceID
	return &v
}

// RevisionOf returns a defensive copy of the revision-parent ID pointer.
// Mutating the returned pointer does NOT affect the invoice's internal
// state (see issue #96).
func (inv *Invoice) RevisionOf() *shared.InvoiceID {
	if inv.revisionOf == nil {
		return nil
	}
	v := *inv.revisionOf
	return &v
}

// VoidReason returns the reason this invoice was voided.
func (inv *Invoice) VoidReason() string { return inv.voidReason }

// SetRevisionOf sets the revision link to the direct parent invoice.
// revisionOf points to the immediate predecessor in the revision chain.
// This is used after invoice creation to link a replacement to its direct parent.
//
// It bumps the optimistic-locking version because it mutates persisted state
// (issue #147): a compliant repository must be able to detect a lost update on
// the revision-link write the same way it does for status transitions.
//
// A self-reference (id == inv.ID()) is rejected as a no-op: an invoice cannot be
// a revision of itself, and linking one would create a cycle in the revision
// chain that breaks chain traversal. The mutation and version bump are skipped
// in that case (issue #162 L-9).
func (inv *Invoice) SetRevisionOf(id shared.InvoiceID) {
	if id == inv.id {
		return
	}
	inv.revisionOf = &id
	inv.version++
}

// SetOriginalInvoiceID sets the root invoice ID of the revision chain.
// originalInvoiceID always points to the first invoice in the chain,
// regardless of how many revisions have occurred.
// This is used after invoice creation to link a replacement to the chain root.
//
// Like SetRevisionOf, it bumps the optimistic-locking version because it
// mutates persisted state (issue #147).
//
// A self-reference (id == inv.ID()) is rejected as a no-op: the chain root is by
// definition an EARLIER invoice, so pointing an invoice's original-link at
// itself would corrupt revision-chain traversal. The mutation and version bump
// are skipped in that case (issue #162 L-9).
func (inv *Invoice) SetOriginalInvoiceID(id shared.InvoiceID) {
	if id == inv.id {
		return
	}
	inv.originalInvoiceID = &id
	inv.version++
}

// PaymentMethodID returns a defensive copy of the invoice-level payment
// method ID pointer. Mutating the returned pointer does NOT affect the
// invoice's internal state (see issue #96).
func (inv *Invoice) PaymentMethodID() *string {
	if inv.paymentMethodID == nil {
		return nil
	}
	v := *inv.paymentMethodID
	return &v
}

// WithPaymentMethodID sets the payment method ID on the invoice.
// The pointer is defensively copied so callers may safely mutate their
// own *string after construction (see issue #96).
func WithPaymentMethodID(id *string) InvoiceOption {
	return func(inv *Invoice) {
		if id == nil {
			inv.paymentMethodID = nil
			return
		}
		v := *id
		inv.paymentMethodID = &v
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

// WithMetadata sets metadata key-value pairs on the invoice.
func WithMetadata(m map[string]string) InvoiceOption {
	return func(inv *Invoice) {
		for k, v := range m {
			inv.metadata[k] = v
		}
	}
}
