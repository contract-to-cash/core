package pricing

import (
	"context"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// BillingCycle represents how often billing occurs.
// Defined here in pricing as it is an intrinsic property of a Price.
type BillingCycle string

const (
	BillingCycleDaily   BillingCycle = "daily"
	BillingCycleWeekly  BillingCycle = "weekly"
	BillingCycleMonthly BillingCycle = "monthly"
	BillingCycleYearly  BillingCycle = "yearly"
)

// PriceStatus represents the lifecycle status of a price.
type PriceStatus string

const (
	PriceStatusActive   PriceStatus = "active"
	PriceStatusArchived PriceStatus = "archived"
)

// Price represents how you charge for a product.
// Price is immutable after creation — amount, currency, interval, and pricingModel cannot change.
// To revise a price, create a new Price object.
type Price struct {
	id           shared.PriceID
	productID    shared.ProductID
	amount       shared.Money
	currency     shared.Currency
	interval     BillingInterval
	pricingModel PricingModel
	status       PriceStatus
	createdAt    time.Time
}

// NewPrice creates a new active Price.
// billingCycle is kept for backward compatibility; it is converted to a BillingInterval internally.
func NewPrice(
	productID shared.ProductID,
	amount shared.Money,
	currency shared.Currency,
	billingCycle BillingCycle,
	pricingModel PricingModel,
	createdAt time.Time,
) *Price {
	return &Price{
		id:           shared.NewPriceID(),
		productID:    productID,
		amount:       amount,
		currency:     currency,
		interval:     BillingCycleToInterval(billingCycle),
		pricingModel: pricingModel,
		status:       PriceStatusActive,
		createdAt:    createdAt,
	}
}

// NewPriceWithInterval creates a new active Price with a BillingInterval.
// This supports flexible billing intervals like quarterly (3 months) or semi-annual (6 months).
func NewPriceWithInterval(
	productID shared.ProductID,
	amount shared.Money,
	currency shared.Currency,
	interval BillingInterval,
	pricingModel PricingModel,
	createdAt time.Time,
) *Price {
	return &Price{
		id:           shared.NewPriceID(),
		productID:    productID,
		amount:       amount,
		currency:     currency,
		interval:     interval,
		pricingModel: pricingModel,
		status:       PriceStatusActive,
		createdAt:    createdAt,
	}
}

// ID returns the price ID.
func (p *Price) ID() shared.PriceID { return p.id }

// ProductID returns the associated product ID.
func (p *Price) ProductID() shared.ProductID { return p.productID }

// Amount returns the base price amount.
func (p *Price) Amount() shared.Money { return p.amount }

// Currency returns the currency.
func (p *Price) Currency() shared.Currency { return p.currency }

// BillingCycle returns the billing cycle string derived from the interval.
// For intervals without an exact BillingCycle match (e.g., quarterly), returns "".
// Prefer Interval() for new code.
func (p *Price) BillingCycle() BillingCycle { return p.interval.ToBillingCycle() }

// Interval returns the billing interval.
func (p *Price) Interval() BillingInterval { return p.interval }

// PricingModel returns the pricing model (for usage-based pricing).
func (p *Price) PricingModel() PricingModel { return p.pricingModel }

// Status returns the price status.
func (p *Price) Status() PriceStatus { return p.status }

// CreatedAt returns the creation timestamp.
func (p *Price) CreatedAt() time.Time { return p.createdAt }

// Archive marks the price as archived. This is the only mutation allowed on Price.
func (p *Price) Archive() error {
	if p.status == PriceStatusArchived {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			"price is already archived")
	}
	p.status = PriceStatusArchived
	return nil
}

// PriceRepository provides access to prices.
type PriceRepository interface {
	FindByID(ctx context.Context, id shared.PriceID) (*Price, error)
	FindByProductID(ctx context.Context, productID shared.ProductID) ([]*Price, error)
	FindActiveByProductID(ctx context.Context, productID shared.ProductID) ([]*Price, error)
	Save(ctx context.Context, price *Price) error
}
