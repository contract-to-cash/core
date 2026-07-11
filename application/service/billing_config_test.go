package service

import (
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestCollectionMethodConstants(t *testing.T) {
	// Verify typed constants exist and have expected string values
	if CollectionAutoCharge != "charge_automatically" {
		t.Errorf("CollectionAutoCharge = %q, want %q", CollectionAutoCharge, "charge_automatically")
	}
	if CollectionSendInvoice != "send_invoice" {
		t.Errorf("CollectionSendInvoice = %q, want %q", CollectionSendInvoice, "send_invoice")
	}
}

func TestNewBillingConfig_Defaults(t *testing.T) {
	cfg, err := NewBillingConfig()
	if err != nil {
		t.Fatalf("NewBillingConfig() returned error: %v", err)
	}

	if cfg.GracePeriod != 1*time.Hour {
		t.Errorf("GracePeriod = %v, want 1h", cfg.GracePeriod)
	}
	if cfg.DaysUntilDue != 30 {
		t.Errorf("DaysUntilDue = %d, want 30", cfg.DaysUntilDue)
	}
	if cfg.CollectionMethod != CollectionAutoCharge {
		t.Errorf("CollectionMethod = %q, want %q", cfg.CollectionMethod, CollectionAutoCharge)
	}
	if cfg.TaxRoundingMode != shared.RoundDown {
		t.Errorf("TaxRoundingMode = %q, want %q", cfg.TaxRoundingMode, shared.RoundDown)
	}
}

func TestNewBillingConfig_TaxRoundingMode(t *testing.T) {
	cfg, err := NewBillingConfig(WithTaxRoundingMode(shared.RoundHalfUp))
	if err != nil {
		t.Fatalf("NewBillingConfig() returned error: %v", err)
	}
	if cfg.TaxRoundingMode != shared.RoundHalfUp {
		t.Errorf("TaxRoundingMode = %q, want %q", cfg.TaxRoundingMode, shared.RoundHalfUp)
	}

	// Invalid mode is rejected.
	if _, err := NewBillingConfig(WithTaxRoundingMode("sideways")); err == nil {
		t.Fatal("expected error for invalid TaxRoundingMode, got nil")
	} else if !containsStr(err.Error(), "TaxRoundingMode") {
		t.Errorf("error message %q does not name TaxRoundingMode", err.Error())
	}
}

func TestBillingConfig_effectiveTaxRoundingMode(t *testing.T) {
	// Zero value falls back to the documented RoundDown default.
	if got := (BillingConfig{}).effectiveTaxRoundingMode(); got != shared.RoundDown {
		t.Errorf("zero-value effectiveTaxRoundingMode() = %q, want %q", got, shared.RoundDown)
	}
	// Explicit value is respected verbatim.
	if got := (BillingConfig{TaxRoundingMode: shared.RoundUp}).effectiveTaxRoundingMode(); got != shared.RoundUp {
		t.Errorf("explicit effectiveTaxRoundingMode() = %q, want %q", got, shared.RoundUp)
	}
}

func TestNewBillingConfig_WithOptions(t *testing.T) {
	cfg, err := NewBillingConfig(
		WithGracePeriod(2*time.Hour),
		WithDaysUntilDue(45),
		WithCollectionMethod(CollectionSendInvoice),
	)
	if err != nil {
		t.Fatalf("NewBillingConfig() returned error: %v", err)
	}

	if cfg.GracePeriod != 2*time.Hour {
		t.Errorf("GracePeriod = %v, want 2h", cfg.GracePeriod)
	}
	if cfg.DaysUntilDue != 45 {
		t.Errorf("DaysUntilDue = %d, want 45", cfg.DaysUntilDue)
	}
	if cfg.CollectionMethod != CollectionSendInvoice {
		t.Errorf("CollectionMethod = %q, want %q", cfg.CollectionMethod, CollectionSendInvoice)
	}
}

func TestNewBillingConfig_NegativeGracePeriod(t *testing.T) {
	_, err := NewBillingConfig(WithGracePeriod(-1 * time.Hour))
	if err == nil {
		t.Fatal("expected error for negative GracePeriod, got nil")
	}
	// Error message should include field name and invalid value
	want := "GracePeriod"
	if got := err.Error(); !containsStr(got, want) {
		t.Errorf("error message %q does not contain %q", got, want)
	}
}

func TestNewBillingConfig_NegativeDaysUntilDue(t *testing.T) {
	_, err := NewBillingConfig(WithDaysUntilDue(-30))
	if err == nil {
		t.Fatal("expected error for negative DaysUntilDue, got nil")
	}
	want := "DaysUntilDue"
	if got := err.Error(); !containsStr(got, want) {
		t.Errorf("error message %q does not contain %q", got, want)
	}
}

func TestNewBillingConfig_InvalidCollectionMethod(t *testing.T) {
	_, err := NewBillingConfig(WithCollectionMethod("invalid"))
	if err == nil {
		t.Fatal("expected error for invalid CollectionMethod, got nil")
	}
	want := "CollectionMethod"
	if got := err.Error(); !containsStr(got, want) {
		t.Errorf("error message %q does not contain %q", got, want)
	}
}

func TestNewBillingConfig_ZeroGracePeriod_Allowed(t *testing.T) {
	// Zero GracePeriod means "finalize immediately" — valid use case
	cfg, err := NewBillingConfig(WithGracePeriod(0))
	if err != nil {
		t.Fatalf("NewBillingConfig() returned error: %v", err)
	}
	if cfg.GracePeriod != 0 {
		t.Errorf("GracePeriod = %v, want 0", cfg.GracePeriod)
	}
}

func TestNewBillingConfig_ZeroDaysUntilDue_Allowed(t *testing.T) {
	// Zero DaysUntilDue is a valid stored value; validate() does not reject it.
	// It is a sentinel meaning "use the default at usage time": GenerateInvoice
	// resolves it via effectiveDaysUntilDue() to a 30-day due date rather than an
	// immediately-due invoice (see TestBillingConfig_effectiveDaysUntilDue).
	cfg, err := NewBillingConfig(WithDaysUntilDue(0))
	if err != nil {
		t.Fatalf("NewBillingConfig() returned error: %v", err)
	}
	if cfg.DaysUntilDue != 0 {
		t.Errorf("DaysUntilDue = %d, want 0", cfg.DaysUntilDue)
	}
}

func TestBillingConfig_effectiveDaysUntilDue(t *testing.T) {
	// Zero value falls back to the documented 30-day default.
	if got := (BillingConfig{}).effectiveDaysUntilDue(); got != defaultDaysUntilDue {
		t.Errorf("zero-value effectiveDaysUntilDue() = %d, want %d", got, defaultDaysUntilDue)
	}
	// Explicit value is respected verbatim.
	if got := (BillingConfig{DaysUntilDue: 45}).effectiveDaysUntilDue(); got != 45 {
		t.Errorf("explicit effectiveDaysUntilDue() = %d, want 45", got)
	}
}

func TestBillingConfig_ZeroValueBackwardCompatibility(t *testing.T) {
	// BillingConfig{} struct literal must still work.
	// Zero-value CollectionMethod is treated as CollectionAutoCharge at usage time.
	cfg := BillingConfig{}
	if cfg.GracePeriod != 0 {
		t.Errorf("zero-value GracePeriod = %v, want 0", cfg.GracePeriod)
	}
	if cfg.DaysUntilDue != 0 {
		t.Errorf("zero-value DaysUntilDue = %d, want 0", cfg.DaysUntilDue)
	}
	if cfg.CollectionMethod != "" {
		t.Errorf("zero-value CollectionMethod = %q, want empty", cfg.CollectionMethod)
	}
}

func TestBillingConfig_ConstantAssignment(t *testing.T) {
	// CollectionMethod is a distinct type. Use typed constants for assignment.
	cfg := BillingConfig{
		CollectionMethod: CollectionAutoCharge,
		DaysUntilDue:     30,
	}
	if cfg.CollectionMethod != CollectionAutoCharge {
		t.Errorf("CollectionMethod = %q, want %q", cfg.CollectionMethod, CollectionAutoCharge)
	}
}

// containsStr is a helper to check if s contains substr.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
