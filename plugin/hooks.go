package plugin

import (
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
)

// DiscountHook is implemented by plugins that calculate discounts during billing.
type DiscountHook interface {
	Plugin
	CalculateDiscount(ctx *CalculationContext) (shared.Money, error)
}

// TaxHook is implemented by plugins that calculate tax during billing.
type TaxHook interface {
	Plugin
	CalculateTax(ctx *CalculationContext) (shared.Money, error)
}

// InvoiceLifecycleHook is implemented by plugins that hook into invoice calculation lifecycle.
type InvoiceLifecycleHook interface {
	Plugin
	BeforeCalculation(ctx *CalculationContext) error
	AfterCalculation(ctx *CalculationContext, invoice *invoice.Invoice) error
}
