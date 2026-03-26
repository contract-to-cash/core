package billing

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestNewProrationResult(t *testing.T) {
	credit := shared.NewMoney(new(big.Rat).SetInt64(300), shared.CurrencyJPY)
	charge := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyJPY)
	effectiveDate := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	result, err := NewProrationResult(credit, charge, effectiveDate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// AdjustmentAmount = 500 - 300 = 200
	expectedAdj := new(big.Rat).SetInt64(200)
	if result.AdjustmentAmount.Amount().Cmp(expectedAdj) != 0 {
		t.Errorf("expected adjustment 200, got %s", result.AdjustmentAmount.Amount().RatString())
	}

	if result.CreditAmount.Amount().Cmp(credit.Amount()) != 0 {
		t.Errorf("expected credit %s, got %s", credit.Amount().RatString(), result.CreditAmount.Amount().RatString())
	}

	if result.ChargeAmount.Amount().Cmp(charge.Amount()) != 0 {
		t.Errorf("expected charge %s, got %s", charge.Amount().RatString(), result.ChargeAmount.Amount().RatString())
	}

	if !result.EffectiveDate.Equal(effectiveDate) {
		t.Errorf("expected effectiveDate %v, got %v", effectiveDate, result.EffectiveDate)
	}
}

func TestNewProrationResult_NegativeAdjustment(t *testing.T) {
	credit := shared.NewMoney(new(big.Rat).SetInt64(800), shared.CurrencyJPY)
	charge := shared.NewMoney(new(big.Rat).SetInt64(300), shared.CurrencyJPY)
	effectiveDate := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	result, err := NewProrationResult(credit, charge, effectiveDate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// AdjustmentAmount = 300 - 800 = -500
	expectedAdj := new(big.Rat).SetInt64(-500)
	if result.AdjustmentAmount.Amount().Cmp(expectedAdj) != 0 {
		t.Errorf("expected adjustment -500, got %s", result.AdjustmentAmount.Amount().RatString())
	}
}

func TestNewProrationResult_CurrencyMismatch(t *testing.T) {
	credit := shared.NewMoney(new(big.Rat).SetInt64(300), shared.CurrencyJPY)
	charge := shared.NewMoney(new(big.Rat).SetInt64(500), shared.CurrencyUSD)
	effectiveDate := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	_, err := NewProrationResult(credit, charge, effectiveDate)
	if err == nil {
		t.Error("expected error for currency mismatch")
	}
}
