package plugin

import (
	"time"

	"github.com/contract-to-cash/core/domain/invoice"
)

// InvoiceDocument represents a rendered invoice document.
// This is a simplified version; detailed fields will be defined in Phase 4.
type InvoiceDocument struct {
	InvoiceID     string
	InvoiceNumber string
}

// DeliveryResult represents the result of delivering an invoice document.
type DeliveryResult struct {
	DeliveryID string
	Status     string
	SentAt     *time.Time
	Error      *string
}

// InvoiceGenerationHook is implemented by plugins that participate in invoice document generation.
type InvoiceGenerationHook interface {
	Plugin
	BuildDocument(ctx *Context, invoice *invoice.Invoice, doc *InvoiceDocument) error
	AfterRender(ctx *Context, doc *InvoiceDocument, rendered []byte) error
	AfterDelivery(ctx *Context, doc *InvoiceDocument, result *DeliveryResult) error
}
