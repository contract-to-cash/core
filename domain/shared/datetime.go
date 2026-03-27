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

// MarshalJSON implements json.Marshaler for DateRange.
func (r DateRange) MarshalJSON() ([]byte, error) {
	type dateRangeJSON struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	}
	return marshalJSON(dateRangeJSON{Start: r.start, End: r.end})
}

// Next returns the next DateRange of the same duration based on the billing cycle.
// The new range starts where the current one ends.
func (r DateRange) Next(cycle string) DateRange {
	switch cycle {
	case "monthly":
		end := r.end.AddDate(0, 1, 0)
		return DateRange{start: r.end, end: end}
	case "yearly":
		end := r.end.AddDate(1, 0, 0)
		return DateRange{start: r.end, end: end}
	case "weekly":
		end := r.end.AddDate(0, 0, 7)
		return DateRange{start: r.end, end: end}
	case "daily":
		end := r.end.AddDate(0, 0, 1)
		return DateRange{start: r.end, end: end}
	default:
		// Default to monthly if unknown cycle
		end := r.end.AddDate(0, 1, 0)
		return DateRange{start: r.end, end: end}
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
