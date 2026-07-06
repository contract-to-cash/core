package coupon

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
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
	discount, _ := c.CalculateDiscount(subtotal)

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
	discount, _ := c.CalculateDiscount(subtotal)

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
	discount, _ := c.CalculateDiscount(subtotal)

	expected := big.NewRat(500, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount capped at 500, got %s", discount.Amount().RatString())
	}
}

func TestCoupon_IsApplicableToProduct(t *testing.T) {
	tests := []struct {
		name         string
		applicableTo []shared.ProductID
		productID    shared.ProductID
		want         bool
	}{
		{
			name:         "empty applicableTo matches all products",
			applicableTo: nil,
			productID:    "any-product",
			want:         true,
		},
		{
			name:         "matching product ID",
			applicableTo: []shared.ProductID{"product-gold", "product-silver"},
			productID:    "product-gold",
			want:         true,
		},
		{
			name:         "non-matching product ID",
			applicableTo: []shared.ProductID{"product-gold"},
			productID:    "product-silver",
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
			if got := c.IsApplicableToProduct(tt.productID); got != tt.want {
				t.Errorf("IsApplicableToProduct(%q) = %v, want %v", tt.productID, got, tt.want)
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

func TestCoupon_Getters(t *testing.T) {
	minAmount := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	maxDiscount := shared.NewMoney(big.NewRat(500, 1), shared.CurrencyJPY)
	usageLimit := 10
	value := big.NewRat(15, 100)
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	validUntil := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)

	c := NewCoupon(
		"c1", "CODE15", CouponTypePercentage,
		value, shared.CurrencyJPY,
		&minAmount, &maxDiscount,
		validFrom, validUntil,
		&usageLimit, 3, []shared.ProductID{"plan-a"},
	)

	t.Run("CouponType", func(t *testing.T) {
		if got := c.CouponType(); got != CouponTypePercentage {
			t.Errorf("CouponType() = %v, want %v", got, CouponTypePercentage)
		}
	})

	t.Run("Value returns correct value", func(t *testing.T) {
		got := c.Value()
		if got.Cmp(big.NewRat(15, 100)) != 0 {
			t.Errorf("Value() = %s, want 15/100", got.RatString())
		}
	})

	t.Run("Value returns defensive copy", func(t *testing.T) {
		got := c.Value()
		got.SetInt64(999) // mutate the returned value
		// Original should be unchanged
		if c.Value().Cmp(big.NewRat(15, 100)) != 0 {
			t.Errorf("Value() was mutated by caller, got %s", c.Value().RatString())
		}
	})

	t.Run("ValidFrom", func(t *testing.T) {
		if got := c.ValidFrom(); !got.Equal(validFrom) {
			t.Errorf("ValidFrom() = %v, want %v", got, validFrom)
		}
	})

	t.Run("ValidUntil", func(t *testing.T) {
		if got := c.ValidUntil(); !got.Equal(validUntil) {
			t.Errorf("ValidUntil() = %v, want %v", got, validUntil)
		}
	})

	t.Run("MinAmount", func(t *testing.T) {
		got := c.MinAmount()
		if got == nil {
			t.Fatal("MinAmount() = nil, want non-nil")
		}
		if got.Amount().Cmp(big.NewRat(1000, 1)) != 0 {
			t.Errorf("MinAmount().Amount() = %s, want 1000", got.Amount().RatString())
		}
	})

	t.Run("MaxDiscount", func(t *testing.T) {
		got := c.MaxDiscount()
		if got == nil {
			t.Fatal("MaxDiscount() = nil, want non-nil")
		}
		if got.Amount().Cmp(big.NewRat(500, 1)) != 0 {
			t.Errorf("MaxDiscount().Amount() = %s, want 500", got.Amount().RatString())
		}
	})

	t.Run("UsageLimit", func(t *testing.T) {
		got := c.UsageLimit()
		if got == nil {
			t.Fatal("UsageLimit() = nil, want non-nil")
		}
		if *got != 10 {
			t.Errorf("UsageLimit() = %d, want 10", *got)
		}
	})

	t.Run("UsedCount", func(t *testing.T) {
		if got := c.UsedCount(); got != 3 {
			t.Errorf("UsedCount() = %d, want 3", got)
		}
	})
}

func TestCoupon_Getters_NilOptionalFields(t *testing.T) {
	c := NewCoupon(
		"c2", "CODE20", CouponTypeFixed,
		big.NewRat(500, 1), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)

	t.Run("MinAmount nil", func(t *testing.T) {
		if got := c.MinAmount(); got != nil {
			t.Errorf("MinAmount() = %v, want nil", got)
		}
	})

	t.Run("MaxDiscount nil", func(t *testing.T) {
		if got := c.MaxDiscount(); got != nil {
			t.Errorf("MaxDiscount() = %v, want nil", got)
		}
	})

	t.Run("UsageLimit nil", func(t *testing.T) {
		if got := c.UsageLimit(); got != nil {
			t.Errorf("UsageLimit() = %v, want nil", got)
		}
	})

	t.Run("CouponType fixed", func(t *testing.T) {
		if got := c.CouponType(); got != CouponTypeFixed {
			t.Errorf("CouponType() = %v, want %v", got, CouponTypeFixed)
		}
	})
}

func TestCoupon_IsApplicableToContractType(t *testing.T) {
	tests := []struct {
		name          string
		contractTypes []contract.ContractType
		ct            contract.ContractType
		want          bool
	}{
		{
			name:          "empty types matches all",
			contractTypes: nil,
			ct:            contract.ContractTypeSubscription,
			want:          true,
		},
		{
			name:          "matching contract type",
			contractTypes: []contract.ContractType{contract.ContractTypeSubscription, contract.ContractTypeUsageBased},
			ct:            contract.ContractTypeSubscription,
			want:          true,
		},
		{
			name:          "non-matching contract type",
			contractTypes: []contract.ContractType{contract.ContractTypeSubscription},
			ct:            contract.ContractTypeOneTime,
			want:          false,
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
			if len(tt.contractTypes) > 0 {
				c.WithApplicableContractTypes(tt.contractTypes)
			}
			if got := c.IsApplicableToContractType(tt.ct); got != tt.want {
				t.Errorf("IsApplicableToContractType(%q) = %v, want %v", tt.ct, got, tt.want)
			}
		})
	}
}
