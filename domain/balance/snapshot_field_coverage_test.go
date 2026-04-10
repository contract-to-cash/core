package balance

import (
	"reflect"
	"testing"
)

// TestBalanceEntrySnapshot_FieldCoverage is a guard against field drift.
//
// BalanceEntry has a field `loadedVersion` that exists only to implement
// optimistic locking at load time; it is deliberately NOT stored separately
// in the snapshot (FromSnapshot sets both from Version). We exclude it here.
func TestBalanceEntrySnapshot_FieldCoverage(t *testing.T) {
	t.Parallel()

	excluded := map[string]struct{}{
		"loadedversion": {}, // merged into Version in the snapshot
	}

	entityFields := fieldNames(reflect.TypeOf(BalanceEntry{}))
	snapshotFields := fieldNames(reflect.TypeOf(BalanceEntrySnapshot{}))

	for name := range entityFields {
		if _, skip := excluded[name]; skip {
			continue
		}
		if _, ok := snapshotFields[name]; !ok {
			t.Errorf("BalanceEntry field %q has no matching BalanceEntrySnapshot field. "+
				"When adding a new field to BalanceEntry, add it to BalanceEntrySnapshot too.", name)
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
