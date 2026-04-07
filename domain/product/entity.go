package product

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// ProductStatus represents the lifecycle status of a product.
type ProductStatus string

const (
	ProductStatusActive   ProductStatus = "active"
	ProductStatusArchived ProductStatus = "archived"
)

// Feature defines a feature within a product.
type Feature struct {
	Name     string
	Included bool
	Limit    *int64
}

// UsageMetric defines a usage-based metric within a product.
type UsageMetric struct {
	Name             shared.MetricName
	IncludedQuantity int64
}

// Product represents what you sell, decoupled from how you charge.
type Product struct {
	id           shared.ProductID
	name         string
	description  string
	features     []Feature
	usageMetrics []UsageMetric
	status       ProductStatus
	metadata     map[string]string
	createdAt    time.Time
}

// NewProduct creates a new active Product.
func NewProduct(name string, description string, createdAt time.Time) *Product {
	return &Product{
		id:          shared.NewProductID(),
		name:        name,
		description: description,
		status:      ProductStatusActive,
		metadata:    make(map[string]string),
		createdAt:   createdAt,
	}
}

// ID returns the product ID.
func (p *Product) ID() shared.ProductID { return p.id }

// Name returns the product name.
func (p *Product) Name() string { return p.name }

// Description returns the product description.
func (p *Product) Description() string { return p.description }

// Status returns the product status.
func (p *Product) Status() ProductStatus { return p.status }

// CreatedAt returns the creation timestamp.
func (p *Product) CreatedAt() time.Time { return p.createdAt }

// Features returns a copy of the product's features.
func (p *Product) Features() []Feature {
	cp := make([]Feature, len(p.features))
	copy(cp, p.features)
	return cp
}

// UsageMetrics returns a copy of the product's usage metrics.
func (p *Product) UsageMetrics() []UsageMetric {
	cp := make([]UsageMetric, len(p.usageMetrics))
	copy(cp, p.usageMetrics)
	return cp
}

// Metadata returns a copy of the product's metadata.
func (p *Product) Metadata() map[string]string {
	cp := make(map[string]string, len(p.metadata))
	for k, v := range p.metadata {
		cp[k] = v
	}
	return cp
}

// AddFeature adds a feature to the product.
// If a feature with the same name already exists, it is replaced.
func (p *Product) AddFeature(f Feature) {
	for i, existing := range p.features {
		if existing.Name == f.Name {
			p.features[i] = f
			return
		}
	}
	p.features = append(p.features, f)
}

// AddUsageMetric adds a usage metric to the product.
// If a metric with the same name already exists, it is replaced.
func (p *Product) AddUsageMetric(m UsageMetric) {
	for i, existing := range p.usageMetrics {
		if existing.Name == m.Name {
			p.usageMetrics[i] = m
			return
		}
	}
	p.usageMetrics = append(p.usageMetrics, m)
}

// SetMetadata sets a metadata key-value pair.
func (p *Product) SetMetadata(key, value string) {
	p.metadata[key] = value
}

// Archive marks the product as archived.
func (p *Product) Archive() error {
	if p.status == ProductStatusArchived {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			"product is already archived")
	}
	p.status = ProductStatusArchived
	return nil
}
