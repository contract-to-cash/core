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

func TestDateRange_Equals(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	dr1, _ := NewDateRange(start, end)
	dr2, _ := NewDateRange(start, end)

	if !dr1.Equals(dr2) {
		t.Error("expected equal date ranges to be equal")
	}

	dr3, _ := NewDateRange(start, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	if dr1.Equals(dr3) {
		t.Error("expected different date ranges to not be equal")
	}

	// Zero value
	var zero DateRange
	if dr1.Equals(zero) {
		t.Error("expected non-zero to not equal zero")
	}
}

func TestDateRange_String(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	dr, _ := NewDateRange(start, end)
	s := dr.String()
	if s == "" {
		t.Error("expected non-empty string representation")
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

// TestDateRange_UnmarshalJSON_NormalizesToUTC verifies that a DateRange
// deserialized from JSON with a non-UTC zone offset is normalized to UTC,
// matching NewDateRange's UTC-only contract (issue #162 L-3).
func TestDateRange_UnmarshalJSON_NormalizesToUTC(t *testing.T) {
	// +09:00 instants that are 2026-01-01T00:00Z and 2026-02-01T00:00Z in UTC.
	jst := time.FixedZone("JST", 9*3600)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, jst)
	end := time.Date(2026, 2, 1, 9, 0, 0, 0, jst)

	src, err := NewDateRange(start, end)
	if err != nil {
		t.Fatalf("NewDateRange: %v", err)
	}
	data, err := src.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var got DateRange
	if err := got.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if loc := got.Start().Location(); loc != time.UTC {
		t.Errorf("Start not normalized to UTC: got location %v", loc)
	}
	if loc := got.End().Location(); loc != time.UTC {
		t.Errorf("End not normalized to UTC: got location %v", loc)
	}
	wantStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Start().Equal(wantStart) {
		t.Errorf("Start = %v, want %v", got.Start(), wantStart)
	}
}

// TestDateRange_UnmarshalJSON_ToleratesInvertedRange verifies deserialize stays
// replay-safe: an inverted (start > end) range that NewDateRange would reject
// must still deserialize so a historically-persisted event never fails to
// replay (issue #162 L-3).
func TestDateRange_UnmarshalJSON_ToleratesInvertedRange(t *testing.T) {
	data := []byte(`{"start":"2026-02-01T00:00:00Z","end":"2026-01-01T00:00:00Z"}`)
	var got DateRange
	if err := got.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON must tolerate inverted range for replay safety, got: %v", err)
	}
	if !got.Start().After(got.End()) {
		t.Errorf("expected inverted range preserved (start after end), got start=%v end=%v",
			got.Start(), got.End())
	}
}
