// pointer_isolation_test.go — see issue #96.
package product

import (
	"testing"
	"time"
)

// TestProduct_Features_GetterIsDefensivelyCopied verifies that mutating the
// *int64 returned inside Features() does NOT alter the product's internal
// state. Although Features() already returns a copy of the slice, the
// Feature.Limit pointer field is a nested pointer and must be deep-copied.
func TestProduct_Features_GetterIsDefensivelyCopied(t *testing.T) {
	limit := int64(100)
	p := NewProduct("pro", "desc", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p.AddFeature(Feature{Name: "api_calls", Included: true, Limit: &limit})

	// Mutate the Limit pointer returned via the getter.
	features := p.Features()
	if features[0].Limit == nil {
		t.Fatal("Limit must not be nil")
	}
	*features[0].Limit = 9999

	got := p.Features()
	if got[0].Limit == nil || *got[0].Limit != 100 {
		t.Errorf("Product.Features()[0].Limit leaks internal pointer: got %v, want 100", got[0].Limit)
	}
}

// TestProduct_AddFeature_IntakeIsDefensivelyCopied verifies that mutating
// the caller-owned *int64 after passing it via AddFeature does NOT alter
// the product's internal state. Pattern C.
func TestProduct_AddFeature_IntakeIsDefensivelyCopied(t *testing.T) {
	limit := int64(100)
	p := NewProduct("pro", "desc", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p.AddFeature(Feature{Name: "api_calls", Included: true, Limit: &limit})

	limit = 9999

	got := p.Features()
	if got[0].Limit == nil || *got[0].Limit != 100 {
		t.Errorf("AddFeature does not defend Feature.Limit at intake: got %v, want 100", got[0].Limit)
	}
}

// TestProduct_Features_NilLimit_Safe verifies that a nil Feature.Limit is
// preserved through the getter without panicking.
func TestProduct_Features_NilLimit_Safe(t *testing.T) {
	p := NewProduct("pro", "desc", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p.AddFeature(Feature{Name: "unlimited", Included: true, Limit: nil})

	got := p.Features()
	if got[0].Limit != nil {
		t.Errorf("expected nil Limit, got %v", got[0].Limit)
	}
}
