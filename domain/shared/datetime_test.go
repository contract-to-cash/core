package shared

import (
	"testing"
	"time"
)

func TestNewDateRange(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	dr, err := NewDateRange(start, end)
	if err != nil {
		t.Fatal(err)
	}
	if dr.Start() != start {
		t.Errorf("expected start %v, got %v", start, dr.Start())
	}
	if dr.End() != end {
		t.Errorf("expected end %v, got %v", end, dr.End())
	}
}

func TestNewDateRange_InvalidRange(t *testing.T) {
	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := NewDateRange(start, end)
	if err == nil {
		t.Error("expected error for invalid date range")
	}
}

func TestNewDateRange_SameStartEnd(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := NewDateRange(ts, ts)
	if err == nil {
		t.Error("expected error when start == end")
	}
}

func TestDateRange_Contains(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	dr, _ := NewDateRange(start, end)

	mid := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if !dr.Contains(mid) {
		t.Error("expected to contain mid-range date")
	}
	if !dr.Contains(start) {
		t.Error("expected to contain start (inclusive)")
	}
	if dr.Contains(end) {
		t.Error("expected not to contain end (exclusive)")
	}
	before := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	if dr.Contains(before) {
		t.Error("expected not to contain date before range")
	}
}

func TestDateRange_Duration(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	dr, _ := NewDateRange(start, end)
	expected := 31 * 24 * time.Hour
	if dr.Duration() != expected {
		t.Errorf("expected %v, got %v", expected, dr.Duration())
	}
}

func TestDateRangeNext_Daily(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	dr, _ := NewDateRange(start, end)

	next := dr.Next("daily")

	if next.Start() != end {
		t.Errorf("expected start %v, got %v", end, next.Start())
	}
	wantEnd := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	if next.End() != wantEnd {
		t.Errorf("expected end %v, got %v", wantEnd, next.End())
	}
}

func TestDateRangeNext_Weekly(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	dr, _ := NewDateRange(start, end)

	next := dr.Next("weekly")

	if next.Start() != end {
		t.Errorf("expected start %v, got %v", end, next.Start())
	}
	wantEnd := time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC)
	if next.End() != wantEnd {
		t.Errorf("expected end %v, got %v", wantEnd, next.End())
	}
}

func TestDateRangeNext_Monthly(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	dr, _ := NewDateRange(start, end)

	next := dr.Next("monthly")

	if next.Start() != end {
		t.Errorf("expected start %v, got %v", end, next.Start())
	}
	wantEnd := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if next.End() != wantEnd {
		t.Errorf("expected end %v, got %v", wantEnd, next.End())
	}
}

func TestDateRangeNext_Yearly(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	dr, _ := NewDateRange(start, end)

	next := dr.Next("yearly")

	if next.Start() != end {
		t.Errorf("expected start %v, got %v", end, next.Start())
	}
	wantEnd := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)
	if next.End() != wantEnd {
		t.Errorf("expected end %v, got %v", wantEnd, next.End())
	}
}

func TestDateRangeNext_UnknownCycle(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	dr, _ := NewDateRange(start, end)

	next := dr.Next("biweekly")

	// Unknown cycle defaults to monthly
	if next.Start() != end {
		t.Errorf("expected start %v, got %v", end, next.Start())
	}
	wantEnd := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if next.End() != wantEnd {
		t.Errorf("expected end %v, got %v", wantEnd, next.End())
	}
}
