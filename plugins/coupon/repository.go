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

	// SaveRedemption idempotently confirms a coupon redemption.
	//
	// Idempotency contract: implementations MUST deduplicate on
	// (couponID, contractID, billingPeriod) — Redemption.IdempotencyKey().
	// A second call whose key matches an existing redemption MUST NOT create a
	// duplicate row and MUST NOT increase the coupon's usage count; it is a
	// no-op (implementations MAY refresh the stored invoiceID/redeemedAt to the
	// latest confirming invoice). This is what makes billing retries and
	// RegenerateInvoice for the same period consume exactly one use, and what
	// prevents a rolled-back invoice's redemption from permanently burning an
	// extra use (a subsequent retry reuses the same key).
	//
	// Concurrency: for two concurrent confirmations of the same key, a single
	// redemption must survive (e.g. a UNIQUE constraint on
	// (coupon_id, contract_id, period_start, period_end) with upsert /
	// insert-or-ignore semantics). Hardening the full check-then-insert race for
	// DISTINCT keys against a global usageLimit is tracked separately (#195).
	SaveRedemption(ctx context.Context, redemption *Redemption) error

	// FindRedemptions retrieves all redemptions for a coupon, optionally filtered
	// by account. It is the source of truth for usage reconciliation: the plugin
	// counts distinct redemptions (excluding the in-flight (contract, period)
	// being (re)confirmed) to enforce global and per-account usage limits in a
	// retry-safe way.
	FindRedemptions(ctx context.Context, couponID CouponID, accountID *shared.AccountID) ([]*Redemption, error)
}
