package pricing

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func jpy(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func usd(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyUSD)
}

// mustP unwraps a NewPrice / NewPriceWithInterval result, panicking on a
// construction error. It takes (p, err) directly so a two-value constructor
// call can be passed as its sole argument.
func mustP(p *Price, err error) *Price {
	if err != nil {
		panic(err)
	}
	return p
}

func TestNewPrice_CreatedAtIsSetFromParameter(t *testing.T) {
	fixedTime := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	p := mustP(NewPrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil, fixedTime))

	if !p.CreatedAt().Equal(fixedTime) {
		t.Errorf("expected createdAt %v, got %v", fixedTime, p.CreatedAt())
	}
}

func TestNewPrice(t *testing.T) {
	productID := shared.NewProductID()
	createdAt := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	p := mustP(NewPrice(productID, jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil, createdAt))

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
	p := mustP(NewPrice(productID, jpy(0), shared.CurrencyJPY, BillingCycleMonthly, model, time.Now()))

	if p.PricingModel() == nil {
		t.Error("expected non-nil pricing model")
	}
	result := p.PricingModel().CalculatePrice(100)
	if result.Amount().Cmp(new(big.Rat).SetInt64(500)) != 0 {
		t.Errorf("expected pricing model to return 500, got %v", result.Amount())
	}
}

func TestPrice_Archive(t *testing.T) {
	p := mustP(NewPrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil, time.Now()))

	if err := p.Archive(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PriceStatusArchived {
		t.Errorf("expected status archived, got %s", p.Status())
	}
}

func TestPrice_Archive_AlreadyArchived(t *testing.T) {
	p := mustP(NewPrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil, time.Now()))
	_ = p.Archive()

	err := p.Archive()
	if err == nil {
		t.Error("expected error when archiving already archived price")
	}
}

func TestPrice_Immutability(t *testing.T) {
	productID := shared.NewProductID()
	amount := jpy(1000)
	p := mustP(NewPrice(productID, amount, shared.CurrencyJPY, BillingCycleMonthly, nil, time.Now()))

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
		p := mustP(NewPrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, cycle, nil, time.Now()))
		if p.BillingCycle() != cycle {
			t.Errorf("expected billing cycle %s, got %s", cycle, p.BillingCycle())
		}
	}
}

func TestNewPriceWithInterval(t *testing.T) {
	productID := shared.NewProductID()
	createdAt := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)

	t.Run("quarterly", func(t *testing.T) {
		p := mustP(NewPriceWithInterval(productID, jpy(3000), shared.CurrencyJPY, Quarterly(), nil, createdAt))
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
		p := mustP(NewPriceWithInterval(productID, jpy(1000), shared.CurrencyJPY, Monthly(), nil, createdAt))
		if p.BillingCycle() != BillingCycleMonthly {
			t.Errorf("expected monthly billing cycle, got %s", p.BillingCycle())
		}
	})

	t.Run("backward compat: NewPrice stores interval", func(t *testing.T) {
		p := mustP(NewPrice(productID, jpy(1000), shared.CurrencyJPY, BillingCycleYearly, nil, createdAt))
		if !p.Interval().Equals(Yearly()) {
			t.Errorf("expected yearly interval, got %v", p.Interval())
		}
	})
}

// TestNewPrice_Validation exercises the construction-time invariants added in
// issue #196.
func TestNewPrice_Validation(t *testing.T) {
	pid := shared.NewProductID()
	now := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		run     func() (*Price, error)
		wantErr bool
		code    shared.ErrorCode
	}{
		{
			name: "valid",
			run: func() (*Price, error) {
				return NewPrice(pid, jpy(1000), shared.CurrencyJPY, BillingCycleMonthly, nil, now)
			},
			wantErr: false,
		},
		{
			name: "unknown billing cycle is rejected (strict, not silently Monthly)",
			run: func() (*Price, error) {
				return NewPrice(pid, jpy(1000), shared.CurrencyJPY, BillingCycle("fortnightly"), nil, now)
			},
			wantErr: true,
			code:    shared.ErrCodeValidation,
		},
		{
			name: "empty billing cycle is rejected",
			run: func() (*Price, error) {
				return NewPrice(pid, jpy(1000), shared.CurrencyJPY, BillingCycle(""), nil, now)
			},
			wantErr: true,
			code:    shared.ErrCodeValidation,
		},
		{
			name: "negative amount is rejected",
			run: func() (*Price, error) {
				return NewPrice(pid, jpy(-1), shared.CurrencyJPY, BillingCycleMonthly, nil, now)
			},
			wantErr: true,
			code:    shared.ErrCodeValidation,
		},
		{
			name: "amount currency mismatch is rejected",
			run: func() (*Price, error) {
				return NewPrice(pid, usd(1000), shared.CurrencyJPY, BillingCycleMonthly, nil, now)
			},
			wantErr: true,
			code:    shared.ErrCodeCurrencyMismatch,
		},
		{
			name: "zero amount does not trigger currency mismatch",
			run: func() (*Price, error) {
				return NewPrice(pid, shared.Zero(shared.CurrencyUSD), shared.CurrencyJPY, BillingCycleMonthly, nil, now)
			},
			wantErr: false,
		},
		{
			name: "zero interval is rejected",
			run: func() (*Price, error) {
				return NewPriceWithInterval(pid, jpy(1000), shared.CurrencyJPY, BillingInterval{}, nil, now)
			},
			wantErr: true,
			code:    shared.ErrCodeValidation,
		},
		{
			name: "quarterly interval is accepted",
			run: func() (*Price, error) {
				return NewPriceWithInterval(pid, jpy(3000), shared.CurrencyJPY, Quarterly(), nil, now)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := tt.run()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (price=%v)", p)
				}
				if p != nil {
					t.Errorf("expected nil price on error, got %v", p)
				}
				var de *shared.DomainError
				if !errors.As(err, &de) {
					t.Fatalf("expected DomainError, got %T", err)
				}
				if de.Code != tt.code {
					t.Errorf("expected code %s, got %s", tt.code, de.Code)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p == nil {
				t.Fatal("expected non-nil price")
			}
		})
	}
}

// TestPrice_PricingModel_ReturnsDefensiveCopy verifies that mutating the tiers of
// a returned TieredPrice model does not alter the Price's internal model or
// subsequent CalculatePrice results (issue #196).
func TestPrice_PricingModel_ReturnsDefensiveCopy(t *testing.T) {
	tiered, err := NewTieredPrice([]PriceTier{
		{UpTo: 10, UnitPrice: jpy(100), FlatFee: jpy(0)},
		{UpTo: 0, UnitPrice: jpy(50), FlatFee: jpy(0)},
	}, TieredPricingGraduated)
	if err != nil {
		t.Fatalf("NewTieredPrice failed: %v", err)
	}
	p := mustP(NewPrice(shared.NewProductID(), jpy(0), shared.CurrencyJPY, BillingCycleMonthly, tiered, time.Now()))

	before := p.PricingModel().CalculatePrice(20)

	// Mutate the backing array of a returned copy.
	leaked, ok := p.PricingModel().(TieredPrice)
	if !ok {
		t.Fatalf("expected TieredPrice, got %T", p.PricingModel())
	}
	leaked.Tiers[0].UnitPrice = jpy(999999)

	after := p.PricingModel().CalculatePrice(20)
	if before.Amount().Cmp(after.Amount()) != 0 {
		t.Errorf("mutating returned tiers changed CalculatePrice: before=%s after=%s",
			before.Amount().RatString(), after.Amount().RatString())
	}
}

// --- NewOneTimePrice (issue #218) ---

func TestNewOneTimePrice(t *testing.T) {
	productID := shared.NewProductID()
	createdAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	p, err := NewOneTimePrice(productID, jpy(50000), shared.CurrencyJPY, createdAt)
	if err != nil {
		t.Fatalf("NewOneTimePrice failed: %v", err)
	}
	if p.ID() == "" {
		t.Error("expected non-empty price ID")
	}
	if p.ProductID() != productID {
		t.Error("expected matching product ID")
	}
	if p.Amount().Amount().Cmp(new(big.Rat).SetInt64(50000)) != 0 {
		t.Errorf("expected amount 50000, got %v", p.Amount().Amount())
	}
	if !p.Interval().IsZero() {
		t.Errorf("expected zero interval, got %v", p.Interval())
	}
	if p.BillingCycle() != "" {
		t.Errorf("expected empty billing cycle, got %q", p.BillingCycle())
	}
	if p.PricingModel() != nil {
		t.Errorf("expected nil pricing model (flat one-time charge), got %T", p.PricingModel())
	}
	if p.Status() != PriceStatusActive {
		t.Errorf("expected status active, got %s", p.Status())
	}
	if !p.CreatedAt().Equal(createdAt) {
		t.Errorf("expected createdAt %v, got %v", createdAt, p.CreatedAt())
	}
}

func TestNewOneTimePrice_AmountInvariants(t *testing.T) {
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	// Negative amount rejected (mirrors NewPrice / NewPriceWithInterval).
	if _, err := NewOneTimePrice(shared.NewProductID(), jpy(-1), shared.CurrencyJPY, now); err == nil {
		t.Error("expected error for negative amount")
	}

	// Currency mismatch rejected.
	if _, err := NewOneTimePrice(shared.NewProductID(), usd(100), shared.CurrencyJPY, now); err == nil {
		t.Error("expected error for currency mismatch")
	}

	// Metadata option is honored, same as the other constructors.
	p, err := NewOneTimePrice(shared.NewProductID(), jpy(1000), shared.CurrencyJPY, now,
		WithMetadata(map[string]string{"creator_id": "u1"}))
	if err != nil {
		t.Fatalf("NewOneTimePrice with metadata failed: %v", err)
	}
	if p.Metadata()["creator_id"] != "u1" {
		t.Errorf("expected metadata creator_id=u1, got %v", p.Metadata())
	}
}

// TestNewPriceWithInterval_StillRejectsZeroInterval pins the constraint that
// NewOneTimePrice is the ONLY constructor that waives the interval requirement
// (issue #218): NewPriceWithInterval keeps rejecting zero intervals.
func TestNewPriceWithInterval_StillRejectsZeroInterval(t *testing.T) {
	_, err := NewPriceWithInterval(shared.NewProductID(), jpy(1000), shared.CurrencyJPY,
		BillingInterval{}, nil, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected NewPriceWithInterval to reject a zero interval")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if domErr.Code != shared.ErrCodeValidation {
		t.Errorf("expected ErrCodeValidation, got %s", domErr.Code)
	}
}
