package inmemory

import (
	"context"
	"sync"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugins/coupon"
)

// Compile-time interface check.
var _ coupon.CouponRepository = (*InMemoryCouponRepository)(nil)

// InMemoryCouponRepository is an in-memory implementation of
// coupon.CouponRepository, suitable for tests, demos, and as the reference for
// the SaveRedemption atomicity contract (issues #185, #195).
//
// The entire SaveRedemption sequence — (a) idempotency check, (b) usage-limit
// enforcement, (c) insert — runs under one mutex, so concurrent confirmations
// are serialized exactly as the interface contract requires: same-key calls
// collapse to a single redemption, and distinct-key calls can never exceed a
// coupon's global or per-account usage limit.
type InMemoryCouponRepository struct {
	mu      sync.RWMutex
	coupons map[coupon.CouponID]*coupon.Coupon
	// order preserves Save insertion order so FindApplicable returns coupons
	// deterministically (map iteration order is random, which would make the
	// plugin's "first valid wins" selection nondeterministic).
	order []coupon.CouponID
	// redemptions is keyed by Redemption.IdempotencyKey()
	// (couponID, contractID, billingPeriod) — the dedup key for issue #185.
	redemptions map[string]*coupon.Redemption
}

// NewInMemoryCouponRepository creates a new InMemoryCouponRepository.
func NewInMemoryCouponRepository() *InMemoryCouponRepository {
	return &InMemoryCouponRepository{
		coupons:     make(map[coupon.CouponID]*coupon.Coupon),
		redemptions: make(map[string]*coupon.Redemption),
	}
}

// Save persists a coupon.
func (r *InMemoryCouponRepository) Save(_ context.Context, c *coupon.Coupon) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.coupons[c.ID()]; !exists {
		r.order = append(r.order, c.ID())
	}
	r.coupons[c.ID()] = c
	return nil
}

// FindByCode retrieves a coupon by its code. Per the CouponRepository contract
// it returns (nil, nil) when no coupon has the code.
func (r *InMemoryCouponRepository) FindByCode(_ context.Context, code string) (*coupon.Coupon, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, id := range r.order {
		if c := r.coupons[id]; c != nil && c.Code() == code {
			return c, nil
		}
	}
	return nil, nil
}

// FindApplicable retrieves the coupons applicable to the query, mirroring the
// Coupon domain's own applicability checks:
//
//   - validity window (and usage-limit baseline) at query.At — Coupon.IsValid
//   - product applicability — Coupon.IsApplicableToProduct
//   - account allowlist/blocklist — Coupon.IsAccountAllowed
//
// Contract-type applicability cannot be filtered here (CouponQuery carries no
// contract type); the coupon plugin re-checks IsApplicableToContractType (and
// everything above, defensively) itself. Results are returned in Save insertion
// order so the plugin's "first valid wins" selection is deterministic.
func (r *InMemoryCouponRepository) FindApplicable(_ context.Context, query coupon.CouponQuery) ([]*coupon.Coupon, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*coupon.Coupon
	for _, id := range r.order {
		c := r.coupons[id]
		if c == nil {
			continue
		}
		if !c.IsValid(query.At) {
			continue
		}
		if !c.IsApplicableToProduct(query.ProductID) {
			continue
		}
		if !c.IsAccountAllowed(query.AccountID) {
			continue
		}
		result = append(result, c)
	}
	return result, nil
}

// SaveRedemption atomically confirms a coupon redemption, enforcing the
// coupon's usage limits as part of the same serialized operation (issues #185,
// #195). The whole (a)-(c) sequence below holds the write lock, which is the
// in-memory equivalent of the advisory-lock / serialized conditional insert a
// real database implementation uses:
//
//	(a) Idempotency: an existing redemption with the same IdempotencyKey()
//	    (couponID, contractID, billingPeriod) is a no-op — retries and
//	    RegenerateInvoice for the same period consume exactly one use.
//	(b) Limit check: otherwise, if inserting would exceed limits.GlobalLimit
//	    (GlobalBaseline + existing distinct rows) or limits.PerAccountLimit
//	    (existing distinct rows for the redemption's account), it returns
//	    coupon.ErrUsageLimitReached without inserting. Enforcing this here,
//	    atomically with the insert, closes the TOCTOU where two concurrent
//	    billing runs for different contracts both pass the plugin's advisory
//	    pre-check (#195).
//	(c) Insert: otherwise the redemption is persisted.
func (r *InMemoryCouponRepository) SaveRedemption(_ context.Context, redemption *coupon.Redemption, limits coupon.RedemptionLimits) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// (a) Idempotency: same key is a no-op, never counted as a new use.
	key := redemption.IdempotencyKey()
	if _, exists := r.redemptions[key]; exists {
		return nil
	}

	// (b) Atomic limit enforcement against existing distinct rows (#195).
	if limits.GlobalLimit != nil {
		if limits.GlobalBaseline+r.countRedemptionsLocked(redemption.CouponID(), nil) >= *limits.GlobalLimit {
			return coupon.ErrUsageLimitReached
		}
	}
	if limits.PerAccountLimit != nil {
		accountID := redemption.AccountID()
		if r.countRedemptionsLocked(redemption.CouponID(), &accountID) >= *limits.PerAccountLimit {
			return coupon.ErrUsageLimitReached
		}
	}

	// (c) Insert.
	r.redemptions[key] = redemption
	return nil
}

// FindRedemptions retrieves all redemptions for a coupon, optionally filtered
// by account. It is the source of truth the plugin reconciles usage counts
// from (issue #185).
func (r *InMemoryCouponRepository) FindRedemptions(_ context.Context, couponID coupon.CouponID, accountID *shared.AccountID) ([]*coupon.Redemption, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*coupon.Redemption
	for _, red := range r.redemptions {
		if red.CouponID() != couponID {
			continue
		}
		if accountID != nil && red.AccountID() != *accountID {
			continue
		}
		result = append(result, red)
	}
	return result, nil
}

// countRedemptionsLocked counts distinct redemption rows for a coupon,
// optionally filtered to a single account. Callers must hold r.mu.
func (r *InMemoryCouponRepository) countRedemptionsLocked(couponID coupon.CouponID, accountID *shared.AccountID) int {
	n := 0
	for _, red := range r.redemptions {
		if red.CouponID() != couponID {
			continue
		}
		if accountID != nil && red.AccountID() != *accountID {
			continue
		}
		n++
	}
	return n
}
