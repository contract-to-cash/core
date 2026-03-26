package billing

import (
	"context"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// Calculator defines the billing calculation interface.
type Calculator interface {
	GenerateInvoice(ctx context.Context, contractID shared.ContractID) error
	CalculateProration(ctx context.Context, contractID shared.ContractID, newPrice shared.Money) (*ProrationResult, error)
}

// ProrationResult holds the result of a proration calculation.
type ProrationResult struct {
	CreditAmount     shared.Money
	ChargeAmount     shared.Money
	AdjustmentAmount shared.Money
	EffectiveDate    time.Time
}

// NewProrationResult creates a ProrationResult with AdjustmentAmount = ChargeAmount - CreditAmount.
func NewProrationResult(creditAmount, chargeAmount shared.Money, effectiveDate time.Time) (*ProrationResult, error) {
	adjustment, err := chargeAmount.Subtract(creditAmount)
	if err != nil {
		return nil, err
	}
	return &ProrationResult{
		CreditAmount:     creditAmount,
		ChargeAmount:     chargeAmount,
		AdjustmentAmount: adjustment,
		EffectiveDate:    effectiveDate,
	}, nil
}
