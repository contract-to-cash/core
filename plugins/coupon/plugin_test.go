package coupon

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// mockCouponRepository is a test double for CouponRepository.
//
// SaveRedemption and RecordUsage are idempotent on (couponID, contractID) as the
// CouponRepository contract requires: a repeated write for a pair that already has
// a redemption is a no-op. recordUsageCalled / saveRedemptionCall count only the
// writes that actually took effect, so a double CommitDiscounts does not inflate them.
type mockCouponRepository struct {
	coupons            []*Coupon
	accountUsage       map[string]int  // key: couponID:accountID
	redeemed           map[string]bool // key: couponID:contractID (idempotency guard)
	usageCount         map[string]int  // key: couponID:contractID
	redemptions        []*Redemption
	recordUsageCalled  int
	saveRedemptionCall int

	// Error injection for testing error paths
	findApplicableErr error
	findUsageErr      error
	recordUsageErr    error
	saveRedemptionErr error
}

func newMockRepo(coupons ...*Coupon) *mockCouponRepository {
	return &mockCouponRepository{
		coupons:      coupons,
		accountUsage: make(map[string]int),
		redeemed:     make(map[string]bool),
		usageCount:   make(map[string]int),
	}
}

func (m *mockCouponRepository) FindByCode(_ context.Context, code string) (*Coupon, error) {
	for _, c := range m.coupons {
		if c.Code() == code {
			return c, nil
		}
	}
	return nil, nil
}

func (m *mockCouponRepository) FindApplicable(_ context.Context, _ CouponQuery) ([]*Coupon, error) {
	if m.findApplicableErr != nil {
		return nil, m.findApplicableErr
	}
	return m.coupons, nil
}

func (m *mockCouponRepository) Save(_ context.Context, _ *Coupon) error {
	return nil
}

func (m *mockCouponRepository) RecordUsage(_ context.Context, couponID CouponID, contractID shared.ContractID) error {
	if m.recordUsageErr != nil {
		return m.recordUsageErr
	}
	// Idempotent on (couponID, contractID): a repeated increment for the same pair
	// is a no-op (see CouponRepository contract).
	key := string(couponID) + ":" + string(contractID)
	if m.usageCount[key] > 0 {
		return nil
	}
	m.usageCount[key]++
	m.recordUsageCalled++
	return nil
}

func (m *mockCouponRepository) FindUsageByAccount(_ context.Context, couponID CouponID, accountID shared.AccountID) (int, error) {
	if m.findUsageErr != nil {
		return 0, m.findUsageErr
	}
	key := string(couponID) + ":" + string(accountID)
	return m.accountUsage[key], nil
}

func (m *mockCouponRepository) SaveRedemption(_ context.Context, r *Redemption) error {
	if m.saveRedemptionErr != nil {
		return m.saveRedemptionErr
	}
	// Idempotent on (couponID, contractID): if a redemption already exists for the
	// pair, do not append a duplicate (see CouponRepository contract).
	key := string(r.CouponID()) + ":" + string(r.ContractID())
	if m.redeemed[key] {
		return nil
	}
	m.redeemed[key] = true
	m.saveRedemptionCall++
	m.redemptions = append(m.redemptions, r)
	return nil
}

func (m *mockCouponRepository) FindRedemptions(_ context.Context, _ CouponID, _ *shared.AccountID) ([]*Redemption, error) {
	return m.redemptions, nil
}

var testClock = shared.FixedClock{FixedTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}

func newTestContext(subtotal shared.Money) *plugin.CalculationContext {
	return plugin.NewCalculationContext(context.Background(), nil, subtotal)
}

func newTestContextWithContract(subtotal shared.Money, c *contract.ContractAggregate) *plugin.CalculationContext {
	return plugin.NewCalculationContext(context.Background(), c, subtotal)
}

func newTestCoupon(id CouponID, code string, ct CouponType, value *big.Rat, applicableTo []shared.ProductID) *Coupon {
	return NewCoupon(
		id, code, ct, value, shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, applicableTo,
	)
}

// TestCouponPlugin_DefensivelySkipsExpiredCoupon guards review M4: even if a repo
// returns an expired/exhausted coupon, the plugin must defensively skip it via
// IsValid rather than apply it and record a redemption.
func TestCouponPlugin_DefensivelySkipsExpiredCoupon(t *testing.T) {
	expired := NewCoupon(
		"c-exp", "OLD10", CouponTypePercentage, big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), // expired before testClock (2026-06-01)
		nil, 0, nil,
	)
	repo := newMockRepo(expired)
	p := NewCouponPlugin(repo, testClock)

	ctx := newTestContext(shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY))
	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !discount.IsZero() {
		t.Errorf("expected no discount for expired coupon, got %s", discount.Amount().RatString())
	}
	if repo.saveRedemptionCall != 0 {
		t.Errorf("expected no redemption recorded for expired coupon, got %d", repo.saveRedemptionCall)
	}
}

// TestCouponPlugin_SkipsForeignCurrencyFixedCoupon guards review W6: a fixed-amount
// coupon denominated in a different currency from the invoice must be skipped, not
// abort the whole invoice calculation with a currency-mismatch error.
func TestCouponPlugin_SkipsForeignCurrencyFixedCoupon(t *testing.T) {
	usdCoupon := NewCoupon(
		"c-usd", "USD5", CouponTypeFixed, big.NewRat(5, 1), shared.CurrencyUSD,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		nil, 0, nil,
	)
	repo := newMockRepo(usdCoupon)
	p := NewCouponPlugin(repo, testClock)

	ctx := newTestContext(shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY))
	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("expected no error (coupon should be skipped), got %v", err)
	}
	if discount.Currency() != shared.CurrencyJPY || !discount.IsZero() {
		t.Errorf("expected zero JPY discount, got %s %s", discount.Amount().RatString(), discount.Currency())
	}
	if repo.saveRedemptionCall != 0 {
		t.Errorf("expected no redemption for foreign-currency coupon, got %d", repo.saveRedemptionCall)
	}
}

func TestCouponPlugin_PercentageDiscount(t *testing.T) {
	coupon := newTestCoupon("c1", "SAVE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

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
	coupon := newTestCoupon("c1", "FIX500", CouponTypeFixed, big.NewRat(500, 1), nil)
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

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

	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

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
	coupon1 := newTestCoupon("c1", "FIRST10", CouponTypePercentage, big.NewRat(10, 100), nil)
	coupon2 := newTestCoupon("c2", "SECOND20", CouponTypePercentage, big.NewRat(20, 100), nil)

	repo := newMockRepo(coupon1, coupon2)
	p := NewCouponPlugin(repo, testClock)

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
	repo := newMockRepo()
	p := NewCouponPlugin(repo, testClock)

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

func TestCouponPlugin_ApplicableToProduct_Match(t *testing.T) {
	// Coupon restricted to product "product-gold"
	coupon := newTestCoupon("c1", "GOLD10", CouponTypePercentage, big.NewRat(10, 100), []shared.ProductID{"product-gold"})
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	// Create a contract and set matching productID on context
	agg := createTestAggregate(t, "acc-1")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)
	ctx.SetProductID("product-gold")

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := big.NewRat(1000, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 1000, got %s", discount.Amount().RatString())
	}
}

func TestCouponPlugin_ApplicableToProduct_NoMatch(t *testing.T) {
	// Coupon restricted to product "product-gold", but context has "product-silver"
	coupon := newTestCoupon("c1", "GOLD10", CouponTypePercentage, big.NewRat(10, 100), []shared.ProductID{"product-gold"})
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "acc-1")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)
	ctx.SetProductID("product-silver")

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !discount.IsZero() {
		t.Errorf("expected zero discount for non-matching product, got %s", discount.Amount().RatString())
	}
}

func TestCouponPlugin_AccountBlocklist(t *testing.T) {
	coupon := newTestCoupon("c1", "SAVE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	coupon.WithBlockedAccountIDs([]shared.AccountID{"blocked-acc"})
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "blocked-acc")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !discount.IsZero() {
		t.Errorf("expected zero discount for blocked account, got %s", discount.Amount().RatString())
	}
}

func TestCouponPlugin_AccountAllowlist(t *testing.T) {
	coupon := newTestCoupon("c1", "VIP10", CouponTypePercentage, big.NewRat(10, 100), nil)
	coupon.WithAllowedAccountIDs([]shared.AccountID{"vip-acc"})
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	// Non-VIP account should get zero discount
	agg := createTestAggregate(t, "regular-acc")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !discount.IsZero() {
		t.Errorf("expected zero discount for non-allowed account, got %s", discount.Amount().RatString())
	}

	// VIP account should get discount
	vipAgg := createTestAggregate(t, "vip-acc")
	ctx2 := newTestContextWithContract(subtotal, vipAgg)

	discount2, err := p.CalculateDiscount(ctx2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := big.NewRat(1000, 1)
	if discount2.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 1000 for VIP, got %s", discount2.Amount().RatString())
	}
}

func TestCouponPlugin_PerAccountUsageLimit(t *testing.T) {
	coupon := newTestCoupon("c1", "ONCE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	coupon.WithPerAccountUsageLimit(1)
	repo := newMockRepo(coupon)
	repo.accountUsage["c1:acc-1"] = 1 // already used once
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "acc-1")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !discount.IsZero() {
		t.Errorf("expected zero discount when per-account limit reached, got %s", discount.Amount().RatString())
	}
}

func TestCouponPlugin_RedemptionRecorded(t *testing.T) {
	coupon := newTestCoupon("c1", "SAVE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "acc-1")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	_, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// CalculateDiscount is side-effect-free: nothing is persisted yet.
	if repo.saveRedemptionCall != 0 {
		t.Fatalf("CalculateDiscount must not persist redemptions, got %d", repo.saveRedemptionCall)
	}

	// The core commits the recorded discounts inside the billing transaction.
	if err := p.CommitDiscounts(context.Background(), ctx.AppliedDiscounts()); err != nil {
		t.Fatalf("unexpected CommitDiscounts error: %v", err)
	}

	if repo.saveRedemptionCall != 1 {
		t.Errorf("expected 1 redemption saved, got %d", repo.saveRedemptionCall)
	}
	if len(repo.redemptions) != 1 {
		t.Fatalf("expected 1 redemption, got %d", len(repo.redemptions))
	}
	r := repo.redemptions[0]
	if r.CouponID() != "c1" {
		t.Errorf("expected coupon ID c1, got %s", r.CouponID())
	}
	if r.AccountID() != "acc-1" {
		t.Errorf("expected account ID acc-1, got %s", r.AccountID())
	}
	if r.Code() != "SAVE10" {
		t.Errorf("expected code SAVE10, got %s", r.Code())
	}
	if r.CodeType() != CodeTypeShared {
		t.Errorf("expected code type shared, got %s", r.CodeType())
	}
}

func TestCouponPlugin_MinAmountNotMet(t *testing.T) {
	minAmt := shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY)
	coupon := NewCoupon(
		"c1", "MIN5000", CouponTypePercentage,
		big.NewRat(10, 100), shared.CurrencyJPY,
		&minAmt, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	)
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	// Subtotal 3000 < minAmount 5000 -> no discount
	subtotal := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !discount.IsZero() {
		t.Errorf("expected zero discount when subtotal < minAmount, got %s", discount.Amount().RatString())
	}

	// Subtotal 10000 >= minAmount 5000 -> discount applied
	subtotal2 := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx2 := newTestContext(subtotal2)

	discount2, err := p.CalculateDiscount(ctx2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := big.NewRat(1000, 1) // 10% of 10000
	if discount2.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 1000, got %s", discount2.Amount().RatString())
	}
}

func TestCouponPlugin_UniqueCodeType(t *testing.T) {
	coupon := newTestCoupon("c1", "UNIQUE-ABC123", CouponTypeFixed, big.NewRat(1000, 1), nil)
	coupon.WithCodeType(CodeTypeUnique).WithPerAccountUsageLimit(1)
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "acc-1")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := big.NewRat(1000, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 1000, got %s", discount.Amount().RatString())
	}

	// Commit the recorded discount (as the billing pipeline does).
	if err := p.CommitDiscounts(context.Background(), ctx.AppliedDiscounts()); err != nil {
		t.Fatalf("unexpected CommitDiscounts error: %v", err)
	}

	// Verify redemption was saved with the unique code and code type (looked up by
	// code during commit).
	if len(repo.redemptions) != 1 {
		t.Fatalf("expected 1 redemption, got %d", len(repo.redemptions))
	}
	if repo.redemptions[0].Code() != "UNIQUE-ABC123" {
		t.Errorf("expected unique code in redemption, got %s", repo.redemptions[0].Code())
	}
	if repo.redemptions[0].CodeType() != CodeTypeUnique {
		t.Errorf("expected code type unique in redemption, got %s", repo.redemptions[0].CodeType())
	}
}

func TestCouponPlugin_ContractType_Match(t *testing.T) {
	coupon := newTestCoupon("c1", "SUB10", CouponTypePercentage, big.NewRat(10, 100), nil)
	coupon.WithApplicableContractTypes([]contract.ContractType{contract.ContractTypeSubscription})
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "acc-1") // subscription type
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := big.NewRat(1000, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 1000, got %s", discount.Amount().RatString())
	}
}

func TestCouponPlugin_ContractType_NoMatch(t *testing.T) {
	// Coupon only for subscription, but contract is one_time
	coupon := newTestCoupon("c1", "SUB10", CouponTypePercentage, big.NewRat(10, 100), nil)
	coupon.WithApplicableContractTypes([]contract.ContractType{contract.ContractTypeSubscription})
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregateWithType(t, "acc-1", contract.ContractTypeOneTime)
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !discount.IsZero() {
		t.Errorf("expected zero discount for non-matching contract type, got %s", discount.Amount().RatString())
	}
}

func TestCouponPlugin_StackingWithMultipleCoupons(t *testing.T) {
	coupon1 := newTestCoupon("c1", "FIRST10", CouponTypePercentage, big.NewRat(10, 100), nil)
	coupon2 := newTestCoupon("c2", "SECOND5", CouponTypePercentage, big.NewRat(5, 100), nil)
	coupon3 := newTestCoupon("c3", "THIRD20", CouponTypePercentage, big.NewRat(20, 100), nil)

	repo := newMockRepo(coupon1, coupon2, coupon3)
	p := NewCouponPlugin(repo, testClock)
	_ = p.Initialize(context.Background(), plugin.Config{
		"allowStacking":        true,
		"maxCouponsPerInvoice": 2,
	})

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Stacking=true, max=2: first two coupons applied (10% + 5% = 1500)
	expected := big.NewRat(1500, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 1500 (two coupons stacked), got %s", discount.Amount().RatString())
	}

	discounts := ctx.AppliedDiscounts()
	if len(discounts) != 2 {
		t.Errorf("expected 2 recorded discounts, got %d", len(discounts))
	}
}

// TestCouponPlugin_ZeroMaxCouponsMeansUnlimited guards W1: an explicit
// maxCouponsPerInvoice of 0 must mean "no limit" (matching the documented
// reference implementation's `> 0` sentinel), NOT "apply zero coupons".
// Regression: the slice-truncation form silently zeroed out all discounts.
func TestCouponPlugin_ZeroMaxCouponsMeansUnlimited(t *testing.T) {
	coupon1 := newTestCoupon("c1", "FIRST10", CouponTypePercentage, big.NewRat(10, 100), nil)
	coupon2 := newTestCoupon("c2", "SECOND5", CouponTypePercentage, big.NewRat(5, 100), nil)

	repo := newMockRepo(coupon1, coupon2)
	p := NewCouponPlugin(repo, testClock)
	_ = p.Initialize(context.Background(), plugin.Config{
		"allowStacking":        true,
		"maxCouponsPerInvoice": 0, // 0 = unlimited
	})

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 0 = no limit: both coupons applied (10% + 5% = 1500), not zeroed out.
	expected := big.NewRat(1500, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 1500 (0=unlimited), got %s", discount.Amount().RatString())
	}
	if got := len(ctx.AppliedDiscounts()); got != 2 {
		t.Errorf("expected 2 applied discounts, got %d", got)
	}
}

func TestCouponPlugin_FindApplicableError(t *testing.T) {
	repo := newMockRepo()
	repo.findApplicableErr = fmt.Errorf("db connection failed")
	p := NewCouponPlugin(repo, testClock)

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	_, err := p.CalculateDiscount(ctx)
	if err == nil {
		t.Fatal("expected error from FindApplicable, got nil")
	}
}

func TestCouponPlugin_SaveRedemptionError_NoUsageRecorded(t *testing.T) {
	coupon := newTestCoupon("c1", "SAVE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	repo := newMockRepo(coupon)
	repo.saveRedemptionErr = fmt.Errorf("redemption save failed")
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "acc-1")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	// Calculation itself never touches the repo now.
	if _, err := p.CalculateDiscount(ctx); err != nil {
		t.Fatalf("unexpected CalculateDiscount error: %v", err)
	}

	// The failure surfaces at commit time; RecordUsage must not run after
	// SaveRedemption fails (redemption precedes usage).
	err := p.CommitDiscounts(context.Background(), ctx.AppliedDiscounts())
	if err == nil {
		t.Fatal("expected error from SaveRedemption, got nil")
	}
	if repo.recordUsageCalled != 0 {
		t.Errorf("expected 0 usage recordings when redemption fails, got %d", repo.recordUsageCalled)
	}
}

// TestCouponPlugin_CalculateDiscount_NoSideEffects is the core regression guard for
// issue #123: CalculateDiscount must be side-effect-free. Even when a coupon is
// fully applicable and a discount is produced, no redemption or usage write may
// occur during the calculation phase — those are deferred to CommitDiscounts, which
// the core runs inside the billing transaction.
func TestCouponPlugin_CalculateDiscount_NoSideEffects(t *testing.T) {
	coupon := newTestCoupon("c1", "SAVE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "acc-1")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if discount.Amount().Cmp(big.NewRat(1000, 1)) != 0 {
		t.Fatalf("expected discount 1000, got %s", discount.Amount().RatString())
	}

	if repo.saveRedemptionCall != 0 {
		t.Errorf("CalculateDiscount persisted %d redemptions; want 0 (must be side-effect-free)", repo.saveRedemptionCall)
	}
	if repo.recordUsageCalled != 0 {
		t.Errorf("CalculateDiscount recorded %d usages; want 0 (must be side-effect-free)", repo.recordUsageCalled)
	}
	if len(repo.redemptions) != 0 {
		t.Errorf("CalculateDiscount left %d redemptions; want 0", len(repo.redemptions))
	}

	// The discount intent must be recorded with the identity fields CommitDiscounts needs.
	applied := ctx.AppliedDiscounts()
	if len(applied) != 1 {
		t.Fatalf("expected 1 applied discount recorded, got %d", len(applied))
	}
	if applied[0].Reference != "c1" {
		t.Errorf("expected Reference=c1 (coupon ID), got %q", applied[0].Reference)
	}
	if applied[0].AccountID != agg.AccountID() {
		t.Errorf("expected AccountID %q, got %q", agg.AccountID(), applied[0].AccountID)
	}
	if applied[0].ContractID != agg.ContractID() {
		t.Errorf("expected ContractID %q, got %q", agg.ContractID(), applied[0].ContractID)
	}
}

// TestCouponPlugin_CommitDiscounts_Idempotent verifies that replaying
// CommitDiscounts with the same recorded discounts (as an optimistic-lock retry or
// idempotent replay of the billing transaction would) does not double-count: the
// (couponID, contractID)-keyed repo dedup keeps exactly one redemption and one
// usage increment.
func TestCouponPlugin_CommitDiscounts_Idempotent(t *testing.T) {
	coupon := newTestCoupon("c1", "SAVE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "acc-1")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	if _, err := p.CalculateDiscount(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	applied := ctx.AppliedDiscounts()

	for i := 0; i < 3; i++ {
		if err := p.CommitDiscounts(context.Background(), applied); err != nil {
			t.Fatalf("CommitDiscounts attempt %d failed: %v", i, err)
		}
	}

	if repo.saveRedemptionCall != 1 {
		t.Errorf("expected exactly 1 effective redemption after 3 commits, got %d", repo.saveRedemptionCall)
	}
	if len(repo.redemptions) != 1 {
		t.Errorf("expected exactly 1 stored redemption, got %d", len(repo.redemptions))
	}
	if repo.recordUsageCalled != 1 {
		t.Errorf("expected exactly 1 effective usage increment, got %d", repo.recordUsageCalled)
	}
}

// TestCouponPlugin_CommitDiscounts_IgnoresForeignPlugin verifies CommitDiscounts
// only acts on its own recorded discounts and skips entries without a coupon
// reference or from other plugins.
func TestCouponPlugin_CommitDiscounts_IgnoresForeignPlugin(t *testing.T) {
	repo := newMockRepo()
	p := NewCouponPlugin(repo, testClock)

	applied := []plugin.AppliedDiscount{
		{PluginName: "other", Code: "X", Reference: "z1", ContractID: "cid"},
		{PluginName: "coupon", Code: "Y"}, // no Reference -> skipped defensively
	}
	if err := p.CommitDiscounts(context.Background(), applied); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.saveRedemptionCall != 0 {
		t.Errorf("expected no redemptions for foreign/unreferenced discounts, got %d", repo.saveRedemptionCall)
	}
}

func TestCouponPlugin_SubtotalAfterDiscountUpdated(t *testing.T) {
	coupon := newTestCoupon("c1", "SAVE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	_, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// SubtotalAfterDiscount should be 10000 - 1000 = 9000
	expected := big.NewRat(9000, 1)
	if ctx.SubtotalAfterDiscount().Amount().Cmp(expected) != 0 {
		t.Errorf("expected subtotal after discount 9000, got %s", ctx.SubtotalAfterDiscount().Amount().RatString())
	}
}

// createTestAggregate creates a ContractAggregate via the Create command for testing.
func createTestAggregate(t *testing.T, accountID shared.AccountID) *contract.ContractAggregate {
	t.Helper()
	return createTestAggregateWithType(t, accountID, contract.ContractTypeSubscription)
}

// createTestAggregateWithType creates a ContractAggregate with a specific contract type.
func createTestAggregateWithType(t *testing.T, accountID shared.AccountID, ct contract.ContractType) *contract.ContractAggregate {
	t.Helper()
	contractID := shared.NewContractID()
	agg := contract.NewContractAggregate(contractID, testClock)
	cmd := contract.CreateContractCommand{
		IdempotencyKey: shared.GenerateID(),
		AccountID:      accountID,
		ContractType:   ct,
		Interval:       pricing.Monthly(),
		Price:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
	}
	if err := agg.Create(cmd, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("failed to create contract aggregate: %v", err)
	}
	return agg
}
