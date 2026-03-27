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

func TestCoupon_IsApplicableToPlan(t *testing.T) {
	tests := []struct {
		name         string
		applicableTo []string
		planID       shared.PlanID
		want         bool
	}{
		{
			name:         "empty applicableTo matches all plans",
			applicableTo: nil,
			planID:       "any-plan",
			want:         true,
		},
		{
			name:         "matching plan ID",
			applicableTo: []string{"plan-gold", "plan-silver"},
			planID:       "plan-gold",
			want:         true,
		},
		{
			name:         "non-matching plan ID",
			applicableTo: []string{"plan-gold"},
			planID:       "plan-silver",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCoupon(
				"c1", "CODE", CouponTypePercentage,
				big.NewRat(10, 100), shared.CurrencyJPY,
				nil, nil,
				time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
				nil, 0, tt.applicableTo,
			)
			if got := c.IsApplicableToPlan(tt.planID); got != tt.want {
				t.Errorf("IsApplicableToPlan(%q) = %v, want %v", tt.planID, got, tt.want)
			}
		})
	}
}

func TestCoupon_IsAccountAllowed(t *testing.T) {
	tests := []struct {
		name      string
		allowed   []shared.AccountID
		blocked   []shared.AccountID
		accountID shared.AccountID
		want      bool
	}{
		{
			name:      "no restrictions allows all",
			accountID: "acc-1",
			want:      true,
		},
		{
			name:      "allowlist permits listed account",
			allowed:   []shared.AccountID{"acc-1", "acc-2"},
			accountID: "acc-1",
			want:      true,
		},
		{
			name:      "allowlist rejects unlisted account",
			allowed:   []shared.AccountID{"acc-1"},
			accountID: "acc-99",
			want:      false,
		},
		{
			name:      "blocklist rejects listed account",
			blocked:   []shared.AccountID{"bad-acc"},
			accountID: "bad-acc",
			want:      false,
		},
		{
			name:      "blocklist allows unlisted account",
			blocked:   []shared.AccountID{"bad-acc"},
			accountID: "good-acc",
			want:      true,
		},
		{
			name:      "blocklist takes priority over allowlist",
			allowed:   []shared.AccountID{"acc-1"},
			blocked:   []shared.AccountID{"acc-1"},
			accountID: "acc-1",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCoupon(
				"c1", "CODE", CouponTypePercentage,
				big.NewRat(10, 100), shared.CurrencyJPY,
				nil, nil,
				time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
				nil, 0, nil,
			)
			if len(tt.allowed) > 0 {
				c.WithAllowedAccountIDs(tt.allowed)
			}
			if len(tt.blocked) > 0 {
				c.WithBlockedAccountIDs(tt.blocked)
			}
			if got := c.IsAccountAllowed(tt.accountID); got != tt.want {
				t.Errorf("IsAccountAllowed(%q) = %v, want %v", tt.accountID, got, tt.want)
			}
		})
	}
}

func TestCoupon_CodeType(t *testing.T) {
	c := NewCoupon(
		"c1", "PROMO", CouponTypePercentage,
		big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)

	// Default is shared
	if c.CodeType() != CodeTypeShared {
		t.Errorf("expected default code type to be shared, got %s", c.CodeType())
	}

	c.WithCodeType(CodeTypeUnique)
	if c.CodeType() != CodeTypeUnique {
		t.Errorf("expected code type to be unique, got %s", c.CodeType())
	}
}
