package coupon

import (
	"context"
	"errors"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// ErrUsageLimitReached is the sentinel error SaveRedemption returns when
// confirming a redemption would exceed a coupon's global or per-account usage
// limit (issue #195). It is the atomic, authoritative gate: CalculateDiscount's
// usage-limit pre-checks are advisory (a best-effort read that two concurrent
// billing runs for DIFFERENT contracts can both pass), so the last line of
// defence against over-redemption is SaveRedemption rejecting the losing
// confirmation with this error. The coupon plugin's AfterCalculation returns a
// descriptive error wrapping this sentinel, which rolls back the billing
// transaction; a retry then recalculates without the now-exhausted coupon.
//
// Repository implementations MUST return this (directly or wrapped such that
// errors.Is(err, ErrUsageLimitReached) is true) rather than a bespoke error, so
// the plugin can distinguish "limit reached" (expected under contention) from a
// genuine persistence failure.
var ErrUsageLimitReached = errors.New("coupon: usage limit reached")

// RedemptionLimits carries the usage limits SaveRedemption must enforce
// atomically together with the insert (issue #195). The plugin populates it from
// the coupon being confirmed; a nil limit means "unlimited" for that dimension.
type RedemptionLimits struct {
	// GlobalLimit is the coupon's global usage limit (Coupon.UsageLimit()); nil
	// means unlimited. When non-nil, SaveRedemption must reject an insert that
	// would make GlobalBaseline + <distinct redemptions of this coupon> exceed it.
	GlobalLimit *int
	// GlobalBaseline is historical usage counted toward GlobalLimit that predates
	// the redemption ledger (Coupon.UsedCount(), e.g. a migration baseline). It is
	// added to the counted redemption rows when enforcing GlobalLimit.
	GlobalBaseline int
	// PerAccountLimit is the coupon's per-account usage limit
	// (Coupon.PerAccountUsageLimit()); nil means unlimited. When non-nil,
	// SaveRedemption must reject an insert that would make the redemption's
	// account exceed it. There is no per-account baseline.
	PerAccountLimit *int
}

// CouponQuery holds query parameters for finding applicable coupons.
type CouponQuery struct {
	ContractID shared.ContractID
	AccountID  shared.AccountID
	ProductID  shared.ProductID
	At         time.Time
}

// CouponRepository defines the persistence interface for coupons.
//
// Usage accounting (issue #185): coupon usage is reconciled from redemption rows
// — there is NO separate usage counter to keep in sync. A redemption is the
// single source of truth for "this coupon was consumed for this (contract,
// billing period)". This removes the double-counting / burn-on-rollback class of
// bug that a side-channel counter incremented from a calculation hook produced.
type CouponRepository interface {
	// FindByCode retrieves a coupon by its code. Returns (nil, nil) when no
	// coupon has the code.
	FindByCode(ctx context.Context, code string) (*Coupon, error)
	// FindApplicable retrieves all applicable coupons for a contract at a given time.
	FindApplicable(ctx context.Context, query CouponQuery) ([]*Coupon, error)
	// Save persists a coupon.
	Save(ctx context.Context, coupon *Coupon) error

	// SaveRedemption atomically confirms a coupon redemption, enforcing the
	// coupon's usage limits as part of the same operation (issues #185, #195).
	//
	// Implementations MUST perform the following as ONE atomic, serialized step
	// (against concurrent SaveRedemption calls for the same coupon):
	//
	//   (a) Idempotency — if a redemption with the same
	//       Redemption.IdempotencyKey() (couponID, contractID, billingPeriod)
	//       already exists, return nil WITHOUT inserting a duplicate and WITHOUT
	//       counting it as a new use. This is what makes billing retries and
	//       RegenerateInvoice for the same period consume exactly one use, and
	//       prevents a rolled-back invoice's redemption from burning an extra use
	//       (a subsequent retry reuses the same key). Implementations MAY refresh
	//       the stored invoiceID/redeemedAt to the latest confirming invoice.
	//
	//   (b) Limit check — otherwise, if inserting this redemption would exceed a
	//       limit in `limits`, return ErrUsageLimitReached (directly or wrapped so
	//       errors.Is(err, ErrUsageLimitReached) holds) and DO NOT insert. Reject
	//       when, for GlobalLimit != nil,
	//         GlobalBaseline + <distinct redemptions of this coupon> >= *GlobalLimit,
	//       or, for PerAccountLimit != nil,
	//         <distinct redemptions of this coupon for redemption.AccountID()> >= *PerAccountLimit.
	//
	//   (c) Insert — otherwise persist the redemption.
	//
	// Enforcing the count-then-insert atomically here (rather than trusting the
	// plugin's pre-check) is what closes the TOCTOU where two concurrent billing
	// runs for DIFFERENT contracts both pass the read-side check and over-redeem a
	// limited coupon (#195). A real database implements this with a UNIQUE index
	// on the idempotency key plus a serialized conditional insert — an advisory
	// lock keyed by coupon ID, a SERIALIZABLE transaction, or
	// INSERT ... ON CONFLICT DO NOTHING followed by a counted re-check under the
	// same lock. An in-memory implementation uses a mutex around the whole (a)-(c)
	// sequence.
	SaveRedemption(ctx context.Context, redemption *Redemption, limits RedemptionLimits) error

	// FindRedemptions retrieves all redemptions for a coupon, optionally filtered
	// by account. It is the source of truth for usage reconciliation: the plugin
	// counts distinct redemptions (excluding the in-flight (contract, period)
	// being (re)confirmed) to enforce global and per-account usage limits in a
	// retry-safe way.
	FindRedemptions(ctx context.Context, couponID CouponID, accountID *shared.AccountID) ([]*Redemption, error)
}
