package product

import (
	"testing"
)

func TestNewProduct(t *testing.T) {
	p := NewProduct("Pro Plan", "Professional tier")

	if p.ID() == "" {
		t.Error("expected non-empty product ID")
	}
	if p.Name() != "Pro Plan" {
		t.Errorf("expected name 'Pro Plan', got %q", p.Name())
	}
	if p.Description() != "Professional tier" {
		t.Errorf("expected description 'Professional tier', got %q", p.Description())
	}
	if p.Status() != ProductStatusActive {
		t.Errorf("expected status active, got %s", p.Status())
	}
	if p.CreatedAt().IsZero() {
		t.Error("expected non-zero createdAt")
	}
}

func TestProduct_AddFeature(t *testing.T) {
	p := NewProduct("Test", "")
	p.AddFeature(Feature{Name: "SSO", Included: true})
	p.AddFeature(Feature{Name: "API Access", Included: false})

	features := p.Features()
	if len(features) != 2 {
		t.Fatalf("expected 2 features, got %d", len(features))
	}
	if features[0].Name != "SSO" {
		t.Errorf("expected first feature 'SSO', got %q", features[0].Name)
	}
}

func TestProduct_AddUsageMetric(t *testing.T) {
	p := NewProduct("Test", "")
	p.AddUsageMetric(UsageMetric{Name: "api_calls", IncludedQuantity: 1000})

	metrics := p.UsageMetrics()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Name != "api_calls" {
		t.Errorf("expected metric name 'api_calls', got %q", metrics[0].Name)
	}
}

func TestProduct_SetMetadata(t *testing.T) {
	p := NewProduct("Test", "")
	p.SetMetadata("tier", "enterprise")

	meta := p.Metadata()
	if meta["tier"] != "enterprise" {
		t.Errorf("expected metadata tier=enterprise, got %q", meta["tier"])
	}
}

func TestProduct_Archive(t *testing.T) {
	p := NewProduct("Test", "")

	if err := p.Archive(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != ProductStatusArchived {
		t.Errorf("expected status archived, got %s", p.Status())
	}
}

func TestProduct_Archive_AlreadyArchived(t *testing.T) {
	p := NewProduct("Test", "")
	_ = p.Archive()

	err := p.Archive()
	if err == nil {
		t.Error("expected error when archiving already archived product")
	}
}

func TestProduct_Features_ReturnsCopy(t *testing.T) {
	p := NewProduct("Test", "")
	p.AddFeature(Feature{Name: "SSO", Included: true})

	features := p.Features()
	features[0].Name = "MODIFIED"

	if p.Features()[0].Name != "SSO" {
		t.Error("Features() should return a copy, not a reference")
	}
}

func TestProduct_UsageMetrics_ReturnsCopy(t *testing.T) {
	p := NewProduct("Test", "")
	p.AddUsageMetric(UsageMetric{Name: "api_calls", IncludedQuantity: 1000})

	metrics := p.UsageMetrics()
	metrics[0].Name = "MODIFIED"

	if p.UsageMetrics()[0].Name != "api_calls" {
		t.Error("UsageMetrics() should return a copy, not a reference")
	}
}

func TestProduct_Metadata_ReturnsCopy(t *testing.T) {
	p := NewProduct("Test", "")
	p.SetMetadata("key", "value")

	meta := p.Metadata()
	meta["key"] = "MODIFIED"

	if p.Metadata()["key"] != "value" {
		t.Error("Metadata() should return a copy, not a reference")
	}
}
