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
//
// It aliases shared.RoundingMode so that a proration calculator can apply the
// configured mode directly via Money.Round (e.g.
// adjustment.Round(2, cfg.RoundingMode)). Proration itself is computed by the
// consumer's proration calculator (whose PlanChangeProration result is passed to
// ContractAggregate.ChangePrice and BillingService.GenerateProrationInvoice);
// this config carries the rounding policy through to that implementation.
type RoundingMode = shared.RoundingMode

const (
	RoundingUp     = shared.RoundUp
	RoundingDown   = shared.RoundDown
	RoundingHalfUp = shared.RoundHalfUp
)

// ProrationConfig holds proration settings.
type ProrationConfig struct {
	Behavior     ProrationBehavior `json:"behavior"`
	RoundingMode RoundingMode      `json:"rounding_mode"`
}

// PlanChangeProration holds the result of a proration calculation for a plan change.
// It is produced by the consumer's proration calculator and consumed by
// ContractAggregate.ChangePrice (recorded on PriceChangedEvent) and
// BillingService.GenerateProrationInvoice.
type PlanChangeProration struct {
	CreditAmount     shared.Money `json:"credit_amount"`
	ChargeAmount     shared.Money `json:"charge_amount"`
	AdjustmentAmount shared.Money `json:"adjustment_amount"`
	EffectiveDate    time.Time    `json:"effective_date"`
}
