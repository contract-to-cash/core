package usage

import (
	"context"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Repository defines the persistence interface for usage records.
type Repository interface {
	Record(ctx context.Context, record *UsageRecord) error
	GetSummary(ctx context.Context, contractID shared.ContractID, metric string, period shared.DateRange) (*UsageSummary, error)
	GetRecords(ctx context.Context, contractID shared.ContractID, metric string, from, to time.Time) ([]*UsageRecord, error)
}
