package port

import (
	"context"

	"github.com/contract-to-cash/core/domain/shared"
)

// GatewayRouter selects the appropriate payment gateway based on routing criteria.
type GatewayRouter interface {
	// Route selects a PaymentGateway based on the given criteria.
	Route(ctx context.Context, criteria RoutingCriteria) (PaymentGateway, error)
}

// RoutingCriteria holds the parameters used to select a gateway.
type RoutingCriteria struct {
	Amount        shared.Money
	Currency      shared.Currency
	PaymentMethod PaymentMethodType
	CustomerID    string
	Country       string
	Metadata      map[string]string
}

// RoutingRule defines a single routing rule for gateway selection.
type RoutingRule struct {
	GatewayID      string
	Priority       int
	PaymentMethods []PaymentMethodType
	Currencies     []shared.Currency
	Countries      []string
	MinAmount      *shared.Money
	MaxAmount      *shared.Money
}
