package coupon

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

var (
	guardValidFrom  = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	guardValidUntil = time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
)

func guardJPY(n int64) shared.Money { return shared.NewMoney(big.NewRat(n, 1), shared.CurrencyJPY) }
func guardUSD(n int64) shared.Money { return shared.NewMoney(big.NewRat(n, 1), shared.CurrencyUSD) }

// Issue #148: a maxDiscount cap configured in a currency other than the
// discount's can no longer be silently dropped (which yielded an uncapped
// discount) — CalculateDiscount surfaces it as an error.
func TestCoupon_CalculateDiscount_ForeignMaxDiscountErrors(t *testing.T) {
	maxUSD := guardUSD(100)
	c := NewCoupon(
		"c-max", "MAX", CouponTypePercentage, big.NewRat(50, 100), shared.CurrencyJPY,
		nil, &maxUSD, guardValidFrom, guardValidUntil, nil, 0, nil,
	)
	_, err := c.CalculateDiscount(guardJPY(10000))
	if err == nil {
		t.Fatal("expected currency mismatch error, got nil")
	}
	var de *shared.DomainError
	if !errors.As(err, &de) || de.Code != shared.ErrCodeCurrencyMismatch {
		t.Fatalf("expected currency_mismatch, got %v", err)
	}
}

func TestCoupon_CalculateDiscount_MatchingMaxDiscountCaps(t *testing.T) {
	maxJPY := guardJPY(1000)
	c := NewCoupon(
		"c-max", "MAX", CouponTypePercentage, big.NewRat(50, 100), shared.CurrencyJPY,
		nil, &maxJPY, guardValidFrom, guardValidUntil, nil, 0, nil,
	)
	// 50% of 10000 = 5000, capped at 1000.
	discount, err := c.CalculateDiscount(guardJPY(10000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if discount.Amount().Cmp(big.NewRat(1000, 1)) != 0 {
		t.Errorf("expected capped discount 1000, got %s", discount.Amount().RatString())
	}
}

// The foreign-maxDiscount error must propagate out of the plugin's
// CalculateDiscount rather than being swallowed (issue #148).
func TestCouponPlugin_ForeignMaxDiscountPropagatesError(t *testing.T) {
	maxUSD := guardUSD(100)
	c := NewCoupon(
		"c-max", "MAX", CouponTypePercentage, big.NewRat(50, 100), shared.CurrencyJPY,
		nil, &maxUSD, guardValidFrom, guardValidUntil, nil, 0, nil,
	)
	repo := newMockRepo(c)
	p := NewCouponPlugin(repo, testClock)

	ctx := newTestContext(guardJPY(10000))
	if _, err := p.CalculateDiscount(ctx); err == nil {
		t.Fatal("expected error to propagate from foreign maxDiscount, got nil")
	}
	if repo.recordUsageCalled != 0 {
		t.Errorf("expected no usage recorded on error, got %d", repo.recordUsageCalled)
	}
}

// Issue #148: a minAmount configured in a foreign currency was previously
// compared as a raw big.Rat across currencies. It is now skipped defensively,
// consistent with the plugin's foreign-currency fixed-discount handling.
func TestCouponPlugin_SkipsCouponWithForeignMinAmount(t *testing.T) {
	minUSD := guardUSD(1)
	c := NewCoupon(
		"c-min", "MIN", CouponTypePercentage, big.NewRat(10, 100), shared.CurrencyJPY,
		&minUSD, nil, guardValidFrom, guardValidUntil, nil, 0, nil,
	)
	repo := newMockRepo(c)
	p := NewCouponPlugin(repo, testClock)

	ctx := newTestContext(guardJPY(10000))
	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !discount.IsZero() {
		t.Errorf("expected zero discount (coupon skipped), got %s", discount.Amount().RatString())
	}
	if repo.recordUsageCalled != 0 {
		t.Errorf("expected no usage recorded for skipped coupon, got %d", repo.recordUsageCalled)
	}
}

// A matching-currency minAmount still gates correctly: below the minimum skips,
// at/above applies.
func TestCouponPlugin_MatchingMinAmountGates(t *testing.T) {
	minJPY := guardJPY(20000)
	c := NewCoupon(
		"c-min", "MIN", CouponTypePercentage, big.NewRat(10, 100), shared.CurrencyJPY,
		&minJPY, nil, guardValidFrom, guardValidUntil, nil, 0, nil,
	)
	repo := newMockRepo(c)
	p := NewCouponPlugin(repo, testClock)

	// Subtotal 10000 < minAmount 20000 → skipped.
	discount, err := p.CalculateDiscount(newTestContext(guardJPY(10000)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !discount.IsZero() {
		t.Errorf("expected zero discount below minimum, got %s", discount.Amount().RatString())
	}
}
