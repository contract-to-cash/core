package service

import (
	"fmt"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// CollectionMethod represents the invoice collection method.
type CollectionMethod string

// Collection method constants.
const (
	// CollectionAutoCharge charges the customer automatically after invoice finalization.
	CollectionAutoCharge CollectionMethod = "charge_automatically"
	// CollectionSendInvoice sends the invoice to the customer and waits for payment.
	CollectionSendInvoice CollectionMethod = "send_invoice"
)

// validCollectionMethods is the set of allowed CollectionMethod values.
var validCollectionMethods = map[CollectionMethod]bool{
	CollectionAutoCharge:  true,
	CollectionSendInvoice: true,
}

// defaultDaysUntilDue is the fallback number of days added to an invoice's issue
// date to compute its due date, applied when BillingConfig.DaysUntilDue is left
// at its zero value. This keeps a zero-value BillingConfig{} from producing an
// immediately-due (issue date == due date) invoice.
const defaultDaysUntilDue = 30

// effectiveDaysUntilDue returns the configured DaysUntilDue, falling back to
// defaultDaysUntilDue when the field is left at its zero value. This matches the
// "zero values are treated as defaults at usage time" contract documented on
// BillingConfig and the default set by NewBillingConfig.
func (c BillingConfig) effectiveDaysUntilDue() int {
	if c.DaysUntilDue == 0 {
		return defaultDaysUntilDue
	}
	return c.DaysUntilDue
}

// BillingConfigOption configures a BillingConfig via the functional options pattern.
type BillingConfigOption func(*BillingConfig)

// WithGracePeriod sets the grace period for invoice finalization.
func WithGracePeriod(d time.Duration) BillingConfigOption {
	return func(c *BillingConfig) { c.GracePeriod = d }
}

// WithDaysUntilDue sets the number of days from invoice creation to due date.
func WithDaysUntilDue(days int) BillingConfigOption {
	return func(c *BillingConfig) { c.DaysUntilDue = days }
}

// WithCollectionMethod sets the invoice collection method.
func WithCollectionMethod(m CollectionMethod) BillingConfigOption {
	return func(c *BillingConfig) { c.CollectionMethod = m }
}

// WithAllowPartialPayment sets whether generated invoices accept partial
// payments. Default false (a payment must settle the full amount due).
func WithAllowPartialPayment(allow bool) BillingConfigOption {
	return func(c *BillingConfig) { c.AllowPartialPayment = allow }
}

// NewBillingConfig creates a validated BillingConfig with documented defaults.
// Options override the defaults. Returns an error if any value is invalid.
//
// Defaults:
//   - GracePeriod: 1 hour
//   - DaysUntilDue: 30
//   - CollectionMethod: CollectionAutoCharge ("charge_automatically")
func NewBillingConfig(opts ...BillingConfigOption) (BillingConfig, error) {
	cfg := BillingConfig{
		GracePeriod:      1 * time.Hour,
		DaysUntilDue:     defaultDaysUntilDue,
		CollectionMethod: CollectionAutoCharge,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return BillingConfig{}, err
	}
	return cfg, nil
}

// validate checks that all BillingConfig fields have valid values.
func (c BillingConfig) validate() error {
	if c.GracePeriod < 0 {
		return shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("GracePeriod must not be negative, got %v", c.GracePeriod))
	}
	if c.DaysUntilDue < 0 {
		return shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("DaysUntilDue must not be negative, got %d", c.DaysUntilDue))
	}
	if c.CollectionMethod != "" && !validCollectionMethods[c.CollectionMethod] {
		return shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("CollectionMethod %q is invalid, must be %q or %q",
				c.CollectionMethod, CollectionAutoCharge, CollectionSendInvoice))
	}
	return nil
}
