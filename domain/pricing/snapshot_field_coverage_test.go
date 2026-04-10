package pricing

import (
	"reflect"
	"testing"
)

func TestPriceSnapshot_FieldCoverage(t *testing.T) {
	t.Parallel()

	entityFields := fieldNames(reflect.TypeOf(Price{}))
	snapshotFields := fieldNames(reflect.TypeOf(PriceSnapshot{}))

	for name := range entityFields {
		if _, ok := snapshotFields[name]; !ok {
			t.Errorf("Price field %q has no matching PriceSnapshot field", name)
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
