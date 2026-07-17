package coupon

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// mockCouponRepository is a test double for CouponRepository.
//
// Redemptions are stored by Redemption.IdempotencyKey() so SaveRedemption is
// idempotent on (coupon, contract, billing period); usage is reconciled from
// these rows (issue #185). saveRedemptionCall counts only NON-duplicate (i.e.
// effective) confirmations.
type mockCouponRepository struct {
	mu                 sync.Mutex
	coupons            []*Coupon
	redemptions        map[string]*Redemption // IdempotencyKey -> redemption
	saveRedemptionCall int

	// Error injection for testing error paths
	findApplicableErr  error
	findRedemptionsErr error
	saveRedemptionErr  error
}

func newMockRepo(coupons ...*Coupon) *mockCouponRepository {
	return &mockCouponRepository{
		coupons:     coupons,
		redemptions: make(map[string]*Redemption),
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

func (m *mockCouponRepository) SaveRedemption(_ context.Context, r *Redemption, limits RedemptionLimits) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveRedemptionErr != nil {
		return m.saveRedemptionErr
	}
	key := r.IdempotencyKey()
	if _, exists := m.redemptions[key]; exists {
		return nil // (a) idempotent no-op
	}
	// (b) atomic limit check against existing distinct rows (#195).
	if limits.GlobalLimit != nil {
		if limits.GlobalBaseline+m.countLocked(r.CouponID(), nil) >= *limits.GlobalLimit {
			return ErrUsageLimitReached
		}
	}
	if limits.PerAccountLimit != nil {
		acct := r.AccountID()
		if m.countLocked(r.CouponID(), &acct) >= *limits.PerAccountLimit {
			return ErrUsageLimitReached
		}
	}
	// (c) insert
	m.redemptions[key] = r
	m.saveRedemptionCall++
	return nil
}

// countLocked counts distinct redemptions of a coupon (optionally filtered to an
// account). Callers must hold m.mu.
func (m *mockCouponRepository) countLocked(couponID CouponID, accountID *shared.AccountID) int {
	n := 0
	for _, r := range m.redemptions {
		if r.CouponID() != couponID {
			continue
		}
		if accountID != nil && r.AccountID() != *accountID {
			continue
		}
		n++
	}
	return n
}

func (m *mockCouponRepository) FindRedemptions(_ context.Context, couponID CouponID, accountID *shared.AccountID) ([]*Redemption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.findRedemptionsErr != nil {
		return nil, m.findRedemptionsErr
	}
	var result []*Redemption
	for _, r := range m.redemptions {
		if r.CouponID() != couponID {
			continue
		}
		if accountID != nil && r.AccountID() != *accountID {
			continue
		}
		result = append(result, r)
	}
	return result, nil
}

// allRedemptions returns every stored redemption (order unspecified).
func (m *mockCouponRepository) allRedemptions() []*Redemption {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*Redemption, 0, len(m.redemptions))
	for _, r := range m.redemptions {
		result = append(result, r)
	}
	return result
}

// seed pre-confirms a redemption, simulating prior committed usage.
func (m *mockCouponRepository) seed(r *Redemption) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.redemptions[r.IdempotencyKey()] = r
}

var testClock = shared.FixedClock{FixedTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}

// testPeriod is the billing period used by unit-test calculation contexts.
var testPeriod = mustDateRange(
	time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
)

func mustDateRange(start, end time.Time) shared.DateRange {
	r, err := shared.NewDateRange(start, end)
	if err != nil {
		panic(err)
	}
	return r
}

func newTestContext(subtotal shared.Money) *plugin.CalculationContext {
	cc := plugin.NewCalculationContext(context.Background(), nil, subtotal)
	cc.SetBillingPeriod(testPeriod)
	return cc
}

func newTestContextWithContract(subtotal shared.Money, c *contract.ContractAggregate) *plugin.CalculationContext {
	cc := plugin.NewCalculationContext(context.Background(), c, subtotal)
	cc.SetBillingPeriod(testPeriod)
	return cc
}

// confirmRedemptions drives AfterCalculation for a context whose discounts were
// already computed by CalculateDiscount, building a minimal invoice for the given
// account/contract and the context's billing period. It mirrors what the billing
// pipeline does inside its transaction (issue #185).
func confirmRedemptions(
	t *testing.T,
	p *CouponPlugin,
	ctx *plugin.CalculationContext,
	accountID shared.AccountID,
	contractID shared.ContractID,
) *invoice.Invoice {
	t.Helper()
	zero := shared.Zero(ctx.Subtotal().Currency())
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		ctx.Subtotal(), zero, zero,
		invoice.WithBillingPeriod(ctx.BillingPeriod()),
	)
	if err != nil {
		t.Fatalf("build test invoice: %v", err)
	}
	if err := p.AfterCalculation(ctx, inv); err != nil {
		t.Fatalf("AfterCalculation: %v", err)
	}
	return inv
}

func newTestCoupon(id CouponID, code string, ct CouponType, value *big.Rat, applicableTo []shared.ProductID) *Coupon {
	return mustCoupon(NewCoupon(
		id, code, ct, value, shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, applicableTo,
	))
}

// TestCouponPlugin_DefensivelySkipsExpiredCoupon guards review M4: even if a repo
// returns an expired/exhausted coupon, the plugin must defensively skip it via
// IsValid rather than apply it and record a redemption.
func TestCouponPlugin_DefensivelySkipsExpiredCoupon(t *testing.T) {
	expired := mustCoupon(NewCoupon(
		"c-exp", "OLD10", CouponTypePercentage, big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), // expired before testClock (2026-06-01)
		nil, 0, nil,
	))
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
	usdCoupon := mustCoupon(NewCoupon(
		"c-usd", "USD5", CouponTypeFixed, big.NewRat(5, 1), shared.CurrencyUSD,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		nil, 0, nil,
	))
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
	coupon := mustCoupon(NewCoupon(
		"c1", "BIG50", CouponTypePercentage,
		big.NewRat(50, 100), shared.CurrencyJPY,
		nil, &maxDiscount,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	))

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

// TestCouponPlugin_NoStackingReturnsFirst verifies that with stacking disabled
// (the default) only the first VALID coupon is applied ("first valid wins").
// Both coupons here are valid, so the first is selected.
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
		t.Errorf("expected discount 1000 (first valid coupon only), got %s", discount.Amount().RatString())
	}

	discounts := ctx.AppliedDiscounts()
	if len(discounts) != 1 {
		t.Errorf("expected 1 recorded discount, got %d", len(discounts))
	}
}

// newExpiredCoupon builds a coupon whose validity window closed before testClock.
func newExpiredCoupon(id CouponID, code string, value *big.Rat) *Coupon {
	return mustCoupon(NewCoupon(
		id, code, CouponTypePercentage, value, shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), // expired before testClock (2026-06-01)
		nil, 0, nil,
	))
}

// TestCouponPlugin_NoStacking_InvalidFirstValidSecond is the core regression for
// #158: with stacking disabled, a leading INVALID coupon (expired) must not
// crowd out a valid coupon behind it. Before the fix the plugin truncated to
// coupons[:1] before validation, so the customer received NO discount despite
// holding a valid coupon.
func TestCouponPlugin_NoStacking_InvalidFirstValidSecond(t *testing.T) {
	expired := newExpiredCoupon("c-exp", "OLD10", big.NewRat(10, 100))
	valid := newTestCoupon("c-valid", "SAVE20", CouponTypePercentage, big.NewRat(20, 100), nil)

	repo := newMockRepo(expired, valid)
	p := NewCouponPlugin(repo, testClock) // default: AllowStacking=false

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The expired coupon is skipped; the valid coupon (20% of 10000) applies.
	expected := big.NewRat(2000, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 2000 (valid coupon applied despite expired first), got %s", discount.Amount().RatString())
	}

	discounts := ctx.AppliedDiscounts()
	if len(discounts) != 1 {
		t.Fatalf("expected 1 recorded discount, got %d", len(discounts))
	}
	if discounts[0].Code != "SAVE20" {
		t.Errorf("expected valid coupon SAVE20 to be applied, got %s", discounts[0].Code)
	}
	// The expired coupon must not have produced a redemption: confirming only
	// yields the single valid coupon's redemption.
	confirmRedemptions(t, p, ctx, "", "")
	if repo.saveRedemptionCall != 1 {
		t.Errorf("expected exactly 1 redemption (valid coupon only), got %d", repo.saveRedemptionCall)
	}
}

// TestCouponPlugin_AllInvalid_ZeroDiscount verifies that when every candidate
// coupon fails validation the result is a zero discount with no redemptions.
func TestCouponPlugin_AllInvalid_ZeroDiscount(t *testing.T) {
	expired1 := newExpiredCoupon("c-exp1", "OLD10", big.NewRat(10, 100))
	expired2 := newExpiredCoupon("c-exp2", "OLD20", big.NewRat(20, 100))

	repo := newMockRepo(expired1, expired2)
	p := NewCouponPlugin(repo, testClock)

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !discount.IsZero() {
		t.Errorf("expected zero discount when all coupons invalid, got %s", discount.Amount().RatString())
	}
	if repo.saveRedemptionCall != 0 {
		t.Errorf("expected no redemptions when all coupons invalid, got %d", repo.saveRedemptionCall)
	}
}

// TestCouponPlugin_MaxCountsValidatedCoupons verifies that MaxCouponsPerInvoice counts
// VALIDATED (applied) coupons, not scanned ones: with stacking on and max=1, a
// leading invalid coupon is skipped and the following valid coupon still fills
// the single slot.
func TestCouponPlugin_MaxCountsValidatedCoupons(t *testing.T) {
	expired := newExpiredCoupon("c-exp", "OLD10", big.NewRat(10, 100))
	valid1 := newTestCoupon("c-v1", "SAVE20", CouponTypePercentage, big.NewRat(20, 100), nil)
	valid2 := newTestCoupon("c-v2", "SAVE5", CouponTypePercentage, big.NewRat(5, 100), nil)

	repo := newMockRepo(expired, valid1, valid2)
	p := NewCouponPlugin(repo, testClock)
	_ = p.Initialize(context.Background(), plugin.Config{
		"allowStacking":        true,
		"maxCouponsPerInvoice": 1,
	})

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// max=1 validated coupon: expired is skipped, first valid (20%) fills the slot.
	expected := big.NewRat(2000, 1)
	if discount.Amount().Cmp(expected) != 0 {
		t.Errorf("expected discount 2000 (first valid coupon fills the single slot), got %s", discount.Amount().RatString())
	}

	discounts := ctx.AppliedDiscounts()
	if len(discounts) != 1 {
		t.Fatalf("expected 1 recorded discount, got %d", len(discounts))
	}
	if discounts[0].Code != "SAVE20" {
		t.Errorf("expected SAVE20 applied, got %s", discounts[0].Code)
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
	// Prior committed redemption by the SAME account on a DIFFERENT contract —
	// counts toward the per-account limit (it is not the in-flight use).
	repo.seed(NewRedemption(
		"r-prior", "c1", "ONCE10", CodeTypeShared,
		"acc-1", "other-contract", testPeriod, "inv-prior", testClock.Now(),
	))
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

	// CalculateDiscount must NOT persist anything (issue #185): the redemption is
	// confirmed only in AfterCalculation, inside the billing transaction.
	if repo.saveRedemptionCall != 0 {
		t.Fatalf("CalculateDiscount must not save redemptions, got %d", repo.saveRedemptionCall)
	}

	confirmRedemptions(t, p, ctx, agg.AccountID(), agg.ContractID())

	if repo.saveRedemptionCall != 1 {
		t.Errorf("expected 1 redemption saved, got %d", repo.saveRedemptionCall)
	}
	reds := repo.allRedemptions()
	if len(reds) != 1 {
		t.Fatalf("expected 1 redemption, got %d", len(reds))
	}
	r := reds[0]
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
	if !r.BillingPeriod().Equals(testPeriod) {
		t.Errorf("expected redemption keyed by billing period %s, got %s", testPeriod, r.BillingPeriod())
	}
}

func TestCouponPlugin_MinAmountNotMet(t *testing.T) {
	minAmt := shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY)
	coupon := mustCoupon(NewCoupon(
		"c1", "MIN5000", CouponTypePercentage,
		big.NewRat(10, 100), shared.CurrencyJPY,
		&minAmt, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, nil,
	))
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

	// Verify redemption was confirmed with the unique code (in AfterCalculation).
	confirmRedemptions(t, p, ctx, agg.AccountID(), agg.ContractID())
	reds := repo.allRedemptions()
	if len(reds) != 1 {
		t.Fatalf("expected 1 redemption, got %d", len(reds))
	}
	if reds[0].Code() != "UNIQUE-ABC123" {
		t.Errorf("expected unique code in redemption, got %s", reds[0].Code())
	}
	if reds[0].CodeType() != CodeTypeUnique {
		t.Errorf("expected code type unique in redemption, got %s", reds[0].CodeType())
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

// TestCouponPlugin_CalculateDiscountHasNoWriteSideEffects verifies the core of
// issue #185: CalculateDiscount computes and records the discount but performs no
// persistence — even when the repository's SaveRedemption would fail, discount
// calculation succeeds and nothing is written.
func TestCouponPlugin_CalculateDiscountHasNoWriteSideEffects(t *testing.T) {
	coupon := newTestCoupon("c1", "SAVE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	repo := newMockRepo(coupon)
	repo.saveRedemptionErr = fmt.Errorf("redemption save failed")
	p := NewCouponPlugin(repo, testClock)

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("CalculateDiscount must not touch the repository, got error: %v", err)
	}
	if discount.Amount().Cmp(big.NewRat(1000, 1)) != 0 {
		t.Errorf("expected discount 1000, got %s", discount.Amount().RatString())
	}
	if repo.saveRedemptionCall != 0 {
		t.Errorf("expected no redemptions written during calculation, got %d", repo.saveRedemptionCall)
	}
}

// TestCouponPlugin_AfterCalculationSurfacesSaveError verifies that a redemption
// confirmation failure in AfterCalculation is returned (fatal) so the billing
// transaction rolls back rather than persisting an invoice without recording the
// coupon use.
func TestCouponPlugin_AfterCalculationSurfacesSaveError(t *testing.T) {
	coupon := newTestCoupon("c1", "SAVE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "acc-1")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	if _, err := p.CalculateDiscount(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repo.saveRedemptionErr = fmt.Errorf("redemption save failed")
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		subtotal, shared.Zero(shared.CurrencyJPY), shared.Zero(shared.CurrencyJPY),
		invoice.WithBillingPeriod(ctx.BillingPeriod()),
	)
	if err != nil {
		t.Fatalf("build invoice: %v", err)
	}
	if err := p.AfterCalculation(ctx, inv); err == nil {
		t.Fatal("expected AfterCalculation to surface SaveRedemption error, got nil")
	}
}

// TestCouponPlugin_DoesNotWriteSubtotalAfterDiscount verifies the plugin never
// writes ctx.SetSubtotalAfterDiscount — that field is owned by the core billing
// pipeline, which sets it after summing ALL DiscountHook results, rounding, and
// applying the cap guard (issue #244). A plugin-side write would be overwritten
// anyway and, with multiple DiscountHooks, would expose a misleading
// intermediate value.
func TestCouponPlugin_DoesNotWriteSubtotalAfterDiscount(t *testing.T) {
	coupon := newTestCoupon("c1", "SAVE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if discount.Amount().Cmp(big.NewRat(1000, 1)) != 0 {
		t.Errorf("expected discount 1000, got %s", discount.Amount().RatString())
	}

	// SubtotalAfterDiscount must be untouched (still the initial subtotal);
	// the core sets it after the cap guard, not the plugin.
	if ctx.SubtotalAfterDiscount().Amount().Cmp(subtotal.Amount()) != 0 {
		t.Errorf("expected SubtotalAfterDiscount to be untouched (%s), got %s",
			subtotal.Amount().RatString(), ctx.SubtotalAfterDiscount().Amount().RatString())
	}
}

// TestCouponPlugin_Initialize_ConfigTypes covers issue #239: JSON-decoded
// configs (float64 numbers) work, and present-but-mistyped values are
// configuration errors rather than silently ignored. Unknown keys stay ignored.
func TestCouponPlugin_Initialize_ConfigTypes(t *testing.T) {
	newPlugin := func() *CouponPlugin { return NewCouponPlugin(newMockRepo(), testClock) }

	t.Run("json float64 accepted", func(t *testing.T) {
		p := newPlugin()
		if err := p.Initialize(context.Background(), plugin.Config{
			"maxCouponsPerInvoice": float64(3),
			"allowStacking":        true,
			"priority":             float64(42),
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.config.MaxCouponsPerInvoice != 3 {
			t.Errorf("expected MaxCouponsPerInvoice 3, got %d", p.config.MaxCouponsPerInvoice)
		}
		if !p.config.AllowStacking {
			t.Error("expected AllowStacking true")
		}
		if p.Priority() != 42 {
			t.Errorf("expected priority 42, got %d", p.Priority())
		}
	})

	t.Run("string for int key rejected", func(t *testing.T) {
		p := newPlugin()
		if err := p.Initialize(context.Background(), plugin.Config{"maxCouponsPerInvoice": "3"}); err == nil {
			t.Fatal("expected error for string maxCouponsPerInvoice, got nil")
		}
		if err := p.Initialize(context.Background(), plugin.Config{"priority": "high"}); err == nil {
			t.Fatal("expected error for string priority, got nil")
		}
	})

	t.Run("non-integral float rejected", func(t *testing.T) {
		p := newPlugin()
		if err := p.Initialize(context.Background(), plugin.Config{"maxCouponsPerInvoice": 2.5}); err == nil {
			t.Fatal("expected error for non-integral float, got nil")
		}
	})

	t.Run("non-bool for bool key rejected", func(t *testing.T) {
		p := newPlugin()
		if err := p.Initialize(context.Background(), plugin.Config{"allowStacking": "yes"}); err == nil {
			t.Fatal("expected error for string allowStacking, got nil")
		}
	})

	t.Run("unknown keys ignored", func(t *testing.T) {
		p := newPlugin()
		if err := p.Initialize(context.Background(), plugin.Config{"unknownKey": "whatever"}); err != nil {
			t.Fatalf("unexpected error for unknown key: %v", err)
		}
	})
}

// createTestAggregate creates a ContractAggregate via the Create command for testing.
// limitedCoupon builds a coupon valid around testClock with the given global and
// per-account usage limits (0 = leave unset / unlimited).
func limitedCoupon(id CouponID, code string, globalLimit, perAccountLimit int) *Coupon {
	var gl *int
	if globalLimit > 0 {
		gl = &globalLimit
	}
	c := mustCoupon(NewCoupon(
		id, code, CouponTypePercentage, big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		testClock.Now().AddDate(-1, 0, 0), testClock.Now().AddDate(1, 0, 0),
		gl, 0, nil,
	))
	if perAccountLimit > 0 {
		c.WithPerAccountUsageLimit(perAccountLimit)
	}
	return c
}

// prepareConfirm computes the discount for a fresh calculation context bound to
// the given aggregate and returns a closure that confirms the redemption (drives
// AfterCalculation), mirroring what the billing pipeline does inside its
// transaction. The returned closure is safe to run from a goroutine (it does not
// touch *testing.T).
func prepareConfirm(t *testing.T, p *CouponPlugin, agg *contract.ContractAggregate) func() error {
	t.Helper()
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)
	if _, err := p.CalculateDiscount(ctx); err != nil {
		t.Fatalf("CalculateDiscount: %v", err)
	}
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		subtotal, shared.Zero(shared.CurrencyJPY), shared.Zero(shared.CurrencyJPY),
		invoice.WithBillingPeriod(ctx.BillingPeriod()),
	)
	if err != nil {
		t.Fatalf("build invoice: %v", err)
	}
	return func() error { return p.AfterCalculation(ctx, inv) }
}

// runConcurrently fires every confirm closure from its own goroutine, released
// together, and returns their errors.
func runConcurrently(confirms []func() error) []error {
	errs := make([]error, len(confirms))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range confirms {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = confirms[i]()
		}(i)
	}
	close(start)
	wg.Wait()
	return errs
}

// classifyConfirmErrors partitions confirmation errors into successes and
// limit-reached rejections, failing on any other error.
func classifyConfirmErrors(t *testing.T, errs []error) (ok, limitReached int) {
	t.Helper()
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrUsageLimitReached):
			limitReached++
		default:
			t.Fatalf("unexpected confirmation error: %v", err)
		}
	}
	return ok, limitReached
}

// TestCouponPlugin_ConcurrentConfirm_DistinctKeys_GlobalLimit is the core #195
// race test: N concurrent confirmations of DISTINCT (contract, period) keys
// against a global usageLimit L < N must let EXACTLY L through — the atomic
// SaveRedemption gate rejects the rest with ErrUsageLimitReached even though all
// N passed CalculateDiscount's advisory read (all saw zero prior redemptions).
func TestCouponPlugin_ConcurrentConfirm_DistinctKeys_GlobalLimit(t *testing.T) {
	const n, limit = 8, 3
	repo := newMockRepo(limitedCoupon("c-global", "SAVE10", limit, 0))
	p := NewCouponPlugin(repo, testClock)

	confirms := make([]func() error, n)
	for i := 0; i < n; i++ {
		agg := createTestAggregate(t, shared.AccountID(fmt.Sprintf("acc-%d", i)))
		confirms[i] = prepareConfirm(t, p, agg)
	}

	ok, limitReached := classifyConfirmErrors(t, runConcurrently(confirms))
	if ok != limit {
		t.Errorf("expected exactly %d successful confirmations, got %d", limit, ok)
	}
	if limitReached != n-limit {
		t.Errorf("expected %d limit-reached rejections, got %d", n-limit, limitReached)
	}
	if got := repo.saveRedemptionCall; got != limit {
		t.Errorf("expected exactly %d persisted redemptions, got %d", limit, got)
	}
}

// TestCouponPlugin_ConcurrentConfirm_SameKey_CollapsesToOne verifies the #185
// invariant still holds under the new atomic contract: N concurrent confirmations
// of the SAME (contract, period) key all succeed (idempotent) and collapse to a
// single redemption — never counted as more than one use.
func TestCouponPlugin_ConcurrentConfirm_SameKey_CollapsesToOne(t *testing.T) {
	const n = 8
	repo := newMockRepo(limitedCoupon("c-same", "SAVE10", 100, 0))
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "acc-1")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)
	if _, err := p.CalculateDiscount(ctx); err != nil {
		t.Fatalf("CalculateDiscount: %v", err)
	}
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		subtotal, shared.Zero(shared.CurrencyJPY), shared.Zero(shared.CurrencyJPY),
		invoice.WithBillingPeriod(ctx.BillingPeriod()),
	)
	if err != nil {
		t.Fatalf("build invoice: %v", err)
	}

	confirms := make([]func() error, n)
	for i := range confirms {
		confirms[i] = func() error { return p.AfterCalculation(ctx, inv) }
	}
	ok, limitReached := classifyConfirmErrors(t, runConcurrently(confirms))
	if ok != n {
		t.Errorf("expected all %d same-key confirmations to succeed, got %d ok / %d limit-reached", n, ok, limitReached)
	}
	if got := repo.saveRedemptionCall; got != 1 {
		t.Errorf("expected same-key confirmations to collapse to 1 redemption, got %d", got)
	}
}

// TestCouponPlugin_ConcurrentConfirm_DistinctKeys_PerAccountLimit is the
// per-account variant of the #195 race: one account, N distinct contracts
// (distinct keys), perAccountUsageLimit L < N → exactly L succeed.
func TestCouponPlugin_ConcurrentConfirm_DistinctKeys_PerAccountLimit(t *testing.T) {
	const n, limit = 6, 2
	repo := newMockRepo(limitedCoupon("c-acct", "SAVE10", 0, limit))
	p := NewCouponPlugin(repo, testClock)

	confirms := make([]func() error, n)
	for i := 0; i < n; i++ {
		agg := createTestAggregate(t, "acc-shared")
		confirms[i] = prepareConfirm(t, p, agg)
	}

	ok, limitReached := classifyConfirmErrors(t, runConcurrently(confirms))
	if ok != limit {
		t.Errorf("expected exactly %d successful confirmations, got %d", limit, ok)
	}
	if limitReached != n-limit {
		t.Errorf("expected %d limit-reached rejections, got %d", n-limit, limitReached)
	}
	if got := repo.saveRedemptionCall; got != limit {
		t.Errorf("expected exactly %d persisted redemptions, got %d", limit, got)
	}
}

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
