package invoice

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// fixturePaidAt is the fixed timestamp used when fixture transitions record a
// payment (shared.Clock convention: tests use fixed times, never time.Now()).
var fixturePaidAt = time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

// advanceInvoiceTo drives a freshly constructed draft invoice to the target
// status via the real state-transition methods. Since WithStatus accepts only
// Draft (issue #238), fixtures must exercise the legitimate transitions:
//
//	finalized:    Finalize
//	issued:       Finalize → MarkIssued
//	overdue:      Finalize → MarkOverdue (the invoice must be constructed with
//	              a due date, e.g. WithDueDate)
//	paid:         Finalize → RecordPayment(full amount due)
//	partial_paid: Finalize → RecordPayment(half) (the invoice must be
//	              constructed with WithAllowPartialPayment(true))
//	voided:       Void (from draft)
//	refunded:     Finalize → RecordPayment(full) → MarkRefunded
func advanceInvoiceTo(t *testing.T, inv *Invoice, status InvoiceStatus) {
	t.Helper()
	step := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("advanceInvoiceTo(%s): %v", status, err)
		}
	}
	switch status {
	case InvoiceStatusDraft:
		// already there
	case InvoiceStatusFinalized:
		step(inv.Finalize())
	case InvoiceStatusIssued:
		step(inv.Finalize())
		step(inv.MarkIssued())
	case InvoiceStatusOverdue:
		step(inv.Finalize())
		step(inv.MarkOverdue(inv.DueDate().Add(24 * time.Hour)))
	case InvoiceStatusPaid:
		step(inv.Finalize())
		step(inv.RecordPayment(inv.AmountDue(), fixturePaidAt))
	case InvoiceStatusPartialPaid:
		step(inv.Finalize())
		step(inv.RecordPayment(inv.AmountDue().Multiply(big.NewRat(1, 2)), fixturePaidAt))
	case InvoiceStatusVoided:
		step(inv.Void())
	case InvoiceStatusRefunded:
		step(inv.Finalize())
		step(inv.RecordPayment(inv.AmountDue(), fixturePaidAt))
		step(inv.MarkRefunded("fixture refund"))
	}
}

// fixtureOptionsFor returns the construction options a fixture needs so that
// advanceInvoiceTo can reach the target status: overdue needs a due date,
// partial_paid needs partial payment enabled. All other statuses need nothing.
func fixtureOptionsFor(status InvoiceStatus, due time.Time) []InvoiceOption {
	var opts []InvoiceOption
	switch status {
	case InvoiceStatusOverdue:
		opts = append(opts, WithDueDate(due))
	case InvoiceStatusPartialPaid:
		opts = append(opts, WithAllowPartialPayment(true))
	case InvoiceStatusDraft, InvoiceStatusFinalized, InvoiceStatusIssued,
		InvoiceStatusPaid, InvoiceStatusVoided, InvoiceStatusRefunded:
		// no extra construction options required
	}
	return opts
}

// TestNewInvoice_WithStatus_NonDraftRejected pins the issue #238 tightening:
// WithStatus accepts only Draft; any other status makes NewInvoice fail with a
// validation DomainError instead of constructing an inconsistent entity (e.g.
// a "paid" invoice with paidAmount=0).
func TestNewInvoice_WithStatus_NonDraftRejected(t *testing.T) {
	nonDraft := []InvoiceStatus{
		InvoiceStatusFinalized,
		InvoiceStatusIssued,
		InvoiceStatusPaid,
		InvoiceStatusPartialPaid,
		InvoiceStatusOverdue,
		InvoiceStatusVoided,
		InvoiceStatusRefunded,
		InvoiceStatus("bogus"),
	}
	for _, status := range nonDraft {
		t.Run(string(status), func(t *testing.T) {
			_, err := NewInvoice(
				shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
				shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
				shared.Zero(shared.CurrencyJPY),
				shared.Zero(shared.CurrencyJPY),
				WithStatus(status),
			)
			assertDomainCode(t, err, shared.ErrCodeValidation)
		})
	}
}

// TestNewInvoice_WithStatus_DraftAccepted pins that the explicit Draft form —
// the only one the core billing pipeline uses — keeps working.
func TestNewInvoice_WithStatus_DraftAccepted(t *testing.T) {
	inv := mustNewInvoice(t,
		shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
		shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		WithStatus(InvoiceStatusDraft),
	)
	if inv.Status() != InvoiceStatusDraft {
		t.Errorf("expected draft, got %s", inv.Status())
	}
}
