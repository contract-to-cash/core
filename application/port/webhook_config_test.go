package port

import (
	"strings"
	"testing"
	"time"
)

func TestWebhookProcessorConfig_Validate_Defaults(t *testing.T) {
	// Zero-value config should be valid (uses defaults)
	cfg := WebhookProcessorConfig{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected zero-value config to be valid, got: %v", err)
	}
}

func TestWebhookProcessorConfig_Validate_NegativeTimestampTolerance(t *testing.T) {
	cfg := WebhookProcessorConfig{
		TimestampTolerance: -1 * time.Minute,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for negative TimestampTolerance, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "TimestampTolerance") {
		t.Errorf("error message %q does not contain field name 'TimestampTolerance'", got)
	}
}

func TestWebhookProcessorConfig_Validate_NegativeMaxEventAge(t *testing.T) {
	cfg := WebhookProcessorConfig{
		MaxEventAge: -1 * time.Hour,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for negative MaxEventAge, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "MaxEventAge") {
		t.Errorf("error message %q does not contain field name 'MaxEventAge'", got)
	}
}

func TestWebhookProcessorConfig_Validate_NegativeMaxRetries(t *testing.T) {
	cfg := WebhookProcessorConfig{
		MaxRetries: -1,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for negative MaxRetries, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "MaxRetries") {
		t.Errorf("error message %q does not contain field name 'MaxRetries'", got)
	}
}

func TestWebhookProcessorConfig_Validate_NegativeDeduplicationTTL(t *testing.T) {
	cfg := WebhookProcessorConfig{
		DeduplicationTTL: -1 * time.Hour,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for negative DeduplicationTTL, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "DeduplicationTTL") {
		t.Errorf("error message %q does not contain field name 'DeduplicationTTL'", got)
	}
}

func TestWebhookProcessorConfig_Validate_NegativeRetryBackoff(t *testing.T) {
	cfg := WebhookProcessorConfig{
		RetryBackoff: -1 * time.Second,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for negative RetryBackoff, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "RetryBackoff") {
		t.Errorf("error message %q does not contain field name 'RetryBackoff'", got)
	}
}

func TestWebhookProcessorConfig_Validate_ValidConfig(t *testing.T) {
	cfg := WebhookProcessorConfig{
		TimestampTolerance: 10 * time.Minute,
		DeduplicationTTL:   48 * time.Hour,
		MaxRetries:         5,
		RetryBackoff:       2 * time.Second,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config to pass validation, got: %v", err)
	}
}

func TestWebhookProcessorConfig_Validate_ZeroMaxRetries_Allowed(t *testing.T) {
	// Zero MaxRetries means "no retries" — valid use case
	cfg := WebhookProcessorConfig{
		MaxRetries: 0,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected zero MaxRetries to be valid, got: %v", err)
	}
}
