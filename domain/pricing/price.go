package pricing

import (
	"context"
	"fmt"
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
	// metadata carries integrator-defined key-value pairs (issue #219), e.g.
	// "creator_id". Because Price is immutable, metadata is accepted only at
	// construction time (WithMetadata option) — there is no setter.
	metadata  map[string]string
	createdAt time.Time
}

// PriceOption is a functional option for NewPrice / NewPriceWithInterval.
type PriceOption func(*Price)

// WithMetadata sets integrator-defined metadata key-value pairs on the price
// at construction time (issue #219). The map is copied, so a caller mutating
// its own map after construction cannot alter the (immutable) Price.
func WithMetadata(m map[string]string) PriceOption {
	return func(p *Price) {
		for k, v := range m {
			if p.metadata == nil {
				p.metadata = make(map[string]string, len(m))
			}
			p.metadata[k] = v
		}
	}
}

// NewPrice creates a new active Price.
// billingCycle is kept for backward compatibility; it is converted to a BillingInterval internally.
//
// Validation (issue #196): a Price is immutable, so a nonsensical one persisted
// here is uncorrectable. NewPrice rejects
//   - a negative base amount;
//   - a base amount whose currency does not match the currency parameter (which
//     would make Amount().Currency() disagree with Currency());
//   - an unrecognized billingCycle. Unlike the lenient BillingCycleToInterval
//     (which silently falls back to Monthly), construction uses the Strict
//     converter and fails loudly, mirroring the event upcaster policy so an
//     unknown cycle never masquerades as monthly (issue #162 L-4).
func NewPrice(
	productID shared.ProductID,
	amount shared.Money,
	currency shared.Currency,
	billingCycle BillingCycle,
	pricingModel PricingModel,
	createdAt time.Time,
	opts ...PriceOption,
) (*Price, error) {
	interval, ok := BillingCycleToIntervalStrict(billingCycle)
	if !ok {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("unknown billing cycle %q (expected daily, weekly, monthly, or yearly)", billingCycle))
	}
	return newPrice(productID, amount, currency, interval, pricingModel, createdAt, opts...)
}

// NewPriceWithInterval creates a new active Price with a BillingInterval.
// This supports flexible billing intervals like quarterly (3 months) or semi-annual (6 months).
//
// Validation mirrors NewPrice (issue #196): it rejects a negative base amount, a
// base amount whose currency disagrees with the currency parameter, and a
// zero-value (uninitialized) interval.
func NewPriceWithInterval(
	productID shared.ProductID,
	amount shared.Money,
	currency shared.Currency,
	interval BillingInterval,
	pricingModel PricingModel,
	createdAt time.Time,
	opts ...PriceOption,
) (*Price, error) {
	if interval.IsZero() {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"billing interval must be set")
	}
	return newPrice(productID, amount, currency, interval, pricingModel, createdAt, opts...)
}

// newPrice is the shared constructor body for NewPrice / NewPriceWithInterval.
// It enforces the amount invariants both public constructors share (issue #196).
func newPrice(
	productID shared.ProductID,
	amount shared.Money,
	currency shared.Currency,
	interval BillingInterval,
	pricingModel PricingModel,
	createdAt time.Time,
	opts ...PriceOption,
) (*Price, error) {
	if amount.IsNegative() {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("price amount must not be negative: %s", amount.Amount().RatString()))
	}
	// A zero amount carries no currency signal, so only cross-check a non-zero
	// amount's currency against the declared currency. This lets callers pass a
	// zero base amount (e.g. pure usage-based prices) as shared.Zero(anyCurrency)
	// without a spurious mismatch, while still catching a genuinely wrong-currency
	// base amount like NewMoney(1000, USD) declared as JPY.
	if !amount.IsZero() && amount.Currency() != currency {
		return nil, shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
			fmt.Sprintf("price amount currency %s does not match declared currency %s",
				amount.Currency(), currency))
	}
	p := &Price{
		id:           shared.NewPriceID(),
		productID:    productID,
		amount:       amount,
		currency:     currency,
		interval:     interval,
		pricingModel: pricingModel,
		status:       PriceStatusActive,
		createdAt:    createdAt,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
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
//
// It returns a defensive copy so a caller mutating the result (e.g. the exported
// TieredPrice.Tiers backing array) cannot alter this Price's internal model and
// change subsequent CalculatePrice results — preserving the documented
// immutability of Price (issue #196). See clonePricingModel.
func (p *Price) PricingModel() PricingModel { return clonePricingModel(p.pricingModel) }

// Metadata returns a copy of the price's integrator-defined metadata
// (issue #219). Mutating the returned map does not affect the (immutable)
// Price. It is never nil.
func (p *Price) Metadata() map[string]string {
	cp := make(map[string]string, len(p.metadata))
	for k, v := range p.metadata {
		cp[k] = v
	}
	return cp
}

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
