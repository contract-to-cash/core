package pricing

import (
	"github.com/contract-to-cash/core/domain/shared"
)

// PricingModel calculates a price based on usage quantity.
type PricingModel interface {
	CalculatePrice(usage int64) shared.Money
}

// Plan represents a pricing plan with its pricing model and features.
type Plan struct {
	id           shared.PlanID
	name         string
	description  string
	pricingModel PricingModel
	usageMetrics []UsageMetric
	features     []Feature
	metadata     map[string]string
}

// UsageMetric defines a usage-based metric within a plan.
type UsageMetric struct {
	Name             string
	PricingModel     PricingModel
	IncludedQuantity int64
}

// Feature defines a feature within a plan.
type Feature struct {
	Name     string
	Included bool
	Limit    *int64
}

// NewPlan creates a new Plan.
func NewPlan(name string, description string, pricingModel PricingModel) *Plan {
	return &Plan{
		id:           shared.NewPlanID(),
		name:         name,
		description:  description,
		pricingModel: pricingModel,
		metadata:     make(map[string]string),
	}
}

// ID returns the plan ID.
func (p *Plan) ID() shared.PlanID { return p.id }

// Name returns the plan name.
func (p *Plan) Name() string { return p.name }

// Description returns the plan description.
func (p *Plan) Description() string { return p.description }

// PricingModel returns the plan's pricing model.
func (p *Plan) PricingModel() PricingModel { return p.pricingModel }

// UsageMetrics returns a copy of the plan's usage metrics.
func (p *Plan) UsageMetrics() []UsageMetric {
	cp := make([]UsageMetric, len(p.usageMetrics))
	copy(cp, p.usageMetrics)
	return cp
}

// Features returns a copy of the plan's features.
func (p *Plan) Features() []Feature {
	cp := make([]Feature, len(p.features))
	copy(cp, p.features)
	return cp
}

// Metadata returns a copy of the plan's metadata.
func (p *Plan) Metadata() map[string]string {
	cp := make(map[string]string, len(p.metadata))
	for k, v := range p.metadata {
		cp[k] = v
	}
	return cp
}
