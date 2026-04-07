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
func (i BillingInterval) AddTo(t time.Time) time.Time {
	switch i.unit {
	case IntervalUnitDay:
		return t.AddDate(0, 0, i.count)
	case IntervalUnitWeek:
		return t.AddDate(0, 0, 7*i.count)
	case IntervalUnitMonth:
		return t.AddDate(0, i.count, 0)
	case IntervalUnitYear:
		return t.AddDate(i.count, 0, 0)
	default:
		// Fallback to monthly (should not happen with validation)
		return t.AddDate(0, i.count, 0)
	}
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
func (i BillingInterval) MarshalJSON() ([]byte, error) {
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
	// Try old format first: plain string like "monthly"
	var oldFormat string
	if err := json.Unmarshal(data, &oldFormat); err == nil {
		if interval, ok := billingCycleToIntervalMap[oldFormat]; ok {
			*i = interval
			return nil
		}
		// Not a known old format string, try as new format
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
	if v.Count < 1 {
		return fmt.Errorf("invalid interval count: %d", v.Count)
	}
	i.unit = v.Unit
	i.count = v.Count
	return nil
}

// BillingCycleToInterval converts a BillingCycle string to a BillingInterval.
// Unknown cycles default to Monthly().
func BillingCycleToInterval(cycle BillingCycle) BillingInterval {
	if interval, ok := billingCycleToIntervalMap[string(cycle)]; ok {
		return interval
	}
	return Monthly()
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
