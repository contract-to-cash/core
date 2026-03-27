package coupon

import (
	"math/big"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
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

// CodeType distinguishes unique one-time codes from shared promotional codes.
type CodeType string

const (
	// CodeTypeShared is a promotional code that can be used by multiple accounts.
	CodeTypeShared CodeType = "shared"
	// CodeTypeUnique is a one-time code assigned to a specific redemption.
	CodeTypeUnique CodeType = "unique"
)

// RedemptionID uniquely identifies a coupon redemption.
type RedemptionID string

// Redemption tracks the usage of a specific coupon code by an account.
type Redemption struct {
	id         RedemptionID
	couponID   CouponID
	code       string
	codeType   CodeType
	accountID  shared.AccountID
	contractID shared.ContractID
	redeemedAt time.Time
}

// NewRedemption creates a new Redemption.
func NewRedemption(
	id RedemptionID,
	couponID CouponID,
	code string,
	codeType CodeType,
	accountID shared.AccountID,
	contractID shared.ContractID,
	redeemedAt time.Time,
) *Redemption {
	return &Redemption{
		id:         id,
		couponID:   couponID,
		code:       code,
		codeType:   codeType,
		accountID:  accountID,
		contractID: contractID,
		redeemedAt: redeemedAt,
	}
}

// ID returns the redemption ID.
func (r *Redemption) ID() RedemptionID { return r.id }

// CouponID returns the coupon ID.
func (r *Redemption) CouponID() CouponID { return r.couponID }

// Code returns the redeemed code.
func (r *Redemption) Code() string { return r.code }

// CodeType returns whether the redeemed code was shared or unique.
func (r *Redemption) CodeType() CodeType { return r.codeType }

// AccountID returns the account that redeemed the coupon.
func (r *Redemption) AccountID() shared.AccountID { return r.accountID }

// ContractID returns the contract the coupon was applied to.
func (r *Redemption) ContractID() shared.ContractID { return r.contractID }

// RedeemedAt returns the time the coupon was redeemed.
func (r *Redemption) RedeemedAt() time.Time { return r.redeemedAt }

// Coupon represents a discount coupon that can be applied to an invoice.
type Coupon struct {
	id                      CouponID
	code                    string
	codeType                CodeType
	couponType              CouponType
	value                   *big.Rat        // percentage: discount rate (e.g. 10/100), fixed: discount amount
	currency                shared.Currency // used only for fixed type
	minAmount               *shared.Money   // minimum purchase amount (nil = no minimum)
	maxDiscount             *shared.Money   // maximum discount amount (nil = no cap)
	validFrom               time.Time
	validUntil              time.Time
	usageLimit              *int
	usedCount               int
	perAccountUsageLimit    *int                    // max uses per account (nil = unlimited)
	applicableTo            []string                // applicable plan IDs (empty = all plans)
	applicableContractTypes []contract.ContractType // applicable contract types (empty = all types)
	allowedAccountIDs       []shared.AccountID
	blockedAccountIDs       []shared.AccountID
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
		codeType:     CodeTypeShared,
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

// WithCodeType sets the code type and returns the coupon for chaining.
func (c *Coupon) WithCodeType(ct CodeType) *Coupon {
	c.codeType = ct
	return c
}

// WithPerAccountUsageLimit sets the per-account usage limit and returns the coupon for chaining.
func (c *Coupon) WithPerAccountUsageLimit(limit int) *Coupon {
	c.perAccountUsageLimit = &limit
	return c
}

// WithAllowedAccountIDs sets the account allowlist and returns the coupon for chaining.
func (c *Coupon) WithAllowedAccountIDs(ids []shared.AccountID) *Coupon {
	c.allowedAccountIDs = ids
	return c
}

// WithBlockedAccountIDs sets the account blocklist and returns the coupon for chaining.
func (c *Coupon) WithBlockedAccountIDs(ids []shared.AccountID) *Coupon {
	c.blockedAccountIDs = ids
	return c
}

// WithApplicableContractTypes sets the applicable contract types and returns the coupon for chaining.
func (c *Coupon) WithApplicableContractTypes(types []contract.ContractType) *Coupon {
	c.applicableContractTypes = types
	return c
}

// ID returns the coupon ID.
func (c *Coupon) ID() CouponID { return c.id }

// Code returns the coupon code.
func (c *Coupon) Code() string { return c.code }

// CodeType returns the code type (shared or unique).
func (c *Coupon) CodeType() CodeType { return c.codeType }

// ApplicableTo returns the applicable plan IDs.
func (c *Coupon) ApplicableTo() []string { return c.applicableTo }

// PerAccountUsageLimit returns the per-account usage limit, or nil if unlimited.
func (c *Coupon) PerAccountUsageLimit() *int { return c.perAccountUsageLimit }

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

// IsApplicableToPlan returns true if the coupon is applicable to the given plan.
// If applicableTo is empty, the coupon applies to all plans.
//
// TODO(#7): Once Product/Price separation is implemented, this should check against
// ProductID instead of PlanID. See #4 comment: "applicableTo should filter by ProductID".
func (c *Coupon) IsApplicableToPlan(planID shared.PlanID) bool {
	if len(c.applicableTo) == 0 {
		return true
	}
	for _, id := range c.applicableTo {
		if id == string(planID) {
			return true
		}
	}
	return false
}

// IsApplicableToContractType returns true if the coupon is applicable to the given contract type.
// If applicableContractTypes is empty, the coupon applies to all contract types.
func (c *Coupon) IsApplicableToContractType(ct contract.ContractType) bool {
	if len(c.applicableContractTypes) == 0 {
		return true
	}
	for _, t := range c.applicableContractTypes {
		if t == ct {
			return true
		}
	}
	return false
}

// IsAccountAllowed returns true if the account is allowed to use this coupon.
// Rules:
//   - If blockedAccountIDs is set and the account is in it, return false.
//   - If allowedAccountIDs is set and the account is NOT in it, return false.
//   - Otherwise, return true.
func (c *Coupon) IsAccountAllowed(accountID shared.AccountID) bool {
	for _, id := range c.blockedAccountIDs {
		if id == accountID {
			return false
		}
	}
	if len(c.allowedAccountIDs) > 0 {
		for _, id := range c.allowedAccountIDs {
			if id == accountID {
				return true
			}
		}
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
