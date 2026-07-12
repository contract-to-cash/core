package plugin

import (
	"encoding/json"
	"math"
	"testing"
)

func TestConfig_Int(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		key         string
		wantValue   int
		wantPresent bool
		wantErr     bool
	}{
		{"absent key", Config{}, "priority", 0, false, false},
		{"absent key on nil map", nil, "priority", 0, false, false},
		{"int", Config{"priority": 42}, "priority", 42, true, false},
		{"negative int", Config{"priority": -7}, "priority", -7, true, false},
		{"integral float64 (json)", Config{"priority": float64(42)}, "priority", 42, true, false},
		{"integral negative float64", Config{"priority": float64(-3)}, "priority", -3, true, false},
		{"zero float64", Config{"priority": float64(0)}, "priority", 0, true, false},
		{"fractional float64", Config{"priority": 42.5}, "priority", 0, false, true},
		{"NaN", Config{"priority": math.NaN()}, "priority", 0, false, true},
		{"+Inf", Config{"priority": math.Inf(1)}, "priority", 0, false, true},
		{"-Inf", Config{"priority": math.Inf(-1)}, "priority", 0, false, true},
		{"string", Config{"priority": "high"}, "priority", 0, false, true},
		{"bool", Config{"priority": true}, "priority", 0, false, true},
		{"nil value", Config{"priority": nil}, "priority", 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, present, err := tt.config.Int(tt.key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Int(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
			}
			if present != tt.wantPresent {
				t.Errorf("Int(%q) present = %v, want %v", tt.key, present, tt.wantPresent)
			}
			if got != tt.wantValue {
				t.Errorf("Int(%q) = %d, want %d", tt.key, got, tt.wantValue)
			}
		})
	}
}

// TestConfig_Int_JSONRoundTrip exercises the motivating case for float64
// acceptance (issue #239): a Config decoded from JSON carries float64 numbers.
func TestConfig_Int_JSONRoundTrip(t *testing.T) {
	var config Config
	if err := json.Unmarshal([]byte(`{"priority": 42, "maxCouponsPerInvoice": 3}`), &config); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got, present, err := config.Int("priority")
	if err != nil || !present || got != 42 {
		t.Errorf("Int(priority) = (%d, %v, %v), want (42, true, nil)", got, present, err)
	}
	got, present, err = config.Int("maxCouponsPerInvoice")
	if err != nil || !present || got != 3 {
		t.Errorf("Int(maxCouponsPerInvoice) = (%d, %v, %v), want (3, true, nil)", got, present, err)
	}
}

func TestConfig_Bool(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		key         string
		wantValue   bool
		wantPresent bool
		wantErr     bool
	}{
		{"absent key", Config{}, "allowStacking", false, false, false},
		{"absent key on nil map", nil, "allowStacking", false, false, false},
		{"true", Config{"allowStacking": true}, "allowStacking", true, true, false},
		{"false", Config{"allowStacking": false}, "allowStacking", false, true, false},
		{"string", Config{"allowStacking": "yes"}, "allowStacking", false, false, true},
		{"int", Config{"allowStacking": 1}, "allowStacking", false, false, true},
		{"nil value", Config{"allowStacking": nil}, "allowStacking", false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, present, err := tt.config.Bool(tt.key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Bool(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
			}
			if present != tt.wantPresent {
				t.Errorf("Bool(%q) present = %v, want %v", tt.key, present, tt.wantPresent)
			}
			if got != tt.wantValue {
				t.Errorf("Bool(%q) = %v, want %v", tt.key, got, tt.wantValue)
			}
		})
	}
}
