package usage

import (
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestMetricName_TypeSafety(t *testing.T) {
	// MetricName is a typed string assignable from string literals
	var m shared.MetricName = "api_calls"
	if m.String() != "api_calls" {
		t.Errorf("expected 'api_calls', got %q", m.String())
	}

	// Verify typed MetricName is used in UsageRecord
	id := shared.NewUsageRecordID()
	contractID := shared.NewContractID()
	r, err := NewUsageRecord(id, contractID, "storage_gb", 100, time.Now().UTC(), "idem_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.MetricName() != "storage_gb" {
		t.Errorf("expected metric 'storage_gb', got %q", r.MetricName())
	}

	// Verify typed MetricName is used in UsageSummary
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	period, _ := shared.NewDateRange(start, end)
	summary := &UsageSummary{
		ContractID: contractID,
		MetricName: "api_calls",
		Period:     period,
		TotalUsage: 500,
	}
	if summary.MetricName != "api_calls" {
		t.Errorf("expected MetricName 'api_calls', got %q", summary.MetricName)
	}
}

func TestNewUsageRecord(t *testing.T) {
	id := shared.NewUsageRecordID()
	contractID := shared.NewContractID()
	metricName := shared.MetricName("api_calls")
	quantity := int64(150)
	ts := time.Now().UTC()
	idempotencyKey := "idem_abc123"

	r, err := NewUsageRecord(id, contractID, metricName, quantity, ts, idempotencyKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

func TestNewUsageRecord_NegativeQuantity(t *testing.T) {
	_, err := NewUsageRecord(
		shared.NewUsageRecordID(),
		shared.NewContractID(),
		"api_calls",
		-1,
		time.Now().UTC(),
		"idem_neg",
	)
	if err == nil {
		t.Fatal("expected error for negative quantity, got nil")
	}
}

func TestNewUsageRecord_ZeroQuantity(t *testing.T) {
	r, err := NewUsageRecord(
		shared.NewUsageRecordID(),
		shared.NewContractID(),
		"api_calls",
		0,
		time.Now().UTC(),
		"idem_zero",
	)
	if err != nil {
		t.Fatalf("unexpected error for zero quantity: %v", err)
	}
	if r.Quantity() != 0 {
		t.Errorf("expected quantity 0, got %d", r.Quantity())
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
