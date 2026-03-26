package shared

import (
	"testing"
	"time"
)

func TestSystemClock_ReturnsUTC(t *testing.T) {
	clock := SystemClock{}
	now := clock.Now()
	if now.Location() != time.UTC {
		t.Errorf("expected UTC, got %s", now.Location())
	}
}

func TestFixedClock_ReturnsSameTime(t *testing.T) {
	fixed := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	clock := FixedClock{FixedTime: fixed}
	if clock.Now() != fixed {
		t.Errorf("expected %v, got %v", fixed, clock.Now())
	}
	if clock.Now() != fixed {
		t.Error("expected same time on second call")
	}
}
