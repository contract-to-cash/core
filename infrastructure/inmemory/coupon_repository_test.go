package inmemory

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugins/coupon"
)

var couponTestNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

func newTestCoupon(t *testing.T, id coupon.CouponID, code string, usageLimit *int, usedCount int, applicableTo []shared.ProductID) *coupon.Coupon {
	t.Helper()
	c, err := coupon.NewCoupon(
		id, code, coupon.CouponTypePercentage, big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		couponTestNow.AddDate(0, -1, 0), couponTestNow.AddDate(0, 1, 0),
		usageLimit, usedCount, applicableTo,
	)
	if err != nil {
		t.Fatalf("new coupon: %v", err)
	}
	return c
}

func newTestPeriod(t *testing.T, monthOffset int) shared.DateRange {
	t.Helper()
	start := time.Date(2026, time.Month(1+monthOffset), 1, 0, 0, 0, 0, time.UTC)
	period, err := shared.NewDateRange(start, start.AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("new date range: %v", err)
	}
	return period
}

func newTestRedemption(t *testing.T, couponID coupon.CouponID, accountID shared.AccountID, contractID shared.ContractID, period shared.DateRange) *coupon.Redemption {
	t.Helper()
	return coupon.NewRedemption(
		coupon.RedemptionID(shared.GenerateID()),
		couponID, "CODE", coupon.CodeTypeShared,
		accountID, contractID, period,
		shared.NewInvoiceID(), couponTestNow,
	)
}

func TestInMemoryCouponRepository_FindByCode(t *testing.T) {
	repo := NewInMemoryCouponRepository()
	ctx := context.Background()

	c := newTestCoupon(t, "c-1", "SAVE10", nil, 0, nil)
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.FindByCode(ctx, "SAVE10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID() != "c-1" {
		t.Errorf("expected coupon c-1, got %v", got)
	}

	// Contract: not found is (nil, nil), not an error.
	got, err = repo.FindByCode(ctx, "NOPE")
	if err != nil {
		t.Fatalf("expected nil error for missing code, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil coupon for missing code, got %v", got)
	}
}

func TestInMemoryCouponRepository_FindApplicable(t *testing.T) {
	repo := NewInMemoryCouponRepository()
	ctx := context.Background()

	productA := shared.NewProductID()
	productB := shared.NewProductID()
	accountAllowed := shared.AccountID("acct-allowed")
	accountBlocked := shared.AccountID("acct-blocked")

	valid := newTestCoupon(t, "c-valid", "VALID", nil, 0, nil)

	expired, err := coupon.NewCoupon(
		"c-expired", "EXPIRED", coupon.CouponTypePercentage, big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		couponTestNow.AddDate(0, -2, 0), couponTestNow.AddDate(0, -1, 0),
		nil, 0, nil,
	)
	if err != nil {
		t.Fatalf("new expired coupon: %v", err)
	}

	limit := 5
	exhausted := newTestCoupon(t, "c-exhausted", "EXHAUSTED", &limit, 5, nil) // usedCount baseline >= limit

	productOnly := newTestCoupon(t, "c-product", "PRODUCT-A", nil, 0, []shared.ProductID{productA})

	blocked := newTestCoupon(t, "c-blocked", "BLOCKED", nil, 0, nil).
		WithBlockedAccountIDs([]shared.AccountID{accountBlocked})

	allowlisted := newTestCoupon(t, "c-allowlist", "ALLOWLIST", nil, 0, nil).
		WithAllowedAccountIDs([]shared.AccountID{accountAllowed})

	for _, c := range []*coupon.Coupon{valid, expired, exhausted, productOnly, blocked, allowlisted} {
		if err := repo.Save(ctx, c); err != nil {
			t.Fatalf("save %s: %v", c.ID(), err)
		}
	}

	t.Run("validity window and baseline usage limit", func(t *testing.T) {
		got, err := repo.FindApplicable(ctx, coupon.CouponQuery{
			AccountID: accountBlocked, // blocked coupon excluded too
			ProductID: productB,       // product-restricted coupon excluded too
			At:        couponTestNow,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Only "valid" survives: expired (window), exhausted (baseline >= limit),
		// productOnly (product B), blocked (blocklist), allowlisted (not in list).
		if len(got) != 1 || got[0].ID() != "c-valid" {
			t.Errorf("expected only c-valid, got %d coupons: %v", len(got), couponIDs(got))
		}
	})

	t.Run("product and allowlist match", func(t *testing.T) {
		got, err := repo.FindApplicable(ctx, coupon.CouponQuery{
			AccountID: accountAllowed,
			ProductID: productA,
			At:        couponTestNow,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// valid, productOnly (product A matches), blocked (account not blocked),
		// allowlisted (account in allowlist) — in Save insertion order.
		want := []coupon.CouponID{"c-valid", "c-product", "c-blocked", "c-allowlist"}
		ids := couponIDs(got)
		if len(ids) != len(want) {
			t.Fatalf("expected %v, got %v", want, ids)
		}
		for i := range want {
			if ids[i] != want[i] {
				t.Fatalf("expected deterministic insertion order %v, got %v", want, ids)
			}
		}
	})
}

func couponIDs(coupons []*coupon.Coupon) []coupon.CouponID {
	ids := make([]coupon.CouponID, 0, len(coupons))
	for _, c := range coupons {
		ids = append(ids, c.ID())
	}
	return ids
}

// TestInMemoryCouponRepository_SaveRedemption_IdempotentReplay verifies §6.3
// invariant (ii): a replay of the same (coupon, contract, billingPeriod) key is
// a no-op that is NOT counted as a new use — even when the coupon is at its
// usage limit, re-confirming an already-recorded redemption succeeds.
func TestInMemoryCouponRepository_SaveRedemption_IdempotentReplay(t *testing.T) {
	repo := NewInMemoryCouponRepository()
	ctx := context.Background()

	couponID := coupon.CouponID("c-1")
	accountID := shared.AccountID("acct-1")
	contractID := shared.NewContractID()
	period := newTestPeriod(t, 0)

	limit := 1
	limits := coupon.RedemptionLimits{GlobalLimit: &limit}

	first := newTestRedemption(t, couponID, accountID, contractID, period)
	if err := repo.SaveRedemption(ctx, first, limits); err != nil {
		t.Fatalf("first SaveRedemption: %v", err)
	}

	// Replay with the same key (distinct Redemption object / ID / invoice):
	// must be a no-op nil even though the global limit (1) is now reached.
	replay := newTestRedemption(t, couponID, accountID, contractID, period)
	if err := repo.SaveRedemption(ctx, replay, limits); err != nil {
		t.Fatalf("idempotent replay must return nil, got %v", err)
	}

	rows, err := repo.FindRedemptions(ctx, couponID, nil)
	if err != nil {
		t.Fatalf("find redemptions: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected exactly 1 redemption after replay, got %d", len(rows))
	}

	// A DISTINCT key must now be rejected by the limit.
	other := newTestRedemption(t, couponID, accountID, shared.NewContractID(), period)
	err = repo.SaveRedemption(ctx, other, limits)
	if !errors.Is(err, coupon.ErrUsageLimitReached) {
		t.Errorf("expected ErrUsageLimitReached for distinct key at limit, got %v", err)
	}
}

// TestInMemoryCouponRepository_SaveRedemption_ConcurrentSameKey verifies §6.3
// invariant (iii): concurrent confirmations of the same idempotency key
// collapse to exactly one redemption, and none of them errors.
func TestInMemoryCouponRepository_SaveRedemption_ConcurrentSameKey(t *testing.T) {
	repo := NewInMemoryCouponRepository()
	ctx := context.Background()

	couponID := coupon.CouponID("c-1")
	accountID := shared.AccountID("acct-1")
	contractID := shared.NewContractID()
	period := newTestPeriod(t, 0)

	limit := 1
	limits := coupon.RedemptionLimits{GlobalLimit: &limit}

	const n = 50
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := newTestRedemption(t, couponID, accountID, contractID, period)
			errs[i] = repo.SaveRedemption(ctx, r, limits)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: same-key confirmation must not error, got %v", i, err)
		}
	}
	rows, err := repo.FindRedemptions(ctx, couponID, nil)
	if err != nil {
		t.Fatalf("find redemptions: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected same-key concurrency to collapse to 1 redemption, got %d", len(rows))
	}
}

// TestInMemoryCouponRepository_SaveRedemption_ConcurrentDistinctKeysRespectGlobalLimit
// verifies §6.3 invariant (iv) / issue #195: N concurrent confirmations with
// DISTINCT keys against a global limit L succeed exactly L times; the rest are
// rejected with ErrUsageLimitReached and nothing is over-inserted.
func TestInMemoryCouponRepository_SaveRedemption_ConcurrentDistinctKeysRespectGlobalLimit(t *testing.T) {
	repo := NewInMemoryCouponRepository()
	ctx := context.Background()

	couponID := coupon.CouponID("c-promo")
	period := newTestPeriod(t, 0)

	const n = 50
	const l = 5
	limit := l
	limits := coupon.RedemptionLimits{GlobalLimit: &limit}

	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Distinct contract (and account) per goroutine => distinct keys.
			r := newTestRedemption(t, couponID,
				shared.AccountID(fmt.Sprintf("acct-%d", i)), shared.NewContractID(), period)
			errs[i] = repo.SaveRedemption(ctx, r, limits)
		}(i)
	}
	wg.Wait()

	succeeded, limited := 0, 0
	for i, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, coupon.ErrUsageLimitReached):
			limited++
		default:
			t.Errorf("goroutine %d: unexpected error %v", i, err)
		}
	}
	if succeeded != l {
		t.Errorf("expected exactly %d confirmations to succeed, got %d", l, succeeded)
	}
	if limited != n-l {
		t.Errorf("expected %d confirmations rejected with ErrUsageLimitReached, got %d", n-l, limited)
	}
	rows, err := repo.FindRedemptions(ctx, couponID, nil)
	if err != nil {
		t.Fatalf("find redemptions: %v", err)
	}
	if len(rows) != l {
		t.Errorf("expected exactly %d redemption rows, got %d", l, len(rows))
	}
}

// TestInMemoryCouponRepository_SaveRedemption_GlobalBaselineRespected verifies
// that the migration baseline (Coupon.UsedCount, passed as GlobalBaseline)
// counts toward the global limit: baseline + rows >= limit rejects the insert.
func TestInMemoryCouponRepository_SaveRedemption_GlobalBaselineRespected(t *testing.T) {
	repo := NewInMemoryCouponRepository()
	ctx := context.Background()

	couponID := coupon.CouponID("c-migrated")
	period := newTestPeriod(t, 0)

	limit := 41
	limits := coupon.RedemptionLimits{GlobalLimit: &limit, GlobalBaseline: 40}

	// 40 (baseline) + 0 (rows) < 41: first insert succeeds.
	first := newTestRedemption(t, couponID, "acct-1", shared.NewContractID(), period)
	if err := repo.SaveRedemption(ctx, first, limits); err != nil {
		t.Fatalf("first SaveRedemption under baseline: %v", err)
	}

	// 40 (baseline) + 1 (row) >= 41: second distinct key is rejected.
	second := newTestRedemption(t, couponID, "acct-2", shared.NewContractID(), period)
	err := repo.SaveRedemption(ctx, second, limits)
	if !errors.Is(err, coupon.ErrUsageLimitReached) {
		t.Errorf("expected ErrUsageLimitReached with baseline at limit, got %v", err)
	}
}

// TestInMemoryCouponRepository_SaveRedemption_PerAccountLimit verifies the
// per-account dimension: distinct keys for the SAME account are limited, while
// another account can still redeem.
func TestInMemoryCouponRepository_SaveRedemption_PerAccountLimit(t *testing.T) {
	repo := NewInMemoryCouponRepository()
	ctx := context.Background()

	couponID := coupon.CouponID("c-1")
	period := newTestPeriod(t, 0)
	accountA := shared.AccountID("acct-a")
	accountB := shared.AccountID("acct-b")

	perAccount := 1
	limits := coupon.RedemptionLimits{PerAccountLimit: &perAccount}

	if err := repo.SaveRedemption(ctx, newTestRedemption(t, couponID, accountA, shared.NewContractID(), period), limits); err != nil {
		t.Fatalf("first redemption for account A: %v", err)
	}

	// Same account, different contract (distinct key): per-account limit hit.
	err := repo.SaveRedemption(ctx, newTestRedemption(t, couponID, accountA, shared.NewContractID(), period), limits)
	if !errors.Is(err, coupon.ErrUsageLimitReached) {
		t.Errorf("expected ErrUsageLimitReached for account A second use, got %v", err)
	}

	// Different account: unaffected.
	if err := repo.SaveRedemption(ctx, newTestRedemption(t, couponID, accountB, shared.NewContractID(), period), limits); err != nil {
		t.Errorf("account B redemption must succeed, got %v", err)
	}
}

func TestInMemoryCouponRepository_FindRedemptions_AccountFilter(t *testing.T) {
	repo := NewInMemoryCouponRepository()
	ctx := context.Background()

	couponID := coupon.CouponID("c-1")
	otherCouponID := coupon.CouponID("c-2")
	accountA := shared.AccountID("acct-a")
	accountB := shared.AccountID("acct-b")

	noLimits := coupon.RedemptionLimits{}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("save redemption: %v", err)
		}
	}
	must(repo.SaveRedemption(ctx, newTestRedemption(t, couponID, accountA, shared.NewContractID(), newTestPeriod(t, 0)), noLimits))
	must(repo.SaveRedemption(ctx, newTestRedemption(t, couponID, accountA, shared.NewContractID(), newTestPeriod(t, 1)), noLimits))
	must(repo.SaveRedemption(ctx, newTestRedemption(t, couponID, accountB, shared.NewContractID(), newTestPeriod(t, 0)), noLimits))
	must(repo.SaveRedemption(ctx, newTestRedemption(t, otherCouponID, accountA, shared.NewContractID(), newTestPeriod(t, 0)), noLimits))

	all, err := repo.FindRedemptions(ctx, couponID, nil)
	if err != nil {
		t.Fatalf("find all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 redemptions for coupon c-1, got %d", len(all))
	}

	onlyA, err := repo.FindRedemptions(ctx, couponID, &accountA)
	if err != nil {
		t.Fatalf("find by account: %v", err)
	}
	if len(onlyA) != 2 {
		t.Errorf("expected 2 redemptions for account A, got %d", len(onlyA))
	}
	for _, r := range onlyA {
		if r.AccountID() != accountA {
			t.Errorf("account filter leaked redemption for %s", r.AccountID())
		}
	}
}
