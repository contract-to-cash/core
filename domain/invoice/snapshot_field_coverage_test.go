package invoice

import (
	"reflect"
	"testing"
)

// TestInvoiceSnapshot_FieldCoverage is a bidirectional guard against field
// drift. When a new field is added to Invoice, the test fails unless the
// same field is added to InvoiceSnapshot; conversely, if a field is removed
// from Invoice but left in InvoiceSnapshot, the test also fails.
//
// This is cheaper than comparing every field individually in round-trip
// tests and catches omissions at CI time.
func TestInvoiceSnapshot_FieldCoverage(t *testing.T) {
	t.Parallel()
	// loadedVersion exists only to implement optimistic locking at load time;
	// it is deliberately NOT stored separately in the snapshot
	// (InvoiceFromSnapshot sets both version and loadedVersion from Version).
	assertFieldParity(t, "Invoice", reflect.TypeOf(Invoice{}),
		"InvoiceSnapshot", reflect.TypeOf(InvoiceSnapshot{}),
		"loadedversion")
}

func TestLineItemSnapshot_FieldCoverage(t *testing.T) {
	t.Parallel()
	assertFieldParity(t, "LineItem", reflect.TypeOf(LineItem{}),
		"LineItemSnapshot", reflect.TypeOf(LineItemSnapshot{}))
}

func TestCreditNoteSnapshot_FieldCoverage(t *testing.T) {
	t.Parallel()
	assertFieldParity(t, "CreditNote", reflect.TypeOf(CreditNote{}),
		"CreditNoteSnapshot", reflect.TypeOf(CreditNoteSnapshot{}))
}

// assertFieldParity verifies bidirectional name parity between two struct
// types after case-insensitive normalization. Any field present in one but
// missing in the other produces a failure.
func assertFieldParity(t *testing.T, entityName string, entityType reflect.Type, snapshotName string, snapshotType reflect.Type, excluded ...string) {
	t.Helper()
	entityFields := collectFieldNames(entityType)
	snapshotFields := collectFieldNames(snapshotType)

	skip := make(map[string]struct{}, len(excluded))
	for _, name := range excluded {
		skip[name] = struct{}{}
	}

	for name := range entityFields {
		if _, ok := skip[name]; ok {
			continue
		}
		if _, ok := snapshotFields[name]; !ok {
			t.Errorf("%s field %q has no matching %s field. "+
				"When adding a new field to %s, add it to %s too "+
				"(or update this test's exclusion list).",
				entityName, name, snapshotName, entityName, snapshotName)
		}
	}
	for name := range snapshotFields {
		if _, ok := skip[name]; ok {
			continue
		}
		if _, ok := entityFields[name]; !ok {
			t.Errorf("%s field %q has no matching %s field. "+
				"If the field was removed from %s, remove it from %s too.",
				snapshotName, name, entityName, entityName, snapshotName)
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
