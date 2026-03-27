package coupon

import (
	"context"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// CouponQuery holds query parameters for finding applicable coupons.
type CouponQuery struct {
	ContractID shared.ContractID
	AccountID  shared.AccountID
	PlanID     shared.PlanID
	At         time.Time
}

// CouponRepository defines the persistence interface for coupons.
type CouponRepository interface {
	// FindByCode retrieves a coupon by its code.
	FindByCode(ctx context.Context, code string) (*Coupon, error)
	// FindApplicable retrieves all applicable coupons for a contract at a given time.
	FindApplicable(ctx context.Context, query CouponQuery) ([]*Coupon, error)
	// Save persists a coupon.
	Save(ctx context.Context, coupon *Coupon) error
	// RecordUsage records that a coupon has been used for a contract.
	RecordUsage(ctx context.Context, couponID CouponID, contractID shared.ContractID) error
	// FindUsageByAccount returns the number of times a coupon has been used by a specific account.
	FindUsageByAccount(ctx context.Context, couponID CouponID, accountID shared.AccountID) (int, error)
	// SaveRedemption persists a coupon redemption record.
	SaveRedemption(ctx context.Context, redemption *Redemption) error
	// FindRedemptions retrieves all redemptions for a coupon, optionally filtered by account.
	FindRedemptions(ctx context.Context, couponID CouponID, accountID *shared.AccountID) ([]*Redemption, error)
}
