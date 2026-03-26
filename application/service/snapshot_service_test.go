package service

import (
	"testing"
)

func TestShouldCreateSnapshot_Interval(t *testing.T) {
	svc := NewSnapshotService(nil, nil, 100)

	tests := []struct {
		version  int
		expected bool
	}{
		{0, false},
		{1, false},
		{50, false},
		{99, false},
		{100, true},
		{101, false},
		{200, true},
		{300, true},
		{150, false},
	}

	for _, tt := range tests {
		got := svc.ShouldCreateSnapshot(tt.version)
		if got != tt.expected {
			t.Errorf("ShouldCreateSnapshot(%d) = %v, want %v", tt.version, got, tt.expected)
		}
	}
}

func TestShouldCreateSnapshot_DefaultInterval(t *testing.T) {
	svc := NewSnapshotService(nil, nil, 0)
	if svc.interval != DefaultSnapshotInterval {
		t.Errorf("expected default interval %d, got %d", DefaultSnapshotInterval, svc.interval)
	}
}

func TestShouldCreateSnapshot_CustomInterval(t *testing.T) {
	svc := NewSnapshotService(nil, nil, 50)

	if !svc.ShouldCreateSnapshot(50) {
		t.Error("expected true for version 50 with interval 50")
	}
	if !svc.ShouldCreateSnapshot(100) {
		t.Error("expected true for version 100 with interval 50")
	}
	if svc.ShouldCreateSnapshot(75) {
		t.Error("expected false for version 75 with interval 50")
	}
}
