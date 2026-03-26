package coupon

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestCoupon_IsValid_WithinPeriod(t *testing.T) {
	c := NewCoupon(
		"c1", "CODE10", CouponTypePercentage,
		big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)

	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if !c.IsValid(at) {
		t.Error("expected coupon to be valid within period")
	}
}

func TestCoupon_IsValid_OutsidePeriod(t *testing.T) {
	c := NewCoupon(
		"c1", "CODE10", CouponTypePercentage,
		big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)

	// Before validFrom
	before := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	if c.IsValid(before) {
		t.Error("expected coupon to be invalid before validFrom")
	}

	// After validUntil
	after := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if c.IsValid(after) {
		t.Error("expected coupon to be invalid after validUntil")
	}
}

func TestCoupon_IsValid_UsageLimitReached(t *testing.T) {
	limit := 5
	c := NewCoupon(
		"c1", "CODE10", CouponTypePercentage,
		big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		&limit, 5, nil,
	)

	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if c.IsValid(at) {
		t.Error("expected coupon to be invalid when usage limit reached")
	}
}

func TestCoupon_CalculateDiscount_Percentage(t *testing.T) {
	c := NewCoupon(
		"c1", "CODE10", CouponTypePercentage,
		big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	discount := c.CalculateDiscount(subtotal)

	expected := big.NewRat(1000, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 1000, got %s", discount.Amount().RatString())
	}
}

func TestCoupon_CalculateDiscount_Fixed(t *testing.T) {
	c := NewCoupon(
		"c1", "FIX500", CouponTypeFixed,
		big.NewRat(500, 1), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	discount := c.CalculateDiscount(subtotal)

	expected := big.NewRat(500, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 500, got %s", discount.Amount().RatString())
	}
}

func TestCoupon_CalculateDiscount_MaxDiscountCap(t *testing.T) {
	maxDiscount := shared.NewMoney(big.NewRat(500, 1), shared.CurrencyJPY)
	c := NewCoupon(
		"c1", "CODE50", CouponTypePercentage,
		big.NewRat(50, 100), shared.CurrencyJPY,
		nil, &maxDiscount,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)

	// 50% of 10000 = 5000, but maxDiscount caps at 500
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	discount := c.CalculateDiscount(subtotal)

	expected := big.NewRat(500, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount capped at 500, got %s", discount.Amount().RatString())
	}
}
