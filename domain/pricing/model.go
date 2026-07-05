package pricing

import (
	"fmt"

	"github.com/contract-to-cash/core/domain/shared"
)

// PricingModel calculates a price based on usage quantity.
//
// Contract: usage must be non-negative. usage == 0 is legitimate (no billable
// usage after allowance deduction) and returns zero money; usage < 0 is a
// programming error. Callers must clamp at the allowance-deduction boundary
// before invoking CalculatePrice — see application/service BillingService,
// which computes `summary.TotalUsage - metric.IncludedQuantity` and clamps the
// result to zero. Implementations enforce this via assertNonNegativeUsage.
type PricingModel interface {
	CalculatePrice(usage int64) shared.Money
}

// assertNonNegativeUsage enforces the PricingModel contract that usage is
// non-negative. It panics on negative input because this is a domain invariant
// violation (a caller bug), not a validation failure of external input:
// returning an error would force every call site to handle an impossible case
// and would cascade through the PricingModel interface signature. The upstream
// non-negative invariant lives in domain/usage (NewUsageRecord rejects negative
// quantity), and BillingService clamps allowance-deducted usage to zero, so a
// negative value reaching here signals a broken invariant that must surface
// loudly rather than be silently absorbed into a zero charge.
func assertNonNegativeUsage(model string, usage int64) {
	if usage < 0 {
		panic(fmt.Sprintf("%s.CalculatePrice: negative usage %d (caller bug)", model, usage))
	}
}

// UsageMetric defines a usage-based metric with its pricing model.
type UsageMetric struct {
	Name             shared.MetricName
	PricingModel     PricingModel
	IncludedQuantity int64
}

// Feature defines a feature within a product.
type Feature struct {
	Name     string
	Included bool
	Limit    *int64
}
