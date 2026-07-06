// Package invoice — credit_note_snapshot.go
//
// Snapshot / Reconstruct pattern for CreditNote. See snapshot.go in this
// package for the full design rationale, the distinction from
// ContractAggregate.MarshalSnapshot, and the persistence-adapters-only warning.

package invoice

import (
	"math/big"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// CreditNoteItemSnapshot is the flat persistence representation of a CreditNoteItem.
type CreditNoteItemSnapshot struct {
	InvoiceLineItemID string
	Description       string
	Amount            shared.Money
	TaxRate           *big.Rat
	TaxAmount         shared.Money
}

// CreditNoteSnapshot is the flat persistence representation of a CreditNote.
//
// WARNING: This type bypasses NewCreditNote's invariants (non-empty items,
// subtotal/tax recalculation, etc.). Use ONLY in persistence adapters.
type CreditNoteSnapshot struct {
	ID           shared.CreditNoteID
	Number       string
	InvoiceID    shared.InvoiceID
	AccountID    shared.AccountID
	ContractID   shared.ContractID
	Status       CreditNoteStatus
	Reason       CreditNoteReason
	Memo         string
	Items        []CreditNoteItemSnapshot
	Subtotal     shared.Money
	TaxAmount    shared.Money
	Total        shared.Money
	CreditAmount shared.Money
	RefundAmount shared.Money
	IssuedAt     *time.Time
	CreatedAt    time.Time
	// Version is the optimistic-locking version (issue #147). The entity's
	// loadedVersion is not stored separately: CreditNoteFromSnapshot restores
	// both version and loadedVersion from this single field.
	Version int
}

// ToSnapshot returns a flat, independent copy of the credit note's internal state.
// Mutating the returned snapshot (including its nested pointer and map
// fields) does NOT affect the credit note at the Snapshot boundary.
//
// For persistence adapters only.
func (cn *CreditNote) ToSnapshot() CreditNoteSnapshot {
	items := make([]CreditNoteItemSnapshot, len(cn.items))
	for i, it := range cn.items {
		// Deep-copy *big.Rat so snapshot mutations do not leak into the entity.
		var taxRate *big.Rat
		if it.taxRate != nil {
			taxRate = new(big.Rat).Set(it.taxRate)
		}
		items[i] = CreditNoteItemSnapshot{
			InvoiceLineItemID: it.invoiceLineItemID,
			Description:       it.description,
			Amount:            it.amount,
			TaxRate:           taxRate,
			TaxAmount:         it.taxAmount,
		}
	}

	var issuedAt *time.Time
	if cn.issuedAt != nil {
		v := *cn.issuedAt
		issuedAt = &v
	}

	return CreditNoteSnapshot{
		ID:           cn.id,
		Number:       cn.number,
		InvoiceID:    cn.invoiceID,
		AccountID:    cn.accountID,
		ContractID:   cn.contractID,
		Status:       cn.status,
		Reason:       cn.reason,
		Memo:         cn.memo,
		Items:        items,
		Subtotal:     cn.subtotal,
		TaxAmount:    cn.taxAmount,
		Total:        cn.total,
		CreditAmount: cn.creditAmount,
		RefundAmount: cn.refundAmount,
		IssuedAt:     issuedAt,
		CreatedAt:    cn.createdAt,
		Version:      cn.version,
	}
}

// CreditNoteFromSnapshot reconstructs a CreditNote from a persistence snapshot.
//
// Performs only the minimal validation required to detect corrupted DB rows
// (non-empty ID). Does NOT re-run NewCreditNote's "must have at least one
// item" invariant, because an adapter may legitimately load items from a
// separate child table after the parent row.
//
// For persistence adapters only.
func CreditNoteFromSnapshot(s CreditNoteSnapshot) (*CreditNote, error) {
	if s.ID == "" {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"credit note snapshot: ID must not be empty")
	}

	items := make([]CreditNoteItem, len(s.Items))
	for i, is := range s.Items {
		// Deep-copy *big.Rat so adapters that retain the snapshot after
		// calling CreditNoteFromSnapshot cannot corrupt the reconstructed entity.
		var taxRate *big.Rat
		if is.TaxRate != nil {
			taxRate = new(big.Rat).Set(is.TaxRate)
		}
		items[i] = CreditNoteItem{
			invoiceLineItemID: is.InvoiceLineItemID,
			description:       is.Description,
			amount:            is.Amount,
			taxRate:           taxRate,
			taxAmount:         is.TaxAmount,
		}
	}

	var issuedAt *time.Time
	if s.IssuedAt != nil {
		v := *s.IssuedAt
		issuedAt = &v
	}

	return &CreditNote{
		id:           s.ID,
		number:       s.Number,
		invoiceID:    s.InvoiceID,
		accountID:    s.AccountID,
		contractID:   s.ContractID,
		status:       s.Status,
		reason:       s.Reason,
		memo:         s.Memo,
		items:        items,
		subtotal:     s.Subtotal,
		taxAmount:    s.TaxAmount,
		total:        s.Total,
		creditAmount: s.CreditAmount,
		refundAmount: s.RefundAmount,
		issuedAt:     issuedAt,
		createdAt:    s.CreatedAt,
		// loadedVersion is set to Version so the next Save through a repository
		// with optimistic locking compares against the correct baseline (#147).
		version:       s.Version,
		loadedVersion: s.Version,
	}, nil
}
