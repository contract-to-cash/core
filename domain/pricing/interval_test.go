package pricing

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewBillingInterval_Valid(t *testing.T) {
	tests := []struct {
		name  string
		unit  IntervalUnit
		count int
	}{
		{"monthly", IntervalUnitMonth, 1},
		{"quarterly", IntervalUnitMonth, 3},
		{"semi-annual", IntervalUnitMonth, 6},
		{"yearly", IntervalUnitYear, 1},
		{"weekly", IntervalUnitWeek, 1},
		{"daily", IntervalUnitDay, 1},
		{"biweekly", IntervalUnitWeek, 2},
		{"24 months", IntervalUnitMonth, 24},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval, err := NewBillingInterval(tt.unit, tt.count)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if interval.Unit() != tt.unit {
				t.Errorf("expected unit %s, got %s", tt.unit, interval.Unit())
			}
			if interval.Count() != tt.count {
				t.Errorf("expected count %d, got %d", tt.count, interval.Count())
			}
		})
	}
}

func TestNewBillingInterval_InvalidCount(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{"zero", 0},
		{"negative", -1},
		{"very negative", -100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewBillingInterval(IntervalUnitMonth, tt.count)
			if err == nil {
				t.Error("expected error for invalid count")
			}
		})
	}
}

func TestNewBillingInterval_InvalidUnit(t *testing.T) {
	_, err := NewBillingInterval(IntervalUnit("biweekly"), 1)
	if err == nil {
		t.Error("expected error for invalid unit")
	}
}

func TestBillingInterval_AddTo(t *testing.T) {
	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		unit     IntervalUnit
		count    int
		expected time.Time
	}{
		{"1 month", IntervalUnitMonth, 1, time.Date(2026, 2, 15, 10, 0, 0, 0, time.UTC)},
		{"3 months (quarterly)", IntervalUnitMonth, 3, time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)},
		{"6 months (semi-annual)", IntervalUnitMonth, 6, time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)},
		{"12 months", IntervalUnitMonth, 12, time.Date(2027, 1, 15, 10, 0, 0, 0, time.UTC)},
		{"1 year", IntervalUnitYear, 1, time.Date(2027, 1, 15, 10, 0, 0, 0, time.UTC)},
		{"2 years", IntervalUnitYear, 2, time.Date(2028, 1, 15, 10, 0, 0, 0, time.UTC)},
		{"1 week", IntervalUnitWeek, 1, time.Date(2026, 1, 22, 10, 0, 0, 0, time.UTC)},
		{"2 weeks", IntervalUnitWeek, 2, time.Date(2026, 1, 29, 10, 0, 0, 0, time.UTC)},
		{"1 day", IntervalUnitDay, 1, time.Date(2026, 1, 16, 10, 0, 0, 0, time.UTC)},
		{"7 days", IntervalUnitDay, 7, time.Date(2026, 1, 22, 10, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval, err := NewBillingInterval(tt.unit, tt.count)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			result := interval.AddTo(base)
			if !result.Equal(tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestBillingInterval_AddTo_Clamping verifies calendar-correct month/year
// addition: the day-of-month is clamped to the last valid day of the target
// month instead of overflowing into the next month (issue #186). Before the
// fix, time.AddDate normalized Jan 31 + 1 month to Mar 3, drifting the anchor.
func TestBillingInterval_AddTo_Clamping(t *testing.T) {
	tests := []struct {
		name     string
		base     time.Time
		unit     IntervalUnit
		count    int
		expected time.Time
	}{
		{"Jan31 +1mo -> Feb28 (non-leap)", time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 1, time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)},
		{"Jan31 +1mo -> Feb29 (leap)", time.Date(2028, 1, 31, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 1, time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)},
		{"Jan31 +2mo -> Mar31", time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 2, time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)},
		{"Mar31 +1mo -> Apr30", time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 1, time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)},
		{"Jan30 +1mo -> Feb28", time.Date(2026, 1, 30, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 1, time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)},
		{"Aug31 +3mo (quarterly) -> Nov30", time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 3, time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)},
		{"Dec31 +1mo -> Jan31 (year rollover)", time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 1, time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC)},
		{"Feb29 +1yr -> Feb28 (leap to non-leap)", time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC), IntervalUnitYear, 1, time.Date(2029, 2, 28, 0, 0, 0, 0, time.UTC)},
		{"Feb29 +4yr -> Feb29 (leap to leap)", time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC), IntervalUnitYear, 4, time.Date(2032, 2, 29, 0, 0, 0, 0, time.UTC)},
		{"time-of-day preserved", time.Date(2026, 1, 31, 10, 30, 15, 0, time.UTC), IntervalUnitMonth, 1, time.Date(2026, 2, 28, 10, 30, 15, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval, err := NewBillingInterval(tt.unit, tt.count)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			result := interval.AddTo(tt.base)
			if !result.Equal(tt.expected) {
				t.Errorf("AddTo(%v): expected %v, got %v", tt.base, tt.expected, result)
			}
		})
	}
}

// TestBillingInterval_AddToWithAnchorDay verifies that passing the original
// billing anchor day restores the intended day-of-month wherever the target
// month allows, preventing anchor drift across successive renewals (issue #186).
func TestBillingInterval_AddToWithAnchorDay(t *testing.T) {
	tests := []struct {
		name      string
		base      time.Time
		unit      IntervalUnit
		count     int
		anchorDay int
		expected  time.Time
	}{
		{"Feb28 +1mo anchor31 -> Mar31 (recovers)", time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 1, 31, time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)},
		{"Mar31 +1mo anchor31 -> Apr30 (clamped)", time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 1, 31, time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)},
		{"Apr30 +1mo anchor31 -> May31 (recovers)", time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 1, 31, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)},
		{"Feb28 +1yr anchor29 -> Feb28 (non-leap)", time.Date(2025, 2, 28, 0, 0, 0, 0, time.UTC), IntervalUnitYear, 1, 29, time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)},
		{"Feb28 +1yr anchor29 -> Feb29 (leap recovers)", time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC), IntervalUnitYear, 1, 29, time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)},
		{"mid-month anchor15 unaffected", time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 1, 15, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)},
		{"anchorDay 0 falls back to AddTo", time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), IntervalUnitMonth, 1, 0, time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)},
		{"day interval ignores anchor", time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), IntervalUnitDay, 5, 31, time.Date(2026, 2, 5, 0, 0, 0, 0, time.UTC)},
		{"week interval ignores anchor", time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), IntervalUnitWeek, 1, 31, time.Date(2026, 2, 7, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval, err := NewBillingInterval(tt.unit, tt.count)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			result := interval.AddToWithAnchorDay(tt.base, tt.anchorDay)
			if !result.Equal(tt.expected) {
				t.Errorf("AddToWithAnchorDay(%v, %d): expected %v, got %v", tt.base, tt.anchorDay, tt.expected, result)
			}
		})
	}
}

// TestBillingInterval_RenewalSequence_NoAnchorDrift proves the canonical
// month-end scenario from issue #186: a Jan 31 subscription must bill on
// Feb 28 -> Mar 31 -> Apr 30 -> May 31, recovering the 31st anchor each time
// the target month is long enough, rather than drifting downward.
func TestBillingInterval_RenewalSequence_NoAnchorDrift(t *testing.T) {
	monthly := Monthly()
	anchor := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	anchorDay := anchor.Day() // 31

	// Initial period end (first AddTo from the anchor clamps to Feb 28).
	cur := monthly.AddTo(anchor)
	wantEnds := []time.Time{
		time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	}
	if want := time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC); !cur.Equal(want) {
		t.Fatalf("initial period end: expected %v, got %v", want, cur)
	}
	for i, want := range wantEnds {
		cur = monthly.AddToWithAnchorDay(cur, anchorDay)
		if !cur.Equal(want) {
			t.Fatalf("renewal %d: expected end %v, got %v", i+1, want, cur)
		}
	}
}

func TestConvenienceConstructors(t *testing.T) {
	tests := []struct {
		name    string
		fn      func() BillingInterval
		unit    IntervalUnit
		count   int
		strRepr string
	}{
		{"Monthly", Monthly, IntervalUnitMonth, 1, "month"},
		{"Yearly", Yearly, IntervalUnitYear, 1, "year"},
		{"Quarterly", Quarterly, IntervalUnitMonth, 3, "3 months"},
		{"SemiAnnual", SemiAnnual, IntervalUnitMonth, 6, "6 months"},
		{"Daily", Daily, IntervalUnitDay, 1, "day"},
		{"Weekly", Weekly, IntervalUnitWeek, 1, "week"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval := tt.fn()
			if interval.Unit() != tt.unit {
				t.Errorf("expected unit %s, got %s", tt.unit, interval.Unit())
			}
			if interval.Count() != tt.count {
				t.Errorf("expected count %d, got %d", tt.count, interval.Count())
			}
			if interval.String() != tt.strRepr {
				t.Errorf("expected string %q, got %q", tt.strRepr, interval.String())
			}
		})
	}
}

func TestBillingInterval_String(t *testing.T) {
	tests := []struct {
		unit     IntervalUnit
		count    int
		expected string
	}{
		{IntervalUnitMonth, 1, "month"},
		{IntervalUnitMonth, 3, "3 months"},
		{IntervalUnitYear, 1, "year"},
		{IntervalUnitYear, 2, "2 years"},
		{IntervalUnitWeek, 1, "week"},
		{IntervalUnitWeek, 2, "2 weeks"},
		{IntervalUnitDay, 1, "day"},
		{IntervalUnitDay, 30, "30 days"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			interval, _ := NewBillingInterval(tt.unit, tt.count)
			if interval.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, interval.String())
			}
		})
	}
}

func TestBillingInterval_Equals(t *testing.T) {
	a := Monthly()
	b := Monthly()
	c := Quarterly()

	if !a.Equals(b) {
		t.Error("expected Monthly to equal Monthly")
	}
	if a.Equals(c) {
		t.Error("expected Monthly to not equal Quarterly")
	}
}

func TestBillingInterval_IsZero(t *testing.T) {
	var zero BillingInterval
	if !zero.IsZero() {
		t.Error("expected zero BillingInterval to be zero")
	}
	if Monthly().IsZero() {
		t.Error("expected Monthly to not be zero")
	}
}

func TestBillingInterval_MarshalJSON(t *testing.T) {
	interval := Quarterly()
	data, err := json.Marshal(interval)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if raw["unit"] != "month" {
		t.Errorf("expected unit 'month', got %v", raw["unit"])
	}
	if raw["count"] != float64(3) {
		t.Errorf("expected count 3, got %v", raw["count"])
	}
}

func TestBillingInterval_UnmarshalJSON_NewFormat(t *testing.T) {
	data := []byte(`{"unit":"month","count":3}`)
	var interval BillingInterval
	if err := json.Unmarshal(data, &interval); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if interval.Unit() != IntervalUnitMonth {
		t.Errorf("expected unit month, got %s", interval.Unit())
	}
	if interval.Count() != 3 {
		t.Errorf("expected count 3, got %d", interval.Count())
	}
}

func TestBillingInterval_UnmarshalJSON_OldFormat(t *testing.T) {
	tests := []struct {
		name  string
		input string
		unit  IntervalUnit
		count int
	}{
		{"monthly", `"monthly"`, IntervalUnitMonth, 1},
		{"yearly", `"yearly"`, IntervalUnitYear, 1},
		{"weekly", `"weekly"`, IntervalUnitWeek, 1},
		{"daily", `"daily"`, IntervalUnitDay, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var interval BillingInterval
			if err := json.Unmarshal([]byte(tt.input), &interval); err != nil {
				t.Fatalf("unexpected unmarshal error: %v", err)
			}
			if interval.Unit() != tt.unit {
				t.Errorf("expected unit %s, got %s", tt.unit, interval.Unit())
			}
			if interval.Count() != tt.count {
				t.Errorf("expected count %d, got %d", tt.count, interval.Count())
			}
		})
	}
}

func TestBillingInterval_MarshalUnmarshalRoundTrip(t *testing.T) {
	intervals := []BillingInterval{
		Monthly(),
		Yearly(),
		Quarterly(),
		SemiAnnual(),
		Daily(),
		Weekly(),
	}

	for _, original := range intervals {
		t.Run(original.String(), func(t *testing.T) {
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("unexpected marshal error: %v", err)
			}
			var decoded BillingInterval
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unexpected unmarshal error: %v", err)
			}
			if !original.Equals(decoded) {
				t.Errorf("round-trip mismatch: original=%v, decoded=%v", original, decoded)
			}
		})
	}
}

func TestBillingCycleToInterval(t *testing.T) {
	tests := []struct {
		cycle BillingCycle
		unit  IntervalUnit
		count int
	}{
		{BillingCycleDaily, IntervalUnitDay, 1},
		{BillingCycleWeekly, IntervalUnitWeek, 1},
		{BillingCycleMonthly, IntervalUnitMonth, 1},
		{BillingCycleYearly, IntervalUnitYear, 1},
	}

	for _, tt := range tests {
		t.Run(string(tt.cycle), func(t *testing.T) {
			interval := BillingCycleToInterval(tt.cycle)
			if interval.Unit() != tt.unit {
				t.Errorf("expected unit %s, got %s", tt.unit, interval.Unit())
			}
			if interval.Count() != tt.count {
				t.Errorf("expected count %d, got %d", tt.count, interval.Count())
			}
		})
	}
}

func TestIntervalToBillingCycle(t *testing.T) {
	tests := []struct {
		name     string
		interval BillingInterval
		cycle    BillingCycle
		ok       bool
	}{
		{"daily", Daily(), BillingCycleDaily, true},
		{"weekly", Weekly(), BillingCycleWeekly, true},
		{"monthly", Monthly(), BillingCycleMonthly, true},
		{"yearly", Yearly(), BillingCycleYearly, true},
		{"quarterly (no match)", Quarterly(), "", false},
		{"semi-annual (no match)", SemiAnnual(), "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cycle, ok := IntervalToBillingCycle(tt.interval)
			if ok != tt.ok {
				t.Errorf("expected ok=%v, got ok=%v", tt.ok, ok)
			}
			if ok && cycle != tt.cycle {
				t.Errorf("expected cycle %s, got %s", tt.cycle, cycle)
			}
		})
	}
}

func TestBillingInterval_ToBillingCycle(t *testing.T) {
	// Monthly should produce "monthly"
	cycle := Monthly().ToBillingCycle()
	if cycle != BillingCycleMonthly {
		t.Errorf("expected %s, got %s", BillingCycleMonthly, cycle)
	}

	// Quarterly has no exact BillingCycle match, should return ""
	cycle = Quarterly().ToBillingCycle()
	if cycle != "" {
		t.Errorf("expected empty string for quarterly, got %s", cycle)
	}
}

func TestBillingInterval_UnmarshalJSON_InvalidUnit(t *testing.T) {
	// M1 fix: invalid unit in new format should be rejected
	data := []byte(`{"unit":"quarter","count":3}`)
	var interval BillingInterval
	err := json.Unmarshal(data, &interval)
	if err == nil {
		t.Fatal("expected error for invalid unit in JSON, got nil")
	}
}

func TestBillingInterval_UnmarshalJSON_UnknownOldFormat(t *testing.T) {
	// M3 fix: unknown old format string should return clear error
	data := []byte(`"bimonthly"`)
	var interval BillingInterval
	err := json.Unmarshal(data, &interval)
	if err == nil {
		t.Fatal("expected error for unknown old format, got nil")
	}
	if got := err.Error(); !contains(got, "unknown billing cycle") {
		t.Errorf("expected error containing 'unknown billing cycle', got: %s", got)
	}
}

func TestBillingInterval_MarshalJSON_ZeroValue(t *testing.T) {
	// M2 fix: zero value should marshal as null
	var interval BillingInterval
	data, err := json.Marshal(interval)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("expected null for zero-value, got %s", string(data))
	}
}

func TestBillingInterval_UnmarshalJSON_Null(t *testing.T) {
	// null should unmarshal to zero value
	interval := Monthly() // start with non-zero
	err := json.Unmarshal([]byte("null"), &interval)
	if err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if !interval.IsZero() {
		t.Errorf("expected zero value after unmarshaling null, got %v", interval)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
