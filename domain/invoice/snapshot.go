// Package invoice — snapshot.go
//
// This file implements the Snapshot / Reconstruct pattern for state-based
// persistence adapters. It exposes a flat DTO (InvoiceSnapshot / LineItemSnapshot)
// and dedicated ToSnapshot / FromSnapshot entry points.
//
// DANGER ZONE — PERSISTENCE ADAPTERS ONLY
//
// The types and functions in this file deliberately bypass construction-time
// invariants enforced by NewInvoice / NewLineItem / RecordPayment / etc.
// They exist solely so that persistence adapters (postgres, mysql, mongo, ...)
// can rehydrate entities whose state was already validated in the past.
//
// Application code and domain services MUST NOT use these APIs. Use NewInvoice
// and the state-transition methods (Finalize, RecordPayment, Void, ...) instead.
//
// See issue #94 for the design rationale.

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
// Mutating the returned snapshot does NOT affect the invoice.
//
// For persistence adapters only. See file header warning.
func (inv *Invoice) ToSnapshot() InvoiceSnapshot {
	lineItems := make([]LineItemSnapshot, len(inv.lineItems))
	for i, li := range inv.lineItems {
		liMeta := make(map[string]string, len(li.metadata))
		for k, v := range li.metadata {
			liMeta[k] = v
		}
		lineItems[i] = LineItemSnapshot{
			ID:          li.id,
			Description: li.description,
			Quantity:    li.quantity,
			UnitPrice:   li.unitPrice,
			Amount:      li.amount,
			TaxRate:     li.taxRate,
			PriceID:     li.priceID,
			Metadata:    liMeta,
		}
	}

	metadata := make(map[string]string, len(inv.metadata))
	for k, v := range inv.metadata {
		metadata[k] = v
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
		PaidAt:            inv.paidAt,
		PaymentMethodID:   inv.paymentMethodID,
		AllowPartialPay:   inv.allowPartialPay,
		OriginalInvoiceID: inv.originalInvoiceID,
		RevisionOf:        inv.revisionOf,
		VoidReason:        inv.voidReason,
		Metadata:          metadata,
	}
}

// FromSnapshot reconstructs an Invoice from a persistence snapshot.
//
// This function performs only the minimal validation required to detect
// corrupted DB rows (e.g. empty ID). It deliberately does NOT re-run
// construction-time invariants such as currency matching or total
// recalculation — those were validated when the invoice was first created
// and may have been computed under rules that have since changed.
//
// For persistence adapters only. See file header warning.
func FromSnapshot(s InvoiceSnapshot) (*Invoice, error) {
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
		lineItems[i] = LineItem{
			id:          lis.ID,
			description: lis.Description,
			quantity:    lis.Quantity,
			unitPrice:   lis.UnitPrice,
			amount:      lis.Amount,
			taxRate:     lis.TaxRate,
			priceID:     lis.PriceID,
			metadata:    liMeta,
		}
	}

	metadata := make(map[string]string, len(s.Metadata))
	for k, v := range s.Metadata {
		metadata[k] = v
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
		paidAt:            s.PaidAt,
		paymentMethodID:   s.PaymentMethodID,
		allowPartialPay:   s.AllowPartialPay,
		originalInvoiceID: s.OriginalInvoiceID,
		revisionOf:        s.RevisionOf,
		voidReason:        s.VoidReason,
		metadata:          metadata,
	}, nil
}
