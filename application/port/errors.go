package port

import "fmt"

// ErrorCode represents a payment gateway error code.
type ErrorCode string

const (
	ErrorCodeCardDeclined          ErrorCode = "card_declined"
	ErrorCodeCardExpired           ErrorCode = "card_expired"
	ErrorCodeInsufficientFunds     ErrorCode = "insufficient_funds"
	ErrorCodeInvalidCard           ErrorCode = "invalid_card"
	ErrorCodeInvalidCVC            ErrorCode = "invalid_cvc"
	ErrorCodeInvalidExpiryMonth    ErrorCode = "invalid_expiry_month"
	ErrorCodeInvalidExpiryYear     ErrorCode = "invalid_expiry_year"
	ErrorCodeProcessingError       ErrorCode = "processing_error"
	ErrorCodeRateLimitExceeded     ErrorCode = "rate_limit_exceeded"
	ErrorCodeAuthenticationRequired ErrorCode = "authentication_required"
	ErrorCodeDuplicateTransaction  ErrorCode = "duplicate_transaction"
	ErrorCodeAmountTooSmall        ErrorCode = "amount_too_small"
	ErrorCodeAmountTooLarge        ErrorCode = "amount_too_large"
	ErrorCodeCurrencyNotSupported  ErrorCode = "currency_not_supported"
	ErrorCodeMethodNotSupported    ErrorCode = "method_not_supported"
	ErrorCodeCustomerNotFound      ErrorCode = "customer_not_found"
	ErrorCodeGatewayUnavailable    ErrorCode = "gateway_unavailable"
	ErrorCodeGatewayTimeout        ErrorCode = "gateway_timeout"
	ErrorCodeFraudSuspected        ErrorCode = "fraud_suspected"
	ErrorCodeTestModeTransaction   ErrorCode = "test_mode_transaction"
	ErrorCodeUnknown               ErrorCode = "unknown"
)

// GatewayError represents an error returned by a payment gateway.
type GatewayError struct {
	Code        ErrorCode
	Message     string
	DeclineCode string
	Param       string
	Retryable   bool
	RawError    error
}

// Error implements the error interface.
func (e *GatewayError) Error() string {
	if e.RawError != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.RawError)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the underlying raw error.
func (e *GatewayError) Unwrap() error {
	return e.RawError
}

// WebhookErrorCode represents a webhook processing error code.
type WebhookErrorCode string

const (
	WebhookErrorCodeInvalidSignature WebhookErrorCode = "invalid_signature"
	WebhookErrorCodeInvalidPayload   WebhookErrorCode = "invalid_payload"
	WebhookErrorCodeUnsupportedEvent WebhookErrorCode = "unsupported_event"
	WebhookErrorCodeDuplicate        WebhookErrorCode = "duplicate_event"
	WebhookErrorCodeProcessingFailed WebhookErrorCode = "processing_failed"
)

// WebhookError represents a webhook processing error.
type WebhookError struct {
	Code    WebhookErrorCode
	Message string
	Cause   error
}

// Error implements the error interface.
func (e *WebhookError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the underlying cause.
func (e *WebhookError) Unwrap() error {
	return e.Cause
}
