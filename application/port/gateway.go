// Package port defines interfaces for external system integrations
// (payment gateways, customer gateways, webhooks, routing).
package port

import "context"

// PaymentMethodType identifies the type of payment method.
type PaymentMethodType string

const (
	PaymentMethodTypeCreditCard       PaymentMethodType = "credit_card"
	PaymentMethodTypeDebitCard        PaymentMethodType = "debit_card"
	PaymentMethodTypeBankTransfer     PaymentMethodType = "bank_transfer"
	PaymentMethodTypeConvenienceStore PaymentMethodType = "convenience_store"
	PaymentMethodTypeQRCode           PaymentMethodType = "qr_code"
	PaymentMethodTypeCarrier          PaymentMethodType = "carrier"
	PaymentMethodTypePostpay          PaymentMethodType = "postpay"
	PaymentMethodTypeDirectDebit      PaymentMethodType = "direct_debit"
)

// PaymentGateway defines the interface for interacting with payment providers.
type PaymentGateway interface {
	// ID returns the unique identifier for this gateway.
	ID() string

	// SupportedMethods returns the payment method types this gateway supports.
	SupportedMethods() []PaymentMethodType

	// Charge performs a one-step charge (authorize + capture).
	Charge(ctx context.Context, req *ChargeRequest) (*ChargeResponse, error)

	// Authorize places a hold on funds without capturing.
	Authorize(ctx context.Context, req *AuthorizeRequest) (*AuthorizeResponse, error)

	// Capture captures a previously authorized transaction.
	Capture(ctx context.Context, req *CaptureRequest) (*CaptureResponse, error)

	// Void cancels a previously authorized transaction before capture.
	Void(ctx context.Context, req *VoidRequest) (*VoidResponse, error)

	// Refund refunds a captured or charged transaction.
	Refund(ctx context.Context, req *RefundRequest) (*RefundResponse, error)

	// Cancel cancels a pending transaction.
	Cancel(ctx context.Context, req *CancelRequest) (*CancelResponse, error)

	// GetTransaction retrieves a transaction by its gateway-side ID.
	GetTransaction(ctx context.Context, transactionID string) (*Transaction, error)

	// RegisterPaymentMethod registers a new payment method for a customer.
	RegisterPaymentMethod(ctx context.Context, req *RegisterPaymentMethodRequest) (*PaymentMethodDetail, error)

	// DeletePaymentMethod removes a payment method.
	DeletePaymentMethod(ctx context.Context, paymentMethodID string) error

	// GetPaymentMethod retrieves a payment method by ID.
	GetPaymentMethod(ctx context.Context, paymentMethodID string) (*PaymentMethodDetail, error)

	// ListPaymentMethods lists all payment methods for a customer.
	ListPaymentMethods(ctx context.Context, customerID string) ([]*PaymentMethodDetail, error)
}
