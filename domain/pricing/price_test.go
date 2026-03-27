package pricing

import (
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

func jpy(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func TestNewPrice(t *testing.T) {
	productID := shared.NewProductID()
	p := NewPrice(productID, jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil)

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
	p := NewPrice(productID, jpy(0), shared.CurrencyJPY, BillingCycleMonthly, model)

	if p.PricingModel() == nil {
		t.Error("expected non-nil pricing model")
	}
	result := p.PricingModel().CalculatePrice(100)
	if result.Amount().Cmp(new(big.Rat).SetInt64(500)) != 0 {
		t.Errorf("expected pricing model to return 500, got %v", result.Amount())
	}
}

func TestPrice_Archive(t *testing.T) {
	p := NewPrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil)

	if err := p.Archive(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PriceStatusArchived {
		t.Errorf("expected status archived, got %s", p.Status())
	}
}

func TestPrice_Archive_AlreadyArchived(t *testing.T) {
	p := NewPrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil)
	_ = p.Archive()

	err := p.Archive()
	if err == nil {
		t.Error("expected error when archiving already archived price")
	}
}

func TestPrice_Immutability(t *testing.T) {
	productID := shared.NewProductID()
	amount := jpy(1000)
	p := NewPrice(productID, amount, shared.CurrencyJPY, BillingCycleMonthly, nil)

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
		p := NewPrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, cycle, nil)
		if p.BillingCycle() != cycle {
			t.Errorf("expected billing cycle %s, got %s", cycle, p.BillingCycle())
		}
	}
}
