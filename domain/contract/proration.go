package contract

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// ProrationBehavior defines when proration charges are applied.
type ProrationBehavior string

const (
	ProrationImmediate     ProrationBehavior = "immediate"
	ProrationNextCycle     ProrationBehavior = "next_cycle"
	ProrationImmediateFull ProrationBehavior = "immediate_full"
)

// RoundingMode defines how monetary rounding is performed.
type RoundingMode string

const (
	RoundingUp     RoundingMode = "up"
	RoundingDown   RoundingMode = "down"
	RoundingHalfUp RoundingMode = "half_up"
)

// ProrationConfig holds proration settings.
type ProrationConfig struct {
	Behavior     ProrationBehavior `json:"behavior"`
	RoundingMode RoundingMode      `json:"rounding_mode"`
}

// PlanChangeProration holds the result of a proration calculation for a plan change.
// This mirrors billing.ProrationResult but lives in the contract domain to avoid circular dependencies.
type PlanChangeProration struct {
	CreditAmount     shared.Money `json:"credit_amount"`
	ChargeAmount     shared.Money `json:"charge_amount"`
	AdjustmentAmount shared.Money `json:"adjustment_amount"`
	EffectiveDate    time.Time    `json:"effective_date"`
}
