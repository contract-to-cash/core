package service

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/product"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/domain/usage"
	"github.com/contract-to-cash/core/plugin"
)

// Regression tests for the persisted-invalid-price panic (review round 1):
// pricing constructors validate model invariants (issues #148/#156/#238), and
// CalculatePrice panics when a model bypasses them — but pricing.FromSnapshot
// reconstructs a historically stored model AS-IS (replay safety), so a price
// persisted before validation existed reaches the billing pipeline invalid.
// safeCalculatePrice must convert the first-use panic into a per-contract
// DomainError instead of letting it crash the integrator's scheduler goroutine.

// newPoisonedPriceBillingService wires a usage-based contract whose Price
// carries the given (possibly invalid, struct-literal — simulating
// FromSnapshot reconstruction) pricing model, with non-zero billable usage so
// CalculatePrice is actually consulted.
func newPoisonedPriceBillingService(t *testing.T, model pricing.PricingModel) (*BillingService, *contract.ContractAggregate, *pricing.Price) {
	t.Helper()
	clock := newTestClock()

	prod := product.NewProduct("API Access", "API usage product", clock.Now())
	prod.AddUsageMetric(product.UsageMetric{Name: "api_calls", IncludedQuantity: 0})

	priceEntity := newTestPrice(prod.ID(), jpy(1000), model)
	agg := newTestContractAggregateWithPriceID(clock, contract.ContractTypeUsageBased, jpy(1000), priceEntity.ID())

	usageRepo := &mockUsageRepoWithMetrics{
		summaries: map[shared.MetricName]*usage.UsageSummary{
			"api_calls": {TotalUsage: 100},
		},
	}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		usageRepo,
		balance.BalanceConfig{},
		&mockPriceRepo{price: priceEntity},
		&mockProductRepo{product: prod},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)
	return svc, agg, priceEntity
}

func assertPoisonedPriceDomainError(t *testing.T, err error, priceID shared.PriceID) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a DomainError for the invalid persisted price, got nil")
	}
	var de *shared.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected *shared.DomainError, got %T: %v", err, err)
	}
	if de.Code != shared.ErrCodeBusinessRule {
		t.Errorf("expected code %s, got %s (%v)", shared.ErrCodeBusinessRule, de.Code, err)
	}
	if !strings.Contains(de.Message, string(priceID)) {
		t.Errorf("error must name the offending price %s, got: %s", priceID, de.Message)
	}
	if !strings.Contains(de.Message, "Validate()") {
		t.Errorf("error should advise running Validate(), got: %s", de.Message)
	}
}

// A UsagePrice with a wrong-currency Maximum (previously billed silently
// without the cap; post-#238 CalculatePrice panics on first use).
func TestGenerateInvoice_InvalidPersistedUsagePrice_ReturnsDomainErrorNotPanic(t *testing.T) {
	badMax := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyUSD)
	model := pricing.UsagePrice{UnitPrice: jpy(10), Maximum: &badMax}

	svc, agg, priceEntity := newPoisonedPriceBillingService(t, model)

	// Any panic escaping here fails the test run — the point of the guard.
	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if inv != nil {
		t.Errorf("expected no invoice for the failed billing run, got %v", inv.ID())
	}
	assertPoisonedPriceDomainError(t, err, priceEntity.ID())
}

// A TieredPrice with unsorted tiers (previously produced a wrong — even
// negative — graduated charge; post-#238 CalculatePrice panics on first use).
func TestGenerateInvoice_InvalidPersistedTieredPrice_ReturnsDomainErrorNotPanic(t *testing.T) {
	model := pricing.TieredPrice{
		Mode: pricing.TieredPricingGraduated,
		Tiers: []pricing.PriceTier{
			{UpTo: 200, UnitPrice: jpy(10), FlatFee: jpy(0)},
			{UpTo: 100, UnitPrice: jpy(5), FlatFee: jpy(0)},
		},
	}

	svc, agg, priceEntity := newPoisonedPriceBillingService(t, model)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if inv != nil {
		t.Errorf("expected no invoice for the failed billing run, got %v", inv.ID())
	}
	assertPoisonedPriceDomainError(t, err, priceEntity.ID())
}

// A VALID model must pass through safeCalculatePrice unchanged (no recover
// interference with the normal path).
func TestGenerateInvoice_ValidUsagePrice_UnaffectedByPanicGuard(t *testing.T) {
	model := pricing.UsagePrice{UnitPrice: jpy(10)}

	svc, agg, _ := newPoisonedPriceBillingService(t, model)

	inv, err := svc.GenerateInvoice(context.Background(), agg.ContractID(), currentPeriodOf(agg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// base 1000 + 100 usage × 10 = 2000
	if inv.Subtotal().Amount().Cmp(new(big.Rat).SetInt64(2000)) != 0 {
		t.Errorf("expected subtotal 2000, got %s", inv.Subtotal().Amount().RatString())
	}
}
