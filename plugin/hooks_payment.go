package plugin

import (
	"github.com/contract-to-cash/core/domain/shared"
)

// BeforeChargeHook is called before a charge is attempted.
type BeforeChargeHook interface {
	Plugin
	BeforeCharge(ctx *PaymentContext, amount shared.Money) error
}

// AfterChargeHook is called after a charge has been successfully processed.
type AfterChargeHook interface {
	Plugin
	AfterCharge(ctx *PaymentContext) error
}

// OnPaymentFailedHook is called when a payment fails.
type OnPaymentFailedHook interface {
	Plugin
	OnPaymentFailed(ctx *PaymentContext, err error) error
}

// OnRefundHook is called when a refund is processed.
type OnRefundHook interface {
	Plugin
	OnRefund(ctx *PaymentContext, refundAmount shared.Money) error
}
