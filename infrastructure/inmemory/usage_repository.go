package inmemory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/domain/usage"
)

// Compile-time interface check.
var _ usage.Repository = (*InMemoryUsageRepository)(nil)

// InMemoryUsageRepository is an in-memory implementation of usage.Repository.
type InMemoryUsageRepository struct {
	mu              sync.RWMutex
	records         []*usage.UsageRecord
	idempotencyKeys map[string]struct{}
}

// NewInMemoryUsageRepository creates a new InMemoryUsageRepository.
func NewInMemoryUsageRepository() *InMemoryUsageRepository {
	return &InMemoryUsageRepository{
		idempotencyKeys: make(map[string]struct{}),
	}
}

// Record persists a usage record.
func (r *InMemoryUsageRepository) Record(_ context.Context, record *usage.UsageRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if record.IdempotencyKey() != "" {
		if _, exists := r.idempotencyKeys[record.IdempotencyKey()]; exists {
			return shared.NewDomainError(shared.ErrCodeDuplicateRequest,
				fmt.Sprintf("duplicate usage record with idempotency key %s", record.IdempotencyKey()))
		}
		r.idempotencyKeys[record.IdempotencyKey()] = struct{}{}
	}

	r.records = append(r.records, record)
	return nil
}

// GetSummary returns aggregated usage for a metric over a period.
func (r *InMemoryUsageRepository) GetSummary(_ context.Context, contractID shared.ContractID, metric shared.MetricName, period shared.DateRange) (*usage.UsageSummary, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var total int64
	for _, rec := range r.records {
		if rec.ContractID() == contractID && rec.MetricName() == metric {
			if period.Contains(rec.Timestamp()) {
				total += rec.Quantity()
			}
		}
	}

	return &usage.UsageSummary{
		ContractID: contractID,
		MetricName: metric,
		Period:     period,
		TotalUsage: total,
	}, nil
}

// GetRecords returns usage records for a contract/metric within a time range.
func (r *InMemoryUsageRepository) GetRecords(_ context.Context, contractID shared.ContractID, metric shared.MetricName, from, to time.Time) ([]*usage.UsageRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*usage.UsageRecord
	for _, rec := range r.records {
		if rec.ContractID() == contractID && rec.MetricName() == metric {
			if !rec.Timestamp().Before(from) && rec.Timestamp().Before(to) {
				result = append(result, rec)
			}
		}
	}
	return result, nil
}
