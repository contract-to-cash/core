package coupon

import (
	"context"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// CouponRepository defines the persistence interface for coupons.
type CouponRepository interface {
	// FindByCode retrieves a coupon by its code.
	FindByCode(ctx context.Context, code string) (*Coupon, error)
	// FindApplicable retrieves all applicable coupons for a contract at a given time.
	FindApplicable(ctx context.Context, contractID shared.ContractID, at time.Time) ([]*Coupon, error)
	// Save persists a coupon.
	Save(ctx context.Context, coupon *Coupon) error
	// RecordUsage records that a coupon has been used for a contract.
	RecordUsage(ctx context.Context, couponID CouponID, contractID shared.ContractID) error
}
