package inmemory

import (
	"context"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/domain/usage"
)

func mustNewUsageRecord(t *testing.T, contractID shared.ContractID, metric shared.MetricName, quantity int64, ts time.Time, idempotencyKey string) *usage.UsageRecord {
	t.Helper()
	rec, err := usage.NewUsageRecord(
		shared.NewUsageRecordID(),
		contractID,
		metric,
		quantity,
		ts,
		idempotencyKey,
	)
	if err != nil {
		t.Fatalf("NewUsageRecord failed: %v", err)
	}
	return rec
}

func TestInMemoryUsageRepository_RecordAndGetRecords(t *testing.T) {
	repo := NewInMemoryUsageRepository()
	ctx := context.Background()

	contractID := shared.NewContractID()
	metric := shared.MetricName("api_calls")

	ts1 := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ts2 := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	ts3 := time.Date(2026, 2, 5, 12, 0, 0, 0, time.UTC)

	rec1 := mustNewUsageRecord(t, contractID, metric, 100, ts1, "key-1")
	rec2 := mustNewUsageRecord(t, contractID, metric, 200, ts2, "key-2")
	rec3 := mustNewUsageRecord(t, contractID, metric, 50, ts3, "key-3")

	for _, rec := range []*usage.UsageRecord{rec1, rec2, rec3} {
		if err := repo.Record(ctx, rec); err != nil {
			t.Fatalf("Record failed: %v", err)
		}
	}

	// Get records within January.
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	records, err := repo.GetRecords(ctx, contractID, metric, from, to)
	if err != nil {
		t.Fatalf("GetRecords failed: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records in January, got %d", len(records))
	}

	// Broader range includes all three.
	toMarch := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	allRecords, err := repo.GetRecords(ctx, contractID, metric, from, toMarch)
	if err != nil {
		t.Fatalf("GetRecords failed: %v", err)
	}
	if len(allRecords) != 3 {
		t.Errorf("expected 3 records total, got %d", len(allRecords))
	}
}

func TestInMemoryUsageRepository_GetRecords_FiltersByMetricAndContract(t *testing.T) {
	repo := NewInMemoryUsageRepository()
	ctx := context.Background()

	contractID1 := shared.NewContractID()
	contractID2 := shared.NewContractID()
	metricA := shared.MetricName("api_calls")
	metricB := shared.MetricName("storage_gb")

	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	rec1 := mustNewUsageRecord(t, contractID1, metricA, 100, ts, "k1")
	rec2 := mustNewUsageRecord(t, contractID1, metricB, 50, ts, "k2")
	rec3 := mustNewUsageRecord(t, contractID2, metricA, 200, ts, "k3")

	for _, rec := range []*usage.UsageRecord{rec1, rec2, rec3} {
		if err := repo.Record(ctx, rec); err != nil {
			t.Fatalf("Record failed: %v", err)
		}
	}

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	results, err := repo.GetRecords(ctx, contractID1, metricA, from, to)
	if err != nil {
		t.Fatalf("GetRecords failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 record for contractID1/metricA, got %d", len(results))
	}
}

func TestInMemoryUsageRepository_GetSummary(t *testing.T) {
	repo := NewInMemoryUsageRepository()
	ctx := context.Background()

	contractID := shared.NewContractID()
	metric := shared.MetricName("api_calls")

	ts1 := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ts2 := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	ts3 := time.Date(2026, 2, 5, 12, 0, 0, 0, time.UTC) // Outside January period.

	rec1 := mustNewUsageRecord(t, contractID, metric, 100, ts1, "s1")
	rec2 := mustNewUsageRecord(t, contractID, metric, 250, ts2, "s2")
	rec3 := mustNewUsageRecord(t, contractID, metric, 50, ts3, "s3")

	for _, rec := range []*usage.UsageRecord{rec1, rec2, rec3} {
		if err := repo.Record(ctx, rec); err != nil {
			t.Fatalf("Record failed: %v", err)
		}
	}

	// Summary for January.
	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	period, err := shared.NewDateRange(periodStart, periodEnd)
	if err != nil {
		t.Fatalf("NewDateRange failed: %v", err)
	}

	summary, err := repo.GetSummary(ctx, contractID, metric, period)
	if err != nil {
		t.Fatalf("GetSummary failed: %v", err)
	}
	// 100 + 250 = 350 (ts3 is outside the period).
	if summary.TotalUsage != 350 {
		t.Errorf("expected total usage 350, got %d", summary.TotalUsage)
	}
	if summary.ContractID != contractID {
		t.Errorf("expected contractID %s, got %s", contractID, summary.ContractID)
	}
	if summary.MetricName != metric {
		t.Errorf("expected metric %s, got %s", metric, summary.MetricName)
	}
}

func TestInMemoryUsageRepository_GetSummary_EmptyResult(t *testing.T) {
	repo := NewInMemoryUsageRepository()
	ctx := context.Background()

	contractID := shared.NewContractID()
	metric := shared.MetricName("api_calls")

	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	period, err := shared.NewDateRange(periodStart, periodEnd)
	if err != nil {
		t.Fatalf("NewDateRange failed: %v", err)
	}

	summary, err := repo.GetSummary(ctx, contractID, metric, period)
	if err != nil {
		t.Fatalf("GetSummary failed: %v", err)
	}
	if summary.TotalUsage != 0 {
		t.Errorf("expected total usage 0, got %d", summary.TotalUsage)
	}
}

func TestInMemoryUsageRepository_IdempotencyKeyDeduplication(t *testing.T) {
	repo := NewInMemoryUsageRepository()
	ctx := context.Background()

	contractID := shared.NewContractID()
	metric := shared.MetricName("api_calls")
	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	rec1 := mustNewUsageRecord(t, contractID, metric, 100, ts, "dup-key")
	if err := repo.Record(ctx, rec1); err != nil {
		t.Fatalf("first Record failed: %v", err)
	}

	// Duplicate idempotency key should fail.
	rec2 := mustNewUsageRecord(t, contractID, metric, 200, ts, "dup-key")
	err := repo.Record(ctx, rec2)
	if err == nil {
		t.Fatal("expected error for duplicate idempotency key")
	}

	// Empty idempotency key should not trigger deduplication.
	rec3 := mustNewUsageRecord(t, contractID, metric, 300, ts, "")
	if err := repo.Record(ctx, rec3); err != nil {
		t.Fatalf("Record with empty key failed: %v", err)
	}
	rec4 := mustNewUsageRecord(t, contractID, metric, 400, ts, "")
	if err := repo.Record(ctx, rec4); err != nil {
		t.Fatalf("second Record with empty key failed: %v", err)
	}
}

func TestInMemoryUsageRepository_TimeRangeFiltering(t *testing.T) {
	repo := NewInMemoryUsageRepository()
	ctx := context.Background()

	contractID := shared.NewContractID()
	metric := shared.MetricName("api_calls")

	// Record at the exact boundary times.
	fromTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	toTime := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	// At from (inclusive).
	recAtFrom := mustNewUsageRecord(t, contractID, metric, 10, fromTime, "b1")
	// At to (exclusive — should NOT be included).
	recAtTo := mustNewUsageRecord(t, contractID, metric, 20, toTime, "b2")
	// Before from (should NOT be included).
	recBefore := mustNewUsageRecord(t, contractID, metric, 30, fromTime.Add(-time.Hour), "b3")

	for _, rec := range []*usage.UsageRecord{recAtFrom, recAtTo, recBefore} {
		if err := repo.Record(ctx, rec); err != nil {
			t.Fatalf("Record failed: %v", err)
		}
	}

	records, err := repo.GetRecords(ctx, contractID, metric, fromTime, toTime)
	if err != nil {
		t.Fatalf("GetRecords failed: %v", err)
	}
	// Only recAtFrom should be included: [from, to) is half-open.
	if len(records) != 1 {
		t.Errorf("expected 1 record in [from, to), got %d", len(records))
	}
}
