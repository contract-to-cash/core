package usage

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// UsageRecord represents a single usage event.
type UsageRecord struct {
	id             shared.UsageRecordID
	contractID     shared.ContractID
	metricName     string
	quantity       int64
	timestamp      time.Time
	metadata       map[string]string
	idempotencyKey string
}

// NewUsageRecord creates a new UsageRecord.
func NewUsageRecord(
	id shared.UsageRecordID,
	contractID shared.ContractID,
	metricName string,
	quantity int64,
	timestamp time.Time,
	idempotencyKey string,
) *UsageRecord {
	return &UsageRecord{
		id:             id,
		contractID:     contractID,
		metricName:     metricName,
		quantity:       quantity,
		timestamp:      timestamp,
		metadata:       make(map[string]string),
		idempotencyKey: idempotencyKey,
	}
}

// --- Getters ---

func (r *UsageRecord) ID() shared.UsageRecordID      { return r.id }
func (r *UsageRecord) ContractID() shared.ContractID { return r.contractID }
func (r *UsageRecord) MetricName() string            { return r.metricName }
func (r *UsageRecord) Quantity() int64               { return r.quantity }
func (r *UsageRecord) Timestamp() time.Time          { return r.timestamp }
func (r *UsageRecord) Metadata() map[string]string {
	cp := make(map[string]string, len(r.metadata))
	for k, v := range r.metadata {
		cp[k] = v
	}
	return cp
}
func (r *UsageRecord) IdempotencyKey() string { return r.idempotencyKey }

// UsageSummary represents aggregated usage for a metric over a period.
type UsageSummary struct {
	ContractID shared.ContractID
	MetricName string
	Period     shared.DateRange
	TotalUsage int64
}
