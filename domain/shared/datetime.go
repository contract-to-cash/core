package shared

import (
	"fmt"
	"time"
)

// DateRange represents a time range with start and end.
type DateRange struct {
	start time.Time
	end   time.Time
}

// NewDateRange creates a new DateRange. Times are normalized to UTC.
// Returns error if start >= end.
func NewDateRange(start, end time.Time) (DateRange, error) {
	start = start.UTC()
	end = end.UTC()
	if !start.Before(end) {
		return DateRange{}, NewDomainError(ErrCodeInvalidDateRange,
			fmt.Sprintf("start (%s) must be before end (%s)", start, end))
	}
	return DateRange{start: start, end: end}, nil
}

// Start returns the start of the range.
func (r DateRange) Start() time.Time {
	return r.start
}

// End returns the end of the range.
func (r DateRange) End() time.Time {
	return r.end
}

// Contains returns true if t is within [start, end).
func (r DateRange) Contains(t time.Time) bool {
	return !t.Before(r.start) && t.Before(r.end)
}

// Duration returns the duration of the range.
func (r DateRange) Duration() time.Duration {
	return r.end.Sub(r.start)
}

// Equals returns true if both date ranges have the same start and end.
func (r DateRange) Equals(other DateRange) bool {
	return r.start.Equal(other.start) && r.end.Equal(other.end)
}

// IsZero returns true if the date range is the zero value (not initialized).
func (r DateRange) IsZero() bool {
	return r.start.IsZero() && r.end.IsZero()
}

// String returns a human-readable representation of the date range.
func (r DateRange) String() string {
	return fmt.Sprintf("[%s, %s)", r.start.Format(time.RFC3339), r.end.Format(time.RFC3339))
}

// MarshalJSON implements json.Marshaler for DateRange.
func (r DateRange) MarshalJSON() ([]byte, error) {
	type dateRangeJSON struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	}
	return marshalJSON(dateRangeJSON{Start: r.start, End: r.end})
}

// Next returns the next DateRange based on the billing cycle.
// The new range starts where the current one ends.
func (r DateRange) Next(cycle string) DateRange {
	end := AddBillingCycleDuration(r.end, cycle)
	return DateRange{start: r.end, end: end}
}

// AddBillingCycleDuration adds one billing cycle duration to a time.
// This is kept for backward compatibility with DateRange.Next() and other callers
// that use string-based billing cycles.
// For new code, use pricing.BillingInterval.AddTo() directly.
func AddBillingCycleDuration(t time.Time, cycle string) time.Time {
	switch cycle {
	case "monthly":
		return t.AddDate(0, 1, 0)
	case "yearly":
		return t.AddDate(1, 0, 0)
	case "weekly":
		return t.AddDate(0, 0, 7)
	case "daily":
		return t.AddDate(0, 0, 1)
	default:
		return t.AddDate(0, 1, 0)
	}
}

// UnmarshalJSON implements json.Unmarshaler for DateRange.
func (r *DateRange) UnmarshalJSON(data []byte) error {
	type dateRangeJSON struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	}
	var v dateRangeJSON
	if err := unmarshalJSON(data, &v); err != nil {
		return err
	}
	r.start = v.Start
	r.end = v.End
	return nil
}
