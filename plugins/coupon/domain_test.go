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

// TestCoupon_Getters_SliceAndCurrencyFields covers the getters added for issue
// #221 (Currency / ApplicableContractTypes / AllowedAccountIDs /
// BlockedAccountIDs) plus ApplicableTo, verifying both the returned values and
// that slice getters return defensive copies.
func TestCoupon_Getters_SliceAndCurrencyFields(t *testing.T) {
	products := []shared.ProductID{"prod-a", "prod-b"}
	contractTypes := []contract.ContractType{contract.ContractTypeSubscription, contract.ContractTypeUsageBased}
	allowed := []shared.AccountID{"acct-1", "acct-2"}
	blocked := []shared.AccountID{"acct-3"}

	c := NewCoupon(
		"c1", "FIX500", CouponTypeFixed,
		big.NewRat(500, 1), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, products,
	).
		WithApplicableContractTypes(contractTypes).
		WithAllowedAccountIDs(allowed).
		WithBlockedAccountIDs(blocked)

	t.Run("Currency", func(t *testing.T) {
		if got := c.Currency(); got != shared.CurrencyJPY {
			t.Errorf("Currency() = %v, want %v", got, shared.CurrencyJPY)
		}
	})

	t.Run("ApplicableTo returns values", func(t *testing.T) {
		got := c.ApplicableTo()
		if len(got) != 2 || got[0] != "prod-a" || got[1] != "prod-b" {
			t.Errorf("ApplicableTo() = %v, want %v", got, products)
		}
	})

	t.Run("ApplicableContractTypes returns values", func(t *testing.T) {
		got := c.ApplicableContractTypes()
		if len(got) != 2 || got[0] != contract.ContractTypeSubscription || got[1] != contract.ContractTypeUsageBased {
			t.Errorf("ApplicableContractTypes() = %v, want %v", got, contractTypes)
		}
	})

	t.Run("AllowedAccountIDs returns values", func(t *testing.T) {
		got := c.AllowedAccountIDs()
		if len(got) != 2 || got[0] != "acct-1" || got[1] != "acct-2" {
			t.Errorf("AllowedAccountIDs() = %v, want %v", got, allowed)
		}
	})

	t.Run("BlockedAccountIDs returns values", func(t *testing.T) {
		got := c.BlockedAccountIDs()
		if len(got) != 1 || got[0] != "acct-3" {
			t.Errorf("BlockedAccountIDs() = %v, want %v", got, blocked)
		}
	})

	t.Run("ApplicableTo returns defensive copy", func(t *testing.T) {
		got := c.ApplicableTo()
		got[0] = "mutated"
		if c.ApplicableTo()[0] != "prod-a" {
			t.Error("mutating ApplicableTo() result changed internal state")
		}
		if !c.IsApplicableToProduct("prod-a") {
			t.Error("internal applicableTo was mutated: prod-a no longer applicable")
		}
	})

	t.Run("ApplicableContractTypes returns defensive copy", func(t *testing.T) {
		got := c.ApplicableContractTypes()
		got[0] = contract.ContractTypeOneTime
		if c.ApplicableContractTypes()[0] != contract.ContractTypeSubscription {
			t.Error("mutating ApplicableContractTypes() result changed internal state")
		}
		if !c.IsApplicableToContractType(contract.ContractTypeSubscription) {
			t.Error("internal applicableContractTypes was mutated: subscription no longer applicable")
		}
	})

	t.Run("AllowedAccountIDs returns defensive copy", func(t *testing.T) {
		got := c.AllowedAccountIDs()
		got[0] = "mutated"
		if c.AllowedAccountIDs()[0] != "acct-1" {
			t.Error("mutating AllowedAccountIDs() result changed internal state")
		}
		if !c.IsAccountAllowed("acct-1") {
			t.Error("internal allowedAccountIDs was mutated: acct-1 no longer allowed")
		}
	})

	t.Run("BlockedAccountIDs returns defensive copy", func(t *testing.T) {
		got := c.BlockedAccountIDs()
		got[0] = "mutated"
		if c.BlockedAccountIDs()[0] != "acct-3" {
			t.Error("mutating BlockedAccountIDs() result changed internal state")
		}
		if c.IsAccountAllowed("acct-3") {
			t.Error("internal blockedAccountIDs was mutated: acct-3 no longer blocked")
		}
	})

	t.Run("empty slices stay empty", func(t *testing.T) {
		plain := NewCoupon(
			"c2", "PLAIN", CouponTypePercentage,
			big.NewRat(10, 100), shared.CurrencyJPY,
			nil, nil,
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
			nil, 0, nil,
		)
		if got := plain.ApplicableTo(); len(got) != 0 {
			t.Errorf("ApplicableTo() = %v, want empty", got)
		}
		if got := plain.ApplicableContractTypes(); len(got) != 0 {
			t.Errorf("ApplicableContractTypes() = %v, want empty", got)
		}
		if got := plain.AllowedAccountIDs(); len(got) != 0 {
			t.Errorf("AllowedAccountIDs() = %v, want empty", got)
		}
		if got := plain.BlockedAccountIDs(); len(got) != 0 {
			t.Errorf("BlockedAccountIDs() = %v, want empty", got)
		}
	})
}

// TestCoupon_GetterRoundTrip verifies the motivation of issue #221: a
// repository implementation can rebuild an equivalent Coupon from getters
// alone (NewCoupon + With* builders), without any side table.
func TestCoupon_GetterRoundTrip(t *testing.T) {
	minAmount := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	maxDiscount := shared.NewMoney(big.NewRat(300, 1), shared.CurrencyJPY)
	usageLimit := 100

	original := NewCoupon(
		"c1", "FIX500", CouponTypeFixed,
		big.NewRat(500, 1), shared.CurrencyJPY,
		&minAmount, &maxDiscount,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		&usageLimit, 7, []shared.ProductID{"prod-a"},
	).
		WithCodeType(CodeTypeUnique).
		WithPerAccountUsageLimit(2).
		WithApplicableContractTypes([]contract.ContractType{contract.ContractTypeSubscription}).
		WithAllowedAccountIDs([]shared.AccountID{"acct-1"}).
		WithBlockedAccountIDs([]shared.AccountID{"acct-9"})

	// Rebuild exclusively from public getters (what Save/FindByCode round-trip
	// through a repository does).
	rebuilt := NewCoupon(
		original.ID(), original.Code(), original.CouponType(),
		original.Value(), original.Currency(),
		original.MinAmount(), original.MaxDiscount(),
		original.ValidFrom(), original.ValidUntil(),
		original.UsageLimit(), original.UsedCount(),
		original.ApplicableTo(),
	).
		WithCodeType(original.CodeType()).
		WithApplicableContractTypes(original.ApplicableContractTypes()).
		WithAllowedAccountIDs(original.AllowedAccountIDs()).
		WithBlockedAccountIDs(original.BlockedAccountIDs())
	if l := original.PerAccountUsageLimit(); l != nil {
		rebuilt.WithPerAccountUsageLimit(*l)
	}

	if rebuilt.ID() != original.ID() {
		t.Errorf("ID: got %v, want %v", rebuilt.ID(), original.ID())
	}
	if rebuilt.Code() != original.Code() {
		t.Errorf("Code: got %v, want %v", rebuilt.Code(), original.Code())
	}
	if rebuilt.CodeType() != original.CodeType() {
		t.Errorf("CodeType: got %v, want %v", rebuilt.CodeType(), original.CodeType())
	}
	if rebuilt.CouponType() != original.CouponType() {
		t.Errorf("CouponType: got %v, want %v", rebuilt.CouponType(), original.CouponType())
	}
	if rebuilt.Value().Cmp(original.Value()) != 0 {
		t.Errorf("Value: got %s, want %s", rebuilt.Value().RatString(), original.Value().RatString())
	}
	if rebuilt.Currency() != original.Currency() {
		t.Errorf("Currency: got %v, want %v", rebuilt.Currency(), original.Currency())
	}
	if rebuilt.MinAmount() == nil || rebuilt.MinAmount().Amount().Cmp(original.MinAmount().Amount()) != 0 ||
		rebuilt.MinAmount().Currency() != original.MinAmount().Currency() {
		t.Errorf("MinAmount: got %v, want %v", rebuilt.MinAmount(), original.MinAmount())
	}
	if rebuilt.MaxDiscount() == nil || rebuilt.MaxDiscount().Amount().Cmp(original.MaxDiscount().Amount()) != 0 ||
		rebuilt.MaxDiscount().Currency() != original.MaxDiscount().Currency() {
		t.Errorf("MaxDiscount: got %v, want %v", rebuilt.MaxDiscount(), original.MaxDiscount())
	}
	if !rebuilt.ValidFrom().Equal(original.ValidFrom()) {
		t.Errorf("ValidFrom: got %v, want %v", rebuilt.ValidFrom(), original.ValidFrom())
	}
	if !rebuilt.ValidUntil().Equal(original.ValidUntil()) {
		t.Errorf("ValidUntil: got %v, want %v", rebuilt.ValidUntil(), original.ValidUntil())
	}
	if rebuilt.UsageLimit() == nil || *rebuilt.UsageLimit() != *original.UsageLimit() {
		t.Errorf("UsageLimit: got %v, want %v", rebuilt.UsageLimit(), original.UsageLimit())
	}
	if rebuilt.UsedCount() != original.UsedCount() {
		t.Errorf("UsedCount: got %d, want %d", rebuilt.UsedCount(), original.UsedCount())
	}
	if rebuilt.PerAccountUsageLimit() == nil || *rebuilt.PerAccountUsageLimit() != *original.PerAccountUsageLimit() {
		t.Errorf("PerAccountUsageLimit: got %v, want %v", rebuilt.PerAccountUsageLimit(), original.PerAccountUsageLimit())
	}
	if got, want := rebuilt.ApplicableTo(), original.ApplicableTo(); len(got) != len(want) || got[0] != want[0] {
		t.Errorf("ApplicableTo: got %v, want %v", got, want)
	}
	if got, want := rebuilt.ApplicableContractTypes(), original.ApplicableContractTypes(); len(got) != len(want) || got[0] != want[0] {
		t.Errorf("ApplicableContractTypes: got %v, want %v", got, want)
	}
	if got, want := rebuilt.AllowedAccountIDs(), original.AllowedAccountIDs(); len(got) != len(want) || got[0] != want[0] {
		t.Errorf("AllowedAccountIDs: got %v, want %v", got, want)
	}
	if got, want := rebuilt.BlockedAccountIDs(), original.BlockedAccountIDs(); len(got) != len(want) || got[0] != want[0] {
		t.Errorf("BlockedAccountIDs: got %v, want %v", got, want)
	}

	// Behavioral equivalence: the rebuilt coupon computes the same discount and
	// applies the same eligibility rules as the original.
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	origDiscount, err := original.CalculateDiscount(subtotal)
	if err != nil {
		t.Fatalf("original.CalculateDiscount: %v", err)
	}
	rebuiltDiscount, err := rebuilt.CalculateDiscount(subtotal)
	if err != nil {
		t.Fatalf("rebuilt.CalculateDiscount: %v", err)
	}
	if rebuiltDiscount.Amount().Cmp(origDiscount.Amount()) != 0 || rebuiltDiscount.Currency() != origDiscount.Currency() {
		t.Errorf("CalculateDiscount: got %s %s, want %s %s",
			rebuiltDiscount.Amount().RatString(), rebuiltDiscount.Currency(),
			origDiscount.Amount().RatString(), origDiscount.Currency())
	}
	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if rebuilt.IsValid(at) != original.IsValid(at) {
		t.Error("IsValid mismatch between original and rebuilt coupon")
	}
	if rebuilt.IsApplicableToProduct("prod-a") != original.IsApplicableToProduct("prod-a") {
		t.Error("IsApplicableToProduct mismatch between original and rebuilt coupon")
	}
	if rebuilt.IsApplicableToContractType(contract.ContractTypeSubscription) != original.IsApplicableToContractType(contract.ContractTypeSubscription) {
		t.Error("IsApplicableToContractType mismatch between original and rebuilt coupon")
	}
	if rebuilt.IsAccountAllowed("acct-1") != original.IsAccountAllowed("acct-1") {
		t.Error("IsAccountAllowed(allowed) mismatch between original and rebuilt coupon")
	}
	if rebuilt.IsAccountAllowed("acct-9") != original.IsAccountAllowed("acct-9") {
		t.Error("IsAccountAllowed(blocked) mismatch between original and rebuilt coupon")
	}
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
