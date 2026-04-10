package usage

import (
	"reflect"
	"testing"
)

// TestUsageRecordSnapshot_FieldCoverage is a bidirectional guard against field drift.
func TestUsageRecordSnapshot_FieldCoverage(t *testing.T) {
	t.Parallel()

	entityFields := fieldNames(reflect.TypeOf(UsageRecord{}))
	snapshotFields := fieldNames(reflect.TypeOf(UsageRecordSnapshot{}))

	for name := range entityFields {
		if _, ok := snapshotFields[name]; !ok {
			t.Errorf("UsageRecord field %q has no matching UsageRecordSnapshot field", name)
		}
	}
	for name := range snapshotFields {
		if _, ok := entityFields[name]; !ok {
			t.Errorf("UsageRecordSnapshot field %q has no matching UsageRecord field", name)
		}
	}
}

func fieldNames(t reflect.Type) map[string]struct{} {
	out := make(map[string]struct{}, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := t.Field(i).Name
		low := make([]byte, len(name))
		for j := 0; j < len(name); j++ {
			c := name[j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			low[j] = c
		}
		out[string(low)] = struct{}{}
	}
	return out
}
