package payment

import (
	"reflect"
	"testing"
)

// TestPaymentSnapshot_FieldCoverage is a bidirectional guard against field drift.
func TestPaymentSnapshot_FieldCoverage(t *testing.T) {
	t.Parallel()

	entityFields := fieldNames(reflect.TypeOf(Payment{}))
	snapshotFields := fieldNames(reflect.TypeOf(PaymentSnapshot{}))

	for name := range entityFields {
		if _, ok := snapshotFields[name]; !ok {
			t.Errorf("Payment field %q has no matching PaymentSnapshot field. "+
				"When adding a new field to Payment, add it to PaymentSnapshot too.", name)
		}
	}
	for name := range snapshotFields {
		if _, ok := entityFields[name]; !ok {
			t.Errorf("PaymentSnapshot field %q has no matching Payment field. "+
				"If the field was removed from Payment, remove it from PaymentSnapshot too.", name)
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
