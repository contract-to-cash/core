package plugin

import (
	"context"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
)

// PaymentContext provides type-safe context for payment hooks.
// It follows the same pattern as CalculationContext for billing hooks.
// PaymentService already loads the invoice, so no additional DB queries are needed.
type PaymentContext struct {
	ctx      context.Context
	payment  *payment.Payment
	invoice  *invoice.Invoice
	contract *contract.ContractAggregate
}

// NewPaymentContext creates a new PaymentContext with the payment and invoice.
// The contract field is optional and can be set later via SetContract.
func NewPaymentContext(ctx context.Context, p *payment.Payment, inv *invoice.Invoice) *PaymentContext {
	return &PaymentContext{
		ctx:     ctx,
		payment: p,
		invoice: inv,
	}
}

// Context returns the underlying context.Context.
func (pc *PaymentContext) Context() context.Context { return pc.ctx }

// Payment returns the payment.
func (pc *PaymentContext) Payment() *payment.Payment { return pc.payment }

// Invoice returns the invoice.
func (pc *PaymentContext) Invoice() *invoice.Invoice { return pc.invoice }

// Contract returns the contract aggregate, if set.
func (pc *PaymentContext) Contract() *contract.ContractAggregate { return pc.contract }

// SetContract sets the contract aggregate on the context.
func (pc *PaymentContext) SetContract(c *contract.ContractAggregate) { pc.contract = c }

// ContractID returns the contract ID from the invoice.
func (pc *PaymentContext) ContractID() shared.ContractID {
	if pc.invoice == nil {
		return ""
	}
	return pc.invoice.ContractID()
}

// AccountID returns the account ID from the invoice.
func (pc *PaymentContext) AccountID() shared.AccountID {
	if pc.invoice == nil {
		return ""
	}
	return pc.invoice.AccountID()
}
