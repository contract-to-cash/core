package service

import (
	"fmt"

	"github.com/contract-to-cash/core/domain/invoice"
)

// transitionInvoiceForTest drives a freshly constructed draft invoice to the
// target status via the real state-transition methods — invoice.WithStatus
// accepts only Draft (issue #238), so fixtures must exercise the legitimate
// transitions. It panics on a transition error so it is usable both from
// tests and from fixture helpers that have no *testing.T.
func transitionInvoiceForTest(inv *invoice.Invoice, status invoice.InvoiceStatus) *invoice.Invoice {
	var err error
	switch status {
	case invoice.InvoiceStatusDraft:
		// already there
	case invoice.InvoiceStatusFinalized:
		err = inv.Finalize()
	case invoice.InvoiceStatusVoided:
		err = inv.Void()
	default:
		err = fmt.Errorf("unsupported target status %s (extend this helper with the real transition chain if a fixture needs it)", status)
	}
	if err != nil {
		panic(fmt.Sprintf("transitionInvoiceForTest(%s): %v", status, err))
	}
	return inv
}
