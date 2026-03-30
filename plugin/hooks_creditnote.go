package plugin

import "github.com/contract-to-cash/core/domain/invoice"

// OnCreditNoteIssuedHook is called when a credit note is issued.
type OnCreditNoteIssuedHook interface {
	Plugin
	OnCreditNoteIssued(ctx *Context, creditNote *invoice.CreditNote) error
}

// OnInvoiceRevisedHook is called when an invoice is voided and a replacement is created.
type OnInvoiceRevisedHook interface {
	Plugin
	OnInvoiceRevised(ctx *Context, original *invoice.Invoice, replacement *invoice.Invoice) error
}
