// Package invoice — snapshot.go
//
// This file implements the Snapshot / Reconstruct pattern for state-based
// persistence adapters. It exposes a flat DTO (InvoiceSnapshot / LineItemSnapshot)
// and dedicated ToSnapshot / InvoiceFromSnapshot entry points. CreditNote uses
// CreditNoteFromSnapshot, defined in credit_note_snapshot.go in this package.
//
// # Relation to ContractAggregate
//
// ContractAggregate is event-sourced and uses MarshalSnapshot() ([]byte, error)
// + LoadFromSnapshot(eventstore.Snapshot) for event-store-level snapshotting.
// That pattern serializes to a byte slice stored inside eventstore.Snapshot.State
// and is coupled to the eventstore package.
//
// State-based entities (Invoice, CreditNote, Payment, BalanceEntry, UsageRecord,
// Product, Price) are NOT event-sourced. They use ToSnapshot() / FromSnapshot(s)
// which return a typed struct that adapters can map directly to DB columns.
//
// The two patterns coexist: each entity uses whichever matches its persistence
// model. Do not mix: do not add ToSnapshot to ContractAggregate, and do not
// add MarshalSnapshot to state-based entities.
//
// # DANGER ZONE — PERSISTENCE ADAPTERS ONLY
//
// The types and functions in this file deliberately bypass construction-time
// invariants enforced by NewInvoice / NewLineItem / RecordPayment / etc.
// They exist solely so that persistence adapters (postgres, mysql, mongo, ...)
// can rehydrate entities whose state was already validated in the past.
//
// Application code and domain services MUST NOT use these APIs. Use NewInvoice
// and the state-transition methods (Finalize, RecordPayment, Void, ...) instead.
//
// This scope is enforced in CI: the forbidigo rule in .golangci.yml blocks
// calls to ToSnapshot / InvoiceFromSnapshot / CreditNoteFromSnapshot from any
// path outside domain/*/snapshot*.go, infrastructure/, and tests/integration/.
// See issue #100.
//
// # Pointer isolation
//
// ToSnapshot / InvoiceFromSnapshot deep-copy pointer fields (*big.Rat, *time.Time,
// *string, *shared.InvoiceID) so that mutations to the snapshot do not leak
// into the entity (and vice versa). The entity's own getters (e.g.
// LineItem.TaxRate(), Invoice.PaidAt()) also defensively copy returned
// pointers — see issue #96.
//
// # Map normalization
//
// To keep the reconstructed entity safe for downstream callers that assume
// maps are non-nil, ToSnapshot / InvoiceFromSnapshot always allocate empty maps
// (make(map[string]string, 0)) when the source map is nil. Round-tripping a
// nil metadata map therefore yields a non-nil empty map. This matches the
// behavior of NewInvoice (which also initializes metadata to an empty map)
// and avoids nil-pointer surprises in adapter code.
//
// See issue #94 for the overall design rationale.

package invoice

import (
	"math/big"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// LineItemSnapshot is the flat persistence representation of a LineItem.
// All fields are exported so adapters can map them directly to DB columns.
type LineItemSnapshot struct {
	ID          string
	Description string
	Quantity    int64
	UnitPrice   shared.Money
	Amount      shared.Money
	TaxRate     *big.Rat
	PriceID     shared.PriceID
	Metadata    map[string]string
}

// InvoiceSnapshot is the flat persistence representation of an Invoice.
// All fields are exported so adapters can map them directly to DB columns.
//
// WARNING: This type bypasses NewInvoice's invariants (currency matching,
// automatic total recalculation, etc.). Use ONLY in persistence adapters.
type InvoiceSnapshot struct {
	ID                shared.InvoiceID
	InvoiceNumber     string
	AccountID         shared.AccountID
	ContractID        shared.ContractID
	LineItems         []LineItemSnapshot
	Subtotal          shared.Money
	TaxAmount         shared.Money
	DiscountAmount    shared.Money
	Total             shared.Money
	AppliedBalance    shared.Money
	AmountDue         shared.Money
	PaidAmount        shared.Money
	Balance           shared.Money
	Status            InvoiceStatus
	BillingPeriod     shared.DateRange
	IssueDate         time.Time
	DueDate           time.Time
	PaidAt            *time.Time
	PaymentMethodID   *string
	AllowPartialPay   bool
	OriginalInvoiceID *shared.InvoiceID
	RevisionOf        *shared.InvoiceID
	VoidReason        string
	Metadata          map[string]string
}

// ToSnapshot returns a flat, independent copy of the invoice's internal state.
// Mutating the returned snapshot (including its nested pointer and map
// fields) does NOT affect the invoice at the Snapshot boundary. The entity's
// own getters also defensively copy pointer fields (see issue #96), so the
// two isolation boundaries compose.
//
// For persistence adapters only. See file header warning.
func (inv *Invoice) ToSnapshot() InvoiceSnapshot {
	lineItems := make([]LineItemSnapshot, len(inv.lineItems))
	for i, li := range inv.lineItems {
		liMeta := make(map[string]string, len(li.metadata))
		for k, v := range li.metadata {
			liMeta[k] = v
		}
		// Deep-copy *big.Rat so snapshot mutations do not leak into the entity.
		var taxRate *big.Rat
		if li.taxRate != nil {
			taxRate = new(big.Rat).Set(li.taxRate)
		}
		lineItems[i] = LineItemSnapshot{
			ID:          li.id,
			Description: li.description,
			Quantity:    li.quantity,
			UnitPrice:   li.unitPrice,
			Amount:      li.amount,
			TaxRate:     taxRate,
			PriceID:     li.priceID,
			Metadata:    liMeta,
		}
	}

	metadata := make(map[string]string, len(inv.metadata))
	for k, v := range inv.metadata {
		metadata[k] = v
	}

	// Deep-copy pointer fields: dereference, then take the address of the
	// local copy so that mutations via the snapshot do not reach the entity.
	var paidAt *time.Time
	if inv.paidAt != nil {
		v := *inv.paidAt
		paidAt = &v
	}
	var paymentMethodID *string
	if inv.paymentMethodID != nil {
		v := *inv.paymentMethodID
		paymentMethodID = &v
	}
	var originalInvoiceID *shared.InvoiceID
	if inv.originalInvoiceID != nil {
		v := *inv.originalInvoiceID
		originalInvoiceID = &v
	}
	var revisionOf *shared.InvoiceID
	if inv.revisionOf != nil {
		v := *inv.revisionOf
		revisionOf = &v
	}

	return InvoiceSnapshot{
		ID:                inv.id,
		InvoiceNumber:     inv.invoiceNumber,
		AccountID:         inv.accountID,
		ContractID:        inv.contractID,
		LineItems:         lineItems,
		Subtotal:          inv.subtotal,
		TaxAmount:         inv.taxAmount,
		DiscountAmount:    inv.discountAmount,
		Total:             inv.total,
		AppliedBalance:    inv.appliedBalance,
		AmountDue:         inv.amountDue,
		PaidAmount:        inv.paidAmount,
		Balance:           inv.balance,
		Status:            inv.status,
		BillingPeriod:     inv.billingPeriod,
		IssueDate:         inv.issueDate,
		DueDate:           inv.dueDate,
		PaidAt:            paidAt,
		PaymentMethodID:   paymentMethodID,
		AllowPartialPay:   inv.allowPartialPay,
		OriginalInvoiceID: originalInvoiceID,
		RevisionOf:        revisionOf,
		VoidReason:        inv.voidReason,
		Metadata:          metadata,
	}
}

// InvoiceFromSnapshot reconstructs an Invoice from a persistence snapshot.
//
// This function performs only the minimal validation required to detect
// corrupted DB rows (e.g. empty ID). It deliberately does NOT re-run
// construction-time invariants such as currency matching or total
// recalculation — those were validated when the invoice was first created
// and may have been computed under rules that have since changed.
//
// Named with the Invoice prefix (rather than a bare FromSnapshot) so that it
// can coexist with CreditNoteFromSnapshot in the same package without
// ambiguity.
//
// For persistence adapters only. See file header warning.
func InvoiceFromSnapshot(s InvoiceSnapshot) (*Invoice, error) {
	if s.ID == "" {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"invoice snapshot: ID must not be empty")
	}

	lineItems := make([]LineItem, len(s.LineItems))
	for i, lis := range s.LineItems {
		liMeta := make(map[string]string, len(lis.Metadata))
		for k, v := range lis.Metadata {
			liMeta[k] = v
		}
		// Deep-copy *big.Rat so adapters that retain the snapshot after
		// calling InvoiceFromSnapshot cannot corrupt the reconstructed entity.
		var taxRate *big.Rat
		if lis.TaxRate != nil {
			taxRate = new(big.Rat).Set(lis.TaxRate)
		}
		lineItems[i] = LineItem{
			id:          lis.ID,
			description: lis.Description,
			quantity:    lis.Quantity,
			unitPrice:   lis.UnitPrice,
			amount:      lis.Amount,
			taxRate:     taxRate,
			priceID:     lis.PriceID,
			metadata:    liMeta,
		}
	}

	metadata := make(map[string]string, len(s.Metadata))
	for k, v := range s.Metadata {
		metadata[k] = v
	}

	// Deep-copy pointer fields: dereference, then take the address of the
	// local copy. This protects the entity from subsequent mutations to the
	// original snapshot (e.g. an adapter that logs the snapshot after use).
	var paidAt *time.Time
	if s.PaidAt != nil {
		v := *s.PaidAt
		paidAt = &v
	}
	var paymentMethodID *string
	if s.PaymentMethodID != nil {
		v := *s.PaymentMethodID
		paymentMethodID = &v
	}
	var originalInvoiceID *shared.InvoiceID
	if s.OriginalInvoiceID != nil {
		v := *s.OriginalInvoiceID
		originalInvoiceID = &v
	}
	var revisionOf *shared.InvoiceID
	if s.RevisionOf != nil {
		v := *s.RevisionOf
		revisionOf = &v
	}

	return &Invoice{
		id:                s.ID,
		invoiceNumber:     s.InvoiceNumber,
		accountID:         s.AccountID,
		contractID:        s.ContractID,
		lineItems:         lineItems,
		subtotal:          s.Subtotal,
		taxAmount:         s.TaxAmount,
		discountAmount:    s.DiscountAmount,
		total:             s.Total,
		appliedBalance:    s.AppliedBalance,
		amountDue:         s.AmountDue,
		paidAmount:        s.PaidAmount,
		balance:           s.Balance,
		status:            s.Status,
		billingPeriod:     s.BillingPeriod,
		issueDate:         s.IssueDate,
		dueDate:           s.DueDate,
		paidAt:            paidAt,
		paymentMethodID:   paymentMethodID,
		allowPartialPay:   s.AllowPartialPay,
		originalInvoiceID: originalInvoiceID,
		revisionOf:        revisionOf,
		voidReason:        s.VoidReason,
		metadata:          metadata,
	}, nil
}
