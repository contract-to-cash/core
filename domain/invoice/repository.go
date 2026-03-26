package invoice

import (
	"context"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Repository defines the persistence interface for invoices.
type Repository interface {
	Save(ctx context.Context, invoice *Invoice) error
	FindByID(ctx context.Context, id shared.InvoiceID) (*Invoice, error)
	FindByContractID(ctx context.Context, contractID shared.ContractID) ([]*Invoice, error)
	FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*Invoice, error)
	FindOverdue(ctx context.Context) ([]*Invoice, error)
	FindByStatus(ctx context.Context, status InvoiceStatus) ([]*Invoice, error)
	FindByIDAsOf(ctx context.Context, id shared.InvoiceID, asOf time.Time) (*Invoice, error)
}
