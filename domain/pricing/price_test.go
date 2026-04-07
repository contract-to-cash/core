package pricing

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func jpy(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func TestNewPrice_CreatedAtIsSetFromParameter(t *testing.T) {
	fixedTime := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	p := NewPrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil, fixedTime)

	if !p.CreatedAt().Equal(fixedTime) {
		t.Errorf("expected createdAt %v, got %v", fixedTime, p.CreatedAt())
	}
}

func TestNewPrice(t *testing.T) {
	productID := shared.NewProductID()
	createdAt := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	p := NewPrice(productID, jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil, createdAt)

	if p.ID() == "" {
		t.Error("expected non-empty price ID")
	}
	if p.ProductID() != productID {
		t.Error("expected matching product ID")
	}
	if p.Amount().Amount().Cmp(new(big.Rat).SetInt64(1000)) != 0 {
		t.Errorf("expected amount 1000, got %v", p.Amount().Amount())
	}
	if p.Currency() != shared.CurrencyJPY {
		t.Errorf("expected currency JPY, got %s", p.Currency())
	}
	if p.BillingCycle() != BillingCycleMonthly {
		t.Errorf("expected billing cycle monthly, got %s", p.BillingCycle())
	}
	if p.Status() != PriceStatusActive {
		t.Errorf("expected status active, got %s", p.Status())
	}
	if p.CreatedAt().IsZero() {
		t.Error("expected non-zero createdAt")
	}
}

func TestPrice_WithPricingModel(t *testing.T) {
	productID := shared.NewProductID()
	model := FlatPrice{Price: jpy(500)}
	p := NewPrice(productID, jpy(0), shared.CurrencyJPY, BillingCycleMonthly, model, time.Now())

	if p.PricingModel() == nil {
		t.Error("expected non-nil pricing model")
	}
	result := p.PricingModel().CalculatePrice(100)
	if result.Amount().Cmp(new(big.Rat).SetInt64(500)) != 0 {
		t.Errorf("expected pricing model to return 500, got %v", result.Amount())
	}
}

func TestPrice_Archive(t *testing.T) {
	p := NewPrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil, time.Now())

	if err := p.Archive(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PriceStatusArchived {
		t.Errorf("expected status archived, got %s", p.Status())
	}
}

func TestPrice_Archive_AlreadyArchived(t *testing.T) {
	p := NewPrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil, time.Now())
	_ = p.Archive()

	err := p.Archive()
	if err == nil {
		t.Error("expected error when archiving already archived price")
	}
}

func TestPrice_Immutability(t *testing.T) {
	productID := shared.NewProductID()
	amount := jpy(1000)
	p := NewPrice(productID, amount, shared.CurrencyJPY, BillingCycleMonthly, nil, time.Now())

	originalID := p.ID()
	originalProductID := p.ProductID()
	originalAmount := p.Amount()
	originalCurrency := p.Currency()
	originalCycle := p.BillingCycle()

	// After archiving, only status should change — all other fields remain the same
	_ = p.Archive()

	if p.ID() != originalID {
		t.Error("ID should not change after archive")
	}
	if p.ProductID() != originalProductID {
		t.Error("ProductID should not change after archive")
	}
	if p.Amount().Amount().Cmp(originalAmount.Amount()) != 0 {
		t.Error("Amount should not change after archive")
	}
	if p.Currency() != originalCurrency {
		t.Error("Currency should not change after archive")
	}
	if p.BillingCycle() != originalCycle {
		t.Error("BillingCycle should not change after archive")
	}
}

func TestPrice_DifferentBillingCycles(t *testing.T) {
	cycles := []BillingCycle{BillingCycleDaily, BillingCycleWeekly, BillingCycleMonthly, BillingCycleYearly}
	for _, cycle := range cycles {
		p := NewPrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, cycle, nil, time.Now())
		if p.BillingCycle() != cycle {
			t.Errorf("expected billing cycle %s, got %s", cycle, p.BillingCycle())
		}
	}
}

func TestNewPriceWithInterval(t *testing.T) {
	productID := shared.NewProductID()
	createdAt := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)

	t.Run("quarterly", func(t *testing.T) {
		p := NewPriceWithInterval(productID, jpy(3000), shared.CurrencyJPY, Quarterly(), nil, createdAt)
		if p.Interval().Unit() != IntervalUnitMonth {
			t.Errorf("expected unit month, got %s", p.Interval().Unit())
		}
		if p.Interval().Count() != 3 {
			t.Errorf("expected count 3, got %d", p.Interval().Count())
		}
		// Quarterly has no exact BillingCycle match
		if p.BillingCycle() != "" {
			t.Errorf("expected empty billing cycle for quarterly, got %s", p.BillingCycle())
		}
	})

	t.Run("monthly via interval", func(t *testing.T) {
		p := NewPriceWithInterval(productID, jpy(1000), shared.CurrencyJPY, Monthly(), nil, createdAt)
		if p.BillingCycle() != BillingCycleMonthly {
			t.Errorf("expected monthly billing cycle, got %s", p.BillingCycle())
		}
	})

	t.Run("backward compat: NewPrice stores interval", func(t *testing.T) {
		p := NewPrice(productID, jpy(1000), shared.CurrencyJPY, BillingCycleYearly, nil, createdAt)
		if !p.Interval().Equals(Yearly()) {
			t.Errorf("expected yearly interval, got %v", p.Interval())
		}
	})
}
