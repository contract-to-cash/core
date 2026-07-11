package integration

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"testing"

	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
	"github.com/contract-to-cash/core/plugins/coupon"
)

// These tests exercise issue #185: coupon redemption must not be persisted as a
// side effect of the discount CALCULATION hook (which runs before the billing
// transaction). Redemptions are confirmed idempotently in AfterCalculation,
// keyed by (coupon, contract, billing period), so:
//
//	(i)   a rolled-back pipeline never permanently consumes a use,
//	(ii)  a retry / RegenerateInvoice for the same period consumes exactly one,
//	(iii) concurrent confirmations of the same key collapse to one redemption.

// --- Idempotent in-memory coupon repository ---

type integrationCouponRepo struct {
	mu          sync.Mutex
	coupons     []*coupon.Coupon
	redemptions map[string]*coupon.Redemption // IdempotencyKey -> redemption
}

func newIntegrationCouponRepo() *integrationCouponRepo {
	return &integrationCouponRepo{redemptions: make(map[string]*coupon.Redemption)}
}

func (r *integrationCouponRepo) add(c *coupon.Coupon) { r.coupons = append(r.coupons, c) }

func (r *integrationCouponRepo) FindByCode(_ context.Context, code string) (*coupon.Coupon, error) {
	for _, c := range r.coupons {
		if c.Code() == code {
			return c, nil
		}
	}
	return nil, nil
}

func (r *integrationCouponRepo) FindApplicable(_ context.Context, _ coupon.CouponQuery) ([]*coupon.Coupon, error) {
	return r.coupons, nil
}

func (r *integrationCouponRepo) Save(_ context.Context, _ *coupon.Coupon) error { return nil }

// SaveRedemption deduplicates on (coupon, contract, billing period).
func (r *integrationCouponRepo) SaveRedemption(_ context.Context, redemption *coupon.Redemption) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := redemption.IdempotencyKey()
	if _, exists := r.redemptions[key]; exists {
		return nil
	}
	r.redemptions[key] = redemption
	return nil
}

func (r *integrationCouponRepo) FindRedemptions(_ context.Context, couponID coupon.CouponID, accountID *shared.AccountID) ([]*coupon.Redemption, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*coupon.Redemption
	for _, rd := range r.redemptions {
		if rd.CouponID() != couponID {
			continue
		}
		if accountID != nil && rd.AccountID() != *accountID {
			continue
		}
		result = append(result, rd)
	}
	return result, nil
}

func (r *integrationCouponRepo) countFor(couponID coupon.CouponID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, rd := range r.redemptions {
		if rd.CouponID() == couponID {
			n++
		}
	}
	return n
}

// --- Invoice repository that fails Save a fixed number of times ---

type failNTimesInvoiceRepo struct {
	invoice.Repository
	remainingFailures int
}

func (r *failNTimesInvoiceRepo) Save(ctx context.Context, inv *invoice.Invoice) error {
	if r.remainingFailures > 0 {
		r.remainingFailures--
		return fmt.Errorf("injected invoice save failure")
	}
	return r.Repository.Save(ctx, inv)
}

// --- Test setup helper ---

func newCouponBillingService(
	t *testing.T,
	clock shared.Clock,
	invoiceRepo invoice.Repository,
	couponRepo coupon.CouponRepository,
) (*service.BillingService, *inmemory.InMemoryContractRepository, *inmemory.InMemoryPriceRepository) {
	t.Helper()

	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
	usageRepo := inmemory.NewInMemoryUsageRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()

	registry := plugin.NewRegistry()
	if err := registry.Register(coupon.NewCouponPlugin(couponRepo, clock)); err != nil {
		t.Fatalf("register coupon plugin: %v", err)
	}

	svc := service.NewBillingService(
		contractRepo,
		invoiceRepo,
		usageRepo,
		balance.BalanceConfig{},
		priceRepo, productRepo,
		registry,
		service.BillingConfig{DaysUntilDue: 30},
		clock,
		service.WithBalanceRepo(balanceRepo),
	)
	return svc, contractRepo, priceRepo
}

// tenPercentCoupon builds a 10% coupon valid around the clock, with a global
// usage limit and a per-account usage limit of 1 (the strictest gate — used to
// prove a retry is not denied by its own rolled-back redemption).
func tenPercentCoupon(clock shared.Clock) *coupon.Coupon {
	usageLimit := 5
	now := clock.Now()
	return coupon.NewCoupon(
		"cpn-185", "SAVE10", coupon.CouponTypePercentage,
		big.NewRat(10, 100), shared.CurrencyJPY,
		nil, nil,
		now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0),
		&usageLimit, 0, nil,
	).WithPerAccountUsageLimit(1)
}

// Invariant (i) + (ii): a billing pipeline that fails after the discount hook
// (invoice save injected to fail once) must NOT consume a coupon use; the retry
// for the same period succeeds, still receives the discount, and consumes
// exactly one use.
func TestCouponRedemption_PipelineFailureThenRetry_ConsumesExactlyOneUse(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	invoiceRepo := &failNTimesInvoiceRepo{
		Repository:        inmemory.NewInMemoryInvoiceRepository(clock),
		remainingFailures: 1,
	}
	couponRepo := newIntegrationCouponRepo()
	couponRepo.add(tenPercentCoupon(clock))

	svc, contractRepo, priceRepo := newCouponBillingService(t, clock, invoiceRepo, couponRepo)
	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, moneyJPY(10000))

	// First attempt: the invoice save fails and the transaction rolls back.
	if _, err := svc.GenerateInvoice(ctx, agg.ContractID(), agg.CurrentPeriod()); err == nil {
		t.Fatal("expected first GenerateInvoice to fail (injected save failure)")
	}

	// Retry for the SAME period must succeed and still apply the discount — it
	// must not be denied by the per-account limit that its own rolled-back
	// redemption would otherwise trip.
	inv, err := svc.GenerateInvoice(ctx, agg.ContractID(), agg.CurrentPeriod())
	if err != nil {
		t.Fatalf("retry GenerateInvoice failed: %v", err)
	}
	assertMoneyEquals(t, "discount", inv.DiscountAmount(), 1000) // 10% of 10000

	if got := couponRepo.countFor("cpn-185"); got != 1 {
		t.Fatalf("expected exactly 1 redemption after failure+retry, got %d", got)
	}
}

// Invariant (ii): RegenerateInvoice (void-and-recreate) for the same period must
// not double-redeem — the idempotency key is (coupon, contract, period).
func TestCouponRedemption_RegenerateSamePeriod_NoDoubleRedeem(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	baseRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	couponRepo := newIntegrationCouponRepo()
	couponRepo.add(tenPercentCoupon(clock))

	svc, contractRepo, priceRepo := newCouponBillingService(t, clock, baseRepo, couponRepo)
	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, moneyJPY(10000))
	period := agg.CurrentPeriod()

	// Generate and confirm one redemption.
	inv, err := svc.GenerateInvoice(ctx, agg.ContractID(), period)
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}
	if got := couponRepo.countFor("cpn-185"); got != 1 {
		t.Fatalf("expected 1 redemption after initial generate, got %d", got)
	}

	// Void the invoice so RegenerateInvoice is permitted for the period.
	if err := inv.Void(); err != nil {
		t.Fatalf("void invoice: %v", err)
	}
	if err := baseRepo.Save(ctx, inv); err != nil {
		t.Fatalf("save voided invoice: %v", err)
	}

	// Regenerate for the same period: the coupon discount applies again but the
	// redemption is the SAME (coupon, contract, period) key — no extra use.
	regen, err := svc.RegenerateInvoice(ctx, agg.ContractID(), period)
	if err != nil {
		t.Fatalf("RegenerateInvoice failed: %v", err)
	}
	assertMoneyEquals(t, "regenerated discount", regen.DiscountAmount(), 1000)

	if got := couponRepo.countFor("cpn-185"); got != 1 {
		t.Fatalf("expected still exactly 1 redemption after regenerate, got %d", got)
	}
}

// Happy path: a single successful generate confirms exactly one redemption,
// keyed by the invoice's billing period.
func TestCouponRedemption_SuccessfulGenerate_RecordsOneRedemption(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()

	couponRepo := newIntegrationCouponRepo()
	couponRepo.add(tenPercentCoupon(clock))

	svc, contractRepo, priceRepo := newCouponBillingService(
		t, clock, inmemory.NewInMemoryInvoiceRepository(clock), couponRepo,
	)
	agg := createActiveContractWithPrice(t, ctx, clock, contractRepo, priceRepo, moneyJPY(10000))
	period := agg.CurrentPeriod()

	inv, err := svc.GenerateInvoice(ctx, agg.ContractID(), period)
	if err != nil {
		t.Fatalf("GenerateInvoice failed: %v", err)
	}
	assertMoneyEquals(t, "discount", inv.DiscountAmount(), 1000)

	reds, err := couponRepo.FindRedemptions(ctx, "cpn-185", nil)
	if err != nil {
		t.Fatalf("FindRedemptions: %v", err)
	}
	if len(reds) != 1 {
		t.Fatalf("expected 1 redemption, got %d", len(reds))
	}
	if !reds[0].BillingPeriod().Equals(period) {
		t.Errorf("redemption billing period = %s, want %s", reds[0].BillingPeriod(), period)
	}
	if reds[0].InvoiceID() != inv.ID() {
		t.Errorf("redemption invoiceID = %s, want %s", reds[0].InvoiceID(), inv.ID())
	}
}
