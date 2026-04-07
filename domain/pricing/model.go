package pricing

import (
	"github.com/contract-to-cash/core/domain/shared"
)

// PricingModel calculates a price based on usage quantity.
type PricingModel interface {
	CalculatePrice(usage int64) shared.Money
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
