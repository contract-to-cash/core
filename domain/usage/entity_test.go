package usage

import (
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestNewUsageRecord(t *testing.T) {
	id := shared.NewUsageRecordID()
	contractID := shared.NewContractID()
	metricName := "api_calls"
	quantity := int64(150)
	ts := time.Now().UTC()
	idempotencyKey := "idem_abc123"

	r := NewUsageRecord(id, contractID, metricName, quantity, ts, idempotencyKey)

	if r.ID() != id {
		t.Errorf("expected id %s, got %s", id, r.ID())
	}
	if r.ContractID() != contractID {
		t.Errorf("expected contractID %s, got %s", contractID, r.ContractID())
	}
	if r.MetricName() != metricName {
		t.Errorf("expected metric %s, got %s", metricName, r.MetricName())
	}
	if r.Quantity() != quantity {
		t.Errorf("expected quantity %d, got %d", quantity, r.Quantity())
	}
	if r.Timestamp() != ts {
		t.Errorf("expected timestamp %v, got %v", ts, r.Timestamp())
	}
	if r.IdempotencyKey() != idempotencyKey {
		t.Errorf("expected idempotency key %s, got %s", idempotencyKey, r.IdempotencyKey())
	}
	if r.Metadata() == nil {
		t.Error("expected metadata to be initialized")
	}
}

func TestUsageSummary(t *testing.T) {
	contractID := shared.NewContractID()
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	period, err := shared.NewDateRange(start, end)
	if err != nil {
		t.Fatalf("unexpected error creating date range: %v", err)
	}

	summary := &UsageSummary{
		ContractID: contractID,
		MetricName: "api_calls",
		Period:     period,
		TotalUsage: 42000,
	}

	if summary.ContractID != contractID {
		t.Errorf("expected contractID %s, got %s", contractID, summary.ContractID)
	}
	if summary.MetricName != "api_calls" {
		t.Errorf("expected metric api_calls, got %s", summary.MetricName)
	}
	if summary.TotalUsage != 42000 {
		t.Errorf("expected total usage 42000, got %d", summary.TotalUsage)
	}
	if summary.Period.Start() != start {
		t.Errorf("expected period start %v, got %v", start, summary.Period.Start())
	}
}
