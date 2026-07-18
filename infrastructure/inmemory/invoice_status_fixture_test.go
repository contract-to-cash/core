package inmemory

import (
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
)

// newTestInvoiceInStatus builds a test invoice (via newTestInvoice) and drives
// it to the target status through the real state-transition methods —
// invoice.WithStatus accepts only Draft (issue #238).
//
// Status-specific construction needs are added automatically before the
// caller's opts (so callers can still override them): Overdue gets a default
// past due date, PartialPaid enables partial payment.
func newTestInvoiceInStatus(t *testing.T, accountID shared.AccountID, contractID shared.ContractID, status invoice.InvoiceStatus, opts ...invoice.InvoiceOption) *invoice.Invoice {
	t.Helper()
	var pre []invoice.InvoiceOption
	switch status {
	case invoice.InvoiceStatusOverdue:
		pre = append(pre, invoice.WithDueDate(time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)))
	case invoice.InvoiceStatusPartialPaid:
		pre = append(pre, invoice.WithAllowPartialPayment(true))
	default:
		// no extra construction options required
	}
	inv := newTestInvoice(t, accountID, contractID, append(pre, opts...)...)

	step := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("newTestInvoiceInStatus(%s): %v", status, err)
		}
	}
	paidAt := time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC)
	switch status {
	case invoice.InvoiceStatusDraft:
		// already there
	case invoice.InvoiceStatusFinalized:
		step(inv.Finalize())
	case invoice.InvoiceStatusIssued:
		step(inv.Finalize())
		step(inv.MarkIssued())
	case invoice.InvoiceStatusOverdue:
		step(inv.Finalize())
		step(inv.MarkOverdue(inv.DueDate().Add(24 * time.Hour)))
	case invoice.InvoiceStatusPaid:
		step(inv.Finalize())
		step(inv.RecordPayment(inv.AmountDue(), paidAt))
	case invoice.InvoiceStatusVoided:
		step(inv.Void())
	default:
		t.Fatalf("newTestInvoiceInStatus: unsupported target status %s (extend this helper with the real transition chain)", status)
	}
	return inv
}
