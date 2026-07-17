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
//
// Deprecated: use pricing.BillingInterval.AddTo to advance billing periods.
// This method uses the legacy string-based cycle and inherits
// AddBillingCycleDuration's hazardous fallback: ⚠️ ANY unrecognized cycle
// string (a typo like "montly", "biweekly", "") SILENTLY advances by ONE
// MONTH instead of failing. It has no in-repo production callers and is kept
// only so external callers do not break; the monthly-default behavior is
// intentionally left unchanged for the same reason.
func (r DateRange) Next(cycle string) DateRange {
	end := AddBillingCycleDuration(r.end, cycle)
	return DateRange{start: r.end, end: end}
}

// AddBillingCycleDuration adds one billing cycle duration to a time.
//
// Deprecated: use pricing.BillingInterval.AddTo (or
// pricing.BillingCycleToIntervalStrict to convert a cycle string with
// validation). ⚠️ This function SILENTLY DEFAULTS TO MONTHLY for any cycle
// string other than "daily", "weekly", "monthly", or "yearly" — a typo or an
// unsupported cycle does not error, it just advances the time by one month,
// which can mis-schedule billing periods. It has no in-repo production
// callers and is kept only for backward compatibility with external callers
// that use string-based billing cycles; the fallback behavior is intentionally
// left unchanged so as not to break them.
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
//
// Times are normalized to UTC so a deserialized range matches one built via
// NewDateRange, whose contract is UTC-only (issue #162 L-3). Unlike NewDateRange
// it does NOT reject start >= end: DateRange is embedded in append-only event
// and snapshot payloads, and rejecting a historically-persisted degenerate or
// inverted range here would make an existing stream fail to replay. Deserialize
// is a read path and must stay replay-safe; range validity is enforced at
// CONSTRUCTION time (NewDateRange) where it belongs.
func (r *DateRange) UnmarshalJSON(data []byte) error {
	type dateRangeJSON struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	}
	var v dateRangeJSON
	if err := unmarshalJSON(data, &v); err != nil {
		return err
	}
	r.start = v.Start.UTC()
	r.end = v.End.UTC()
	return nil
}
