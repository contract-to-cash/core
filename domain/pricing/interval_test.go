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

func TestBillingInterval_AddTo_EndOfMonth(t *testing.T) {
	// Jan 31 + 1 month = Feb 28 (Go's AddDate behavior)
	base := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	interval, _ := NewBillingInterval(IntervalUnitMonth, 1)
	result := interval.AddTo(base)
	expected := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC) // Go normalizes Feb 31 -> Mar 3
	if !result.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, result)
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
