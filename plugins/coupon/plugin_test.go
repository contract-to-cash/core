package coupon

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// mockCouponRepository is a test double for CouponRepository.
type mockCouponRepository struct {
	coupons []*Coupon
}

func (m *mockCouponRepository) FindByCode(_ context.Context, code string) (*Coupon, error) {
	for _, c := range m.coupons {
		if c.Code() == code {
			return c, nil
		}
	}
	return nil, nil
}

func (m *mockCouponRepository) FindApplicable(_ context.Context, _ shared.ContractID, _ time.Time) ([]*Coupon, error) {
	return m.coupons, nil
}

func (m *mockCouponRepository) Save(_ context.Context, _ *Coupon) error {
	return nil
}

func (m *mockCouponRepository) RecordUsage(_ context.Context, _ CouponID, _ shared.ContractID) error {
	return nil
}

func newTestContext(subtotal shared.Money) *plugin.CalculationContext {
	return plugin.NewCalculationContext(context.Background(), nil, subtotal)
}

func TestCouponPlugin_PercentageDiscount(t *testing.T) {
	coupon := NewCoupon(
		"c1", "SAVE10", CouponTypePercentage,
		big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)

	repo := &mockCouponRepository{coupons: []*Coupon{coupon}}
	clock := shared.FixedClock{FixedTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	p := NewCouponPlugin(repo, clock)

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := big.NewRat(1000, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 1000, got %s", discount.Amount().RatString())
	}
}

func TestCouponPlugin_FixedDiscount(t *testing.T) {
	coupon := NewCoupon(
		"c1", "FIX500", CouponTypeFixed,
		big.NewRat(500, 1), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)

	repo := &mockCouponRepository{coupons: []*Coupon{coupon}}
	clock := shared.FixedClock{FixedTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	p := NewCouponPlugin(repo, clock)

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := big.NewRat(500, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 500, got %s", discount.Amount().RatString())
	}
}

func TestCouponPlugin_MaxDiscount(t *testing.T) {
	maxDiscount := shared.NewMoney(big.NewRat(300, 1), shared.CurrencyJPY)
	coupon := NewCoupon(
		"c1", "BIG50", CouponTypePercentage,
		big.NewRat(50, 100), shared.CurrencyJPY,
		nil, &maxDiscount,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)

	repo := &mockCouponRepository{coupons: []*Coupon{coupon}}
	clock := shared.FixedClock{FixedTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	p := NewCouponPlugin(repo, clock)

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 50% of 10000 = 5000, but capped at 300
	expected := big.NewRat(300, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount capped at 300, got %s", discount.Amount().RatString())
	}
}

func TestCouponPlugin_NoStackingReturnsFirst(t *testing.T) {
	coupon1 := NewCoupon(
		"c1", "FIRST10", CouponTypePercentage,
		big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)
	coupon2 := NewCoupon(
		"c2", "SECOND20", CouponTypePercentage,
		big.NewRat(20, 100), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)

	repo := &mockCouponRepository{coupons: []*Coupon{coupon1, coupon2}}
	clock := shared.FixedClock{FixedTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	p := NewCouponPlugin(repo, clock)
	// AllowStacking defaults to false

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only first coupon applied: 10% of 10000 = 1000
	expected := big.NewRat(1000, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 1000 (first coupon only), got %s", discount.Amount().RatString())
	}

	discounts := ctx.AppliedDiscounts()
	if len(discounts) != 1 {
		t.Errorf("expected 1 recorded discount, got %d", len(discounts))
	}
}

func TestCouponPlugin_NoApplicable(t *testing.T) {
	repo := &mockCouponRepository{coupons: nil}
	clock := shared.FixedClock{FixedTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	p := NewCouponPlugin(repo, clock)

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !discount.IsZero() {
		t.Errorf("expected zero discount, got %s", discount.Amount().RatString())
	}
}
