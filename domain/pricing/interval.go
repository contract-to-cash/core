package pricing

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// IntervalUnit represents the base unit for a billing interval.
type IntervalUnit string

const (
	IntervalUnitDay   IntervalUnit = "day"
	IntervalUnitWeek  IntervalUnit = "week"
	IntervalUnitMonth IntervalUnit = "month"
	IntervalUnitYear  IntervalUnit = "year"
)

// validIntervalUnits is the set of valid IntervalUnit values.
var validIntervalUnits = map[IntervalUnit]bool{
	IntervalUnitDay:   true,
	IntervalUnitWeek:  true,
	IntervalUnitMonth: true,
	IntervalUnitYear:  true,
}

// BillingInterval represents how often billing occurs.
// Immutable value object following the interval + count model (Stripe, Chargebee, Recurly).
// For example, {unit: "month", count: 3} represents quarterly billing.
type BillingInterval struct {
	unit  IntervalUnit
	count int // must be >= 1
}

// NewBillingInterval creates a new BillingInterval with validation.
// Returns an error if count < 1 or unit is invalid.
func NewBillingInterval(unit IntervalUnit, count int) (BillingInterval, error) {
	if !validIntervalUnits[unit] {
		return BillingInterval{}, shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("invalid interval unit: %q (must be day, week, month, or year)", unit))
	}
	if count < 1 {
		return BillingInterval{}, shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("interval count must be >= 1, got %d", count))
	}
	return BillingInterval{unit: unit, count: count}, nil
}

// Convenience constructors for common intervals.

// Daily returns a BillingInterval for daily billing.
func Daily() BillingInterval { return BillingInterval{unit: IntervalUnitDay, count: 1} }

// Weekly returns a BillingInterval for weekly billing.
func Weekly() BillingInterval { return BillingInterval{unit: IntervalUnitWeek, count: 1} }

// Monthly returns a BillingInterval for monthly billing.
func Monthly() BillingInterval { return BillingInterval{unit: IntervalUnitMonth, count: 1} }

// Yearly returns a BillingInterval for yearly billing.
func Yearly() BillingInterval { return BillingInterval{unit: IntervalUnitYear, count: 1} }

// Quarterly returns a BillingInterval for quarterly (every 3 months) billing.
func Quarterly() BillingInterval { return BillingInterval{unit: IntervalUnitMonth, count: 3} }

// SemiAnnual returns a BillingInterval for semi-annual (every 6 months) billing.
func SemiAnnual() BillingInterval { return BillingInterval{unit: IntervalUnitMonth, count: 6} }

// Unit returns the interval unit.
func (i BillingInterval) Unit() IntervalUnit { return i.unit }

// Count returns the interval count.
func (i BillingInterval) Count() int { return i.count }

// IsZero returns true if the BillingInterval is the zero value (not initialized).
func (i BillingInterval) IsZero() bool { return i.unit == "" && i.count == 0 }

// Equals returns true if both intervals have the same unit and count.
func (i BillingInterval) Equals(other BillingInterval) bool {
	return i.unit == other.unit && i.count == other.count
}

// AddTo adds this interval's duration to the given time and returns the result.
//
// Month and year addition are calendar-correct: the day-of-month is clamped to
// the last valid day of the target month rather than overflowing. Jan 31 + 1
// month yields Feb 28 (Feb 29 in a leap year), NOT Mar 3, and Feb 29 + 1 year
// yields Feb 28. This is the standard billing-anchor behavior (Stripe/Chargebee)
// and avoids the drift that time.AddDate's overflow normalization introduces
// (issue #186). Day and week addition are exact and unaffected.
//
// Note: adding a single interval still clamps a month-end day DOWN for short
// months. To keep a month-end anchor (e.g. the 31st) from permanently drifting
// across SUCCESSIVE renewals, use AddToWithAnchorDay, which restores the
// original day wherever the target month allows.
func (i BillingInterval) AddTo(t time.Time) time.Time {
	switch i.unit {
	case IntervalUnitDay:
		return t.AddDate(0, 0, i.count)
	case IntervalUnitWeek:
		return t.AddDate(0, 0, 7*i.count)
	case IntervalUnitMonth:
		return addMonthsClamped(t, i.count)
	case IntervalUnitYear:
		return addMonthsClamped(t, 12*i.count)
	default:
		// Fallback to monthly (should not happen with validation)
		return addMonthsClamped(t, i.count)
	}
}

// AddToWithAnchorDay advances t by one interval while honoring a billing anchor
// day-of-month, preventing anchor drift across successive month/year renewals.
//
// Plain AddTo clamps a month-end day down for short months (Jan 31 -> Feb 28),
// but adding another interval to that clamped value keeps drifting
// (Feb 28 -> Mar 28 -> ...). Passing the ORIGINAL anchor day (e.g. 31) restores
// the intended day-of-month wherever the target month is long enough, so a
// month-end subscription bills on the true anchor every cycle:
//
//	Jan 31 -> Feb 28 -> Mar 31 -> Apr 30 -> May 31
//
// anchorDay is the day-of-month of the original activation (1..31); a
// non-positive value falls back to AddTo(t). Day and week intervals have no
// month-end concept and are delegated to AddTo unchanged. Time-of-day and
// location are preserved.
func (i BillingInterval) AddToWithAnchorDay(t time.Time, anchorDay int) time.Time {
	if anchorDay <= 0 {
		return i.AddTo(t)
	}
	var months int
	switch i.unit {
	case IntervalUnitMonth:
		months = i.count
	case IntervalUnitYear:
		months = 12 * i.count
	case IntervalUnitDay, IntervalUnitWeek:
		return i.AddTo(t)
	default:
		return i.AddTo(t)
	}
	y, m := shiftMonths(t.Year(), t.Month(), months)
	d := anchorDay
	if last := daysInMonth(y, m); d > last {
		d = last
	}
	return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// shiftMonths returns the target year and month after adding months (which may
// be negative) to the given year/month, without touching the day.
func shiftMonths(year int, month time.Month, months int) (int, time.Month) {
	total := (int(month) - 1) + months
	y := year + total/12
	m := total % 12
	if m < 0 {
		m += 12
		y--
	}
	return y, time.Month(m + 1)
}

// daysInMonth returns the number of days in the given year/month.
func daysInMonth(year int, month time.Month) int {
	// Day 0 of the following month is the last day of the target month.
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// addMonthsClamped adds months to t, clamping the day-of-month to the last valid
// day of the target month (Jan 31 + 1 month -> Feb 28/29, not Mar 3). Time-of-day
// and location are preserved.
func addMonthsClamped(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	ty, tm := shiftMonths(y, m, months)
	if last := daysInMonth(ty, tm); d > last {
		d = last
	}
	return time.Date(ty, tm, d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// String returns a human-readable representation.
// Examples: "month", "3 months", "year", "2 weeks"
func (i BillingInterval) String() string {
	var unitStr string
	switch i.unit {
	case IntervalUnitDay:
		unitStr = "day"
	case IntervalUnitWeek:
		unitStr = "week"
	case IntervalUnitMonth:
		unitStr = "month"
	case IntervalUnitYear:
		unitStr = "year"
	default:
		unitStr = string(i.unit)
	}

	if i.count == 1 {
		return unitStr
	}
	return fmt.Sprintf("%d %ss", i.count, unitStr)
}

// ToBillingCycle converts this interval to a BillingCycle string, if possible.
// Returns an empty string if no exact BillingCycle match exists (e.g., quarterly).
func (i BillingInterval) ToBillingCycle() BillingCycle {
	cycle, _ := IntervalToBillingCycle(i)
	return cycle
}

// billingIntervalJSON is the JSON representation of BillingInterval.
type billingIntervalJSON struct {
	Unit  IntervalUnit `json:"unit"`
	Count int          `json:"count"`
}

// MarshalJSON implements json.Marshaler.
// Returns null for zero-value BillingInterval so that omitempty works correctly
// with event/snapshot serialization.
func (i BillingInterval) MarshalJSON() ([]byte, error) {
	if i.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(billingIntervalJSON{Unit: i.unit, Count: i.count})
}

// billingCycleToIntervalMap maps old BillingCycle string values to BillingInterval.
var billingCycleToIntervalMap = map[string]BillingInterval{
	"daily":   Daily(),
	"weekly":  Weekly(),
	"monthly": Monthly(),
	"yearly":  Yearly(),
}

// UnmarshalJSON implements json.Unmarshaler.
// Supports both new format ({"unit":"month","count":3}) and
// old format ("monthly") for backward compatibility with existing events/snapshots.
func (i *BillingInterval) UnmarshalJSON(data []byte) error {
	// Handle JSON null
	if string(data) == "null" {
		*i = BillingInterval{}
		return nil
	}

	// Try old format first: plain string like "monthly"
	var oldFormat string
	if err := json.Unmarshal(data, &oldFormat); err == nil {
		if interval, ok := billingCycleToIntervalMap[oldFormat]; ok {
			*i = interval
			return nil
		}
		return fmt.Errorf("unknown billing cycle: %q (expected daily, weekly, monthly, or yearly)", oldFormat)
	}

	// Try new format: {"unit":"month","count":3}
	var v billingIntervalJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("failed to unmarshal BillingInterval: %w", err)
	}
	// Zero value is valid (means "not set"), let it through
	if v.Unit == "" && v.Count == 0 {
		*i = BillingInterval{}
		return nil
	}
	// Validate using the constructor to ensure unit and count are valid
	interval, err := NewBillingInterval(v.Unit, v.Count)
	if err != nil {
		return fmt.Errorf("invalid BillingInterval in JSON: %w", err)
	}
	*i = interval
	return nil
}

// BillingCycleToInterval converts a BillingCycle string to a BillingInterval.
// Unknown cycles default to Monthly(). Callers that must NOT silently accept an
// unrecognized cycle (e.g. event upcasters migrating historical payloads) should
// use BillingCycleToIntervalStrict instead (issue #162 L-4).
func BillingCycleToInterval(cycle BillingCycle) BillingInterval {
	if interval, ok := billingCycleToIntervalMap[string(cycle)]; ok {
		return interval
	}
	return Monthly()
}

// BillingCycleToIntervalStrict converts a BillingCycle string to a
// BillingInterval, reporting via the boolean whether the cycle was recognized.
// Unlike BillingCycleToInterval it does NOT silently fall back to Monthly for an
// unknown cycle — the caller decides how to handle the miss (issue #162 L-4).
func BillingCycleToIntervalStrict(cycle BillingCycle) (BillingInterval, bool) {
	interval, ok := billingCycleToIntervalMap[string(cycle)]
	return interval, ok
}

// IntervalToBillingCycle converts a BillingInterval to a BillingCycle string.
// Returns the cycle and true if an exact match exists, or ("", false) otherwise.
// Only count=1 intervals have exact BillingCycle equivalents.
func IntervalToBillingCycle(interval BillingInterval) (BillingCycle, bool) {
	if interval.count != 1 {
		return "", false
	}
	switch interval.unit {
	case IntervalUnitDay:
		return BillingCycleDaily, true
	case IntervalUnitWeek:
		return BillingCycleWeekly, true
	case IntervalUnitMonth:
		return BillingCycleMonthly, true
	case IntervalUnitYear:
		return BillingCycleYearly, true
	default:
		return "", false
	}
}
