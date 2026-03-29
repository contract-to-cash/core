package payment

import (
	"context"

	"github.com/contract-to-cash/core/domain/shared"
)

// Repository defines the persistence interface for payments.
type Repository interface {
	Save(ctx context.Context, payment *Payment) error
	FindByID(ctx context.Context, id shared.PaymentID) (*Payment, error)
	FindByInvoiceID(ctx context.Context, invoiceID shared.InvoiceID) ([]*Payment, error)
	// FindByIdempotencyKey returns a payment with the given idempotency key,
	// or nil if not found. Used to prevent duplicate payment records on retry.
	FindByIdempotencyKey(ctx context.Context, key string) (*Payment, error)
}
