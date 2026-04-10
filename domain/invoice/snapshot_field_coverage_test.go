package invoice

import (
	"reflect"
	"testing"
)

// TestInvoiceSnapshot_FieldCoverage is a guard against field drift.
// When a new field is added to Invoice, the test fails unless the same
// field is added to InvoiceSnapshot (or explicitly excluded below).
//
// This is cheaper than comparing every field individually in round-trip
// tests and catches omissions at CI time.
func TestInvoiceSnapshot_FieldCoverage(t *testing.T) {
	t.Parallel()

	entityType := reflect.TypeOf(Invoice{})
	snapshotType := reflect.TypeOf(InvoiceSnapshot{})

	// Entity fields are unexported. Snapshot fields are exported. Compare
	// names case-insensitively after snake/camel normalization.
	entityFields := collectFieldNames(entityType)
	snapshotFields := collectFieldNames(snapshotType)

	for name := range entityFields {
		if _, ok := snapshotFields[name]; !ok {
			t.Errorf("Invoice field %q has no matching InvoiceSnapshot field. "+
				"When adding a new field to Invoice, add it to InvoiceSnapshot too "+
				"(or update this test's exclusion list).", name)
		}
	}
}

func TestLineItemSnapshot_FieldCoverage(t *testing.T) {
	t.Parallel()

	entityFields := collectFieldNames(reflect.TypeOf(LineItem{}))
	snapshotFields := collectFieldNames(reflect.TypeOf(LineItemSnapshot{}))

	for name := range entityFields {
		if _, ok := snapshotFields[name]; !ok {
			t.Errorf("LineItem field %q has no matching LineItemSnapshot field", name)
		}
	}
}

func TestCreditNoteSnapshot_FieldCoverage(t *testing.T) {
	t.Parallel()

	entityFields := collectFieldNames(reflect.TypeOf(CreditNote{}))
	snapshotFields := collectFieldNames(reflect.TypeOf(CreditNoteSnapshot{}))

	for name := range entityFields {
		if _, ok := snapshotFields[name]; !ok {
			t.Errorf("CreditNote field %q has no matching CreditNoteSnapshot field", name)
		}
	}
}

// collectFieldNames returns a set of lowercased field names for comparison.
func collectFieldNames(t reflect.Type) map[string]struct{} {
	out := make(map[string]struct{}, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		out[lower(t.Field(i).Name)] = struct{}{}
	}
	return out
}

func lower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
