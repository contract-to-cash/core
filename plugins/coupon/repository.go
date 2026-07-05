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
	ProductID  shared.ProductID
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
	//
	// IDEMPOTENCY: RecordUsage MUST be idempotent on (couponID, contractID). The
	// core calls it from CommitDiscounts inside the billing transaction, and an
	// optimistic-lock retry or idempotent replay of that transaction can invoke it
	// again for the same (couponID, contractID). A second call for a pair that has
	// already been recorded must be a no-op and MUST NOT increment the global usage
	// counter again — otherwise a single redemption inflates the counter and can
	// exhaust a usage-limited coupon on retry.
	RecordUsage(ctx context.Context, couponID CouponID, contractID shared.ContractID) error
	// FindUsageByAccount returns the number of times a coupon has been used by a specific account.
	FindUsageByAccount(ctx context.Context, couponID CouponID, accountID shared.AccountID) (int, error)
	// SaveRedemption persists a coupon redemption record.
	//
	// IDEMPOTENCY: SaveRedemption MUST be idempotent on
	// (Redemption.CouponID, Redemption.ContractID). Because CommitDiscounts may run
	// more than once for the same logical redemption (transaction retry / replay),
	// a call whose (couponID, contractID) already has a redemption must be a no-op
	// (do not append a duplicate). The RedemptionID differs on each call and MUST
	// NOT be used as the dedup key.
	SaveRedemption(ctx context.Context, redemption *Redemption) error
	// FindRedemptions retrieves all redemptions for a coupon, optionally filtered by account.
	FindRedemptions(ctx context.Context, couponID CouponID, accountID *shared.AccountID) ([]*Redemption, error)
}
