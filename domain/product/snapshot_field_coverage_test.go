package product

import (
	"reflect"
	"testing"
)

// TestProductSnapshot_FieldCoverage is a bidirectional guard against field drift.
func TestProductSnapshot_FieldCoverage(t *testing.T) {
	t.Parallel()

	entityFields := fieldNames(reflect.TypeOf(Product{}))
	snapshotFields := fieldNames(reflect.TypeOf(ProductSnapshot{}))

	for name := range entityFields {
		if _, ok := snapshotFields[name]; !ok {
			t.Errorf("Product field %q has no matching ProductSnapshot field", name)
		}
	}
	for name := range snapshotFields {
		if _, ok := entityFields[name]; !ok {
			t.Errorf("ProductSnapshot field %q has no matching Product field", name)
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
