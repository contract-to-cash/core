package service

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

func benchClock() shared.FixedClock {
	return shared.FixedClock{FixedTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}
}

func benchMoney(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func benchBillingService(agg *contract.ContractAggregate, priceEntity *pricing.Price, clock shared.Clock, opts ...BillingServiceOption) *BillingService {
	return NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
		opts...,
	)
}

func BenchmarkGenerateInvoice_Subscription(b *testing.B) {
	b.ReportAllocs()

	clock := benchClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, benchMoney(9800))
	svc := benchBillingService(agg, priceEntity, clock)
	period := currentPeriodOf(agg)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.GenerateInvoice(ctx, agg.ContractID(), period)
	}
}

func BenchmarkGenerateInvoice_WithPlugins(b *testing.B) {
	b.ReportAllocs()

	clock := benchClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, benchMoney(9800))

	// Register 3 discount plugins
	reg := plugin.NewRegistry()
	for i := 0; i < 3; i++ {
		_ = reg.Register(&benchDiscountPlugin{
			pluginName: "bench-discount-" + string(rune('A'+i)),
			discount:   benchMoney(100),
		})
	}

	svc := NewBillingService(
		&mockContractRepo{agg: agg},
		&mockInvoiceRepo{},
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		reg,
		BillingConfig{DaysUntilDue: 30},
		clock,
	)
	period := currentPeriodOf(agg)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.GenerateInvoice(ctx, agg.ContractID(), period)
	}
}

func BenchmarkGenerateInvoice_WithCredits(b *testing.B) {
	b.ReportAllocs()

	clock := benchClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, benchMoney(9800))

	// Prepare 10 credit entries
	credits := make([]*balance.BalanceEntry, 10)
	for i := range credits {
		credits[i] = balance.NewBalanceEntry(
			agg.AccountID(),
			benchMoney(500),
			balance.BalanceReasonManualAdjustment,
			clock.Now(),
		)
	}

	svc := benchBillingService(agg, priceEntity, clock,
		WithBalanceRepo(&mockBalanceRepo{credits: credits}),
	)
	period := currentPeriodOf(agg)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.GenerateInvoice(ctx, agg.ContractID(), period)
	}
}

// benchDiscountPlugin is a minimal DiscountHook for benchmarking.
type benchDiscountPlugin struct {
	pluginName string
	discount   shared.Money
}

func (p *benchDiscountPlugin) Name() string                                        { return p.pluginName }
func (p *benchDiscountPlugin) Version() string                                     { return "1.0.0" }
func (p *benchDiscountPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *benchDiscountPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *benchDiscountPlugin) Priority() int                                       { return 500 }
func (p *benchDiscountPlugin) CalculateDiscount(_ *plugin.CalculationContext) (shared.Money, error) {
	return p.discount, nil
}

var _ plugin.DiscountHook = (*benchDiscountPlugin)(nil)
