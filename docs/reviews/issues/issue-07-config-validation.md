# Add validation to BillingConfig and service constructors

**Labels**: `validation`, `medium-priority`, `framework`

## Problem

`BillingConfig` accepts any values without validation:

```go
type BillingConfig struct {
    GracePeriod      time.Duration
    DaysUntilDue     int
    CollectionMethod string
}
```

- `GracePeriod: -1 * time.Hour` — silently accepted, causes undefined behavior
- `DaysUntilDue: -30` — negative due dates
- `CollectionMethod: "invalid"` — no validation against known values
- Zero-value config (`BillingConfig{}`) uses undocumented defaults scattered across methods

Similarly, `WebhookProcessorConfig` has defaults baked into `ProcessWebhook()` rather than validated at construction time.

## Proposal

```go
// Validated constructor
func NewBillingConfig(opts ...BillingConfigOption) (BillingConfig, error) {
    cfg := BillingConfig{
        GracePeriod:      1 * time.Hour,      // documented default
        DaysUntilDue:     30,                  // documented default
        CollectionMethod: CollectionAutoCharge, // documented default
    }
    for _, opt := range opts {
        opt(&cfg)
    }
    return cfg, cfg.validate()
}

func (c BillingConfig) validate() error {
    if c.GracePeriod < 0 {
        return fmt.Errorf("GracePeriod must be non-negative, got %v", c.GracePeriod)
    }
    if c.DaysUntilDue < 0 {
        return fmt.Errorf("DaysUntilDue must be non-negative, got %d", c.DaysUntilDue)
    }
    switch c.CollectionMethod {
    case CollectionAutoCharge, CollectionSendInvoice:
        // valid
    default:
        return fmt.Errorf("unknown CollectionMethod: %q", c.CollectionMethod)
    }
    return nil
}

// Type-safe CollectionMethod
type CollectionMethod string
const (
    CollectionAutoCharge  CollectionMethod = "charge_automatically"
    CollectionSendInvoice CollectionMethod = "send_invoice"
)
```

## Acceptance Criteria

- [ ] `NewBillingConfig()` constructor with validation and documented defaults
- [ ] `CollectionMethod` as typed string with constants
- [ ] `WebhookProcessorConfig` validation (TimestampTolerance > 0, MaxRetries >= 0)
- [ ] `SnapshotService` interval validation (> 0)
- [ ] Existing `BillingConfig{}` literal usage updated or kept working (backward compatibility)
- [ ] Error messages include field name and invalid value
- [ ] Tests for all validation paths
