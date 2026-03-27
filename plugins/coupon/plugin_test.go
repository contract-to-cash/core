package coupon

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// mockCouponRepository is a test double for CouponRepository.
type mockCouponRepository struct {
	coupons            []*Coupon
	accountUsage       map[string]int // key: couponID:accountID
	redemptions        []*Redemption
	recordUsageCalled  int
	saveRedemptionCall int

	// Error injection for testing error paths
	findApplicableErr    error
	findUsageErr         error
	recordUsageErr       error
	saveRedemptionErr    error
}

func newMockRepo(coupons ...*Coupon) *mockCouponRepository {
	return &mockCouponRepository{
		coupons:      coupons,
		accountUsage: make(map[string]int),
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

func (m *mockCouponRepository) RecordUsage(_ context.Context, _ CouponID, _ shared.ContractID) error {
	if m.recordUsageErr != nil {
		return m.recordUsageErr
	}
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

func newTestCoupon(id CouponID, code string, ct CouponType, value *big.Rat, applicableTo []string) *Coupon {
	return NewCoupon(
		id, code, ct, value, shared.CurrencyJPY,
		nil, nil,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		nil, 0, applicableTo,
	)
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

func TestCouponPlugin_ApplicableToPlan_Match(t *testing.T) {
	// Coupon restricted to plan "plan-gold"
	coupon := newTestCoupon("c1", "GOLD10", CouponTypePercentage, big.NewRat(10, 100), []string{"plan-gold"})
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	// Create a contract with matching planID
	agg := createTestAggregate(t, "acc-1", "plan-gold")
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

func TestCouponPlugin_ApplicableToPlan_NoMatch(t *testing.T) {
	// Coupon restricted to plan "plan-gold", but contract has "plan-silver"
	coupon := newTestCoupon("c1", "GOLD10", CouponTypePercentage, big.NewRat(10, 100), []string{"plan-gold"})
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "acc-1", "plan-silver")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	discount, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !discount.IsZero() {
		t.Errorf("expected zero discount for non-matching plan, got %s", discount.Amount().RatString())
	}
}

func TestCouponPlugin_AccountBlocklist(t *testing.T) {
	coupon := newTestCoupon("c1", "SAVE10", CouponTypePercentage, big.NewRat(10, 100), nil)
	coupon.WithBlockedAccountIDs([]shared.AccountID{"blocked-acc"})
	repo := newMockRepo(coupon)
	p := NewCouponPlugin(repo, testClock)

	agg := createTestAggregate(t, "blocked-acc", "plan-1")
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
	agg := createTestAggregate(t, "regular-acc", "plan-1")
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
	vipAgg := createTestAggregate(t, "vip-acc", "plan-1")
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

	agg := createTestAggregate(t, "acc-1", "plan-1")
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

	agg := createTestAggregate(t, "acc-1", "plan-1")
	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContextWithContract(subtotal, agg)

	_, err := p.CalculateDiscount(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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

	agg := createTestAggregate(t, "acc-1", "plan-1")
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

	// Verify redemption was saved with the unique code
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

	agg := createTestAggregate(t, "acc-1", "plan-1") // subscription type
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

	agg := createTestAggregateWithType(t, "acc-1", "plan-1", contract.ContractTypeOneTime)
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

	subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
	ctx := newTestContext(subtotal)

	_, err := p.CalculateDiscount(ctx)
	if err == nil {
		t.Fatal("expected error from SaveRedemption, got nil")
	}

	// Usage should NOT have been recorded since redemption failed first
	if repo.recordUsageCalled != 0 {
		t.Errorf("expected 0 usage recordings when redemption fails, got %d", repo.recordUsageCalled)
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
func createTestAggregate(t *testing.T, accountID shared.AccountID, planID shared.PlanID) *contract.ContractAggregate {
	t.Helper()
	return createTestAggregateWithType(t, accountID, planID, contract.ContractTypeSubscription)
}

// createTestAggregateWithType creates a ContractAggregate with a specific contract type.
func createTestAggregateWithType(t *testing.T, accountID shared.AccountID, planID shared.PlanID, ct contract.ContractType) *contract.ContractAggregate {
	t.Helper()
	contractID := shared.NewContractID()
	agg := contract.NewContractAggregate(contractID, testClock)
	cmd := contract.CreateContractCommand{
		IdempotencyKey: shared.GenerateID(),
		AccountID:      accountID,
		PlanID:         planID,
		ContractType:   ct,
		BillingCycle:   contract.BillingCycleMonthly,
		Price:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		BasePrice:      shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
	}
	if err := agg.Create(cmd, eventstore.EventMetadata{UserID: "test"}); err != nil {
		t.Fatalf("failed to create contract aggregate: %v", err)
	}
	return agg
}
