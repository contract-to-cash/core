package shared

import "time"

// Clock provides time generation. All domain and application code must use
// this interface instead of time.Now() directly, to enable deterministic testing.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production Clock implementation that returns UTC time.
type SystemClock struct{}

// Now returns the current time in UTC.
func (c SystemClock) Now() time.Time {
	return time.Now().UTC()
}

// FixedClock is a testing Clock that always returns the same time.
type FixedClock struct {
	FixedTime time.Time
}

// Now returns the fixed time.
func (c FixedClock) Now() time.Time {
	return c.FixedTime
}
