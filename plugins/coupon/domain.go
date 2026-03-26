package coupon

import (
	"math/big"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// CouponID uniquely identifies a coupon.
type CouponID string

// CouponType represents the type of discount a coupon provides.
type CouponType string

const (
	// CouponTypePercentage applies a percentage discount.
	CouponTypePercentage CouponType = "percentage"
	// CouponTypeFixed applies a fixed amount discount.
	CouponTypeFixed CouponType = "fixed"
)

// Coupon represents a discount coupon that can be applied to an invoice.
type Coupon struct {
	id           CouponID
	code         string
	couponType   CouponType
	value        *big.Rat        // percentage: discount rate (e.g. 10/100), fixed: discount amount
	currency     shared.Currency // used only for fixed type
	minAmount    *shared.Money   // minimum purchase amount (nil = no minimum)
	maxDiscount  *shared.Money   // maximum discount amount (nil = no cap)
	validFrom    time.Time
	validUntil   time.Time
	usageLimit   *int
	usedCount    int
	applicableTo []string // applicable target identifiers
}

// NewCoupon creates a new Coupon.
func NewCoupon(
	id CouponID,
	code string,
	couponType CouponType,
	value *big.Rat,
	currency shared.Currency,
	minAmount *shared.Money,
	maxDiscount *shared.Money,
	validFrom, validUntil time.Time,
	usageLimit *int,
	usedCount int,
	applicableTo []string,
) *Coupon {
	return &Coupon{
		id:           id,
		code:         code,
		couponType:   couponType,
		value:        value,
		currency:     currency,
		minAmount:    minAmount,
		maxDiscount:  maxDiscount,
		validFrom:    validFrom,
		validUntil:   validUntil,
		usageLimit:   usageLimit,
		usedCount:    usedCount,
		applicableTo: applicableTo,
	}
}

// ID returns the coupon ID.
func (c *Coupon) ID() CouponID { return c.id }

// Code returns the coupon code.
func (c *Coupon) Code() string { return c.code }

// IsValid returns true if the coupon is valid at the given time.
// A coupon is valid when:
//   - the current time is within [validFrom, validUntil]
//   - the usage limit has not been reached (if set)
func (c *Coupon) IsValid(at time.Time) bool {
	if at.Before(c.validFrom) || at.After(c.validUntil) {
		return false
	}
	if c.usageLimit != nil && c.usedCount >= *c.usageLimit {
		return false
	}
	return true
}

// CalculateDiscount calculates the discount amount for the given subtotal.
// For percentage type: subtotal * value, capped by maxDiscount if set.
// For fixed type: the fixed value converted to Money, capped by maxDiscount if set.
func (c *Coupon) CalculateDiscount(subtotal shared.Money) shared.Money {
	var discount shared.Money

	switch c.couponType {
	case CouponTypePercentage:
		discount = subtotal.Multiply(c.value)
	case CouponTypeFixed:
		discount = shared.NewMoney(c.value, c.currency)
	default:
		return shared.Zero(subtotal.Currency())
	}

	// Apply maxDiscount cap
	if c.maxDiscount != nil {
		capped, err := discount.Min(*c.maxDiscount)
		if err == nil {
			discount = capped
		}
	}

	return discount
}
