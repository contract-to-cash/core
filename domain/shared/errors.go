package shared

import "fmt"

// ErrorCode represents a domain error code.
type ErrorCode string

const (
	ErrCodeInvalidStateTransition ErrorCode = "invalid_state_transition"
	ErrCodeValidation             ErrorCode = "validation_error"
	ErrCodeCurrencyMismatch       ErrorCode = "currency_mismatch"
	ErrCodeInvalidDateRange       ErrorCode = "invalid_date_range"
	ErrCodeNotFound               ErrorCode = "not_found"
	ErrCodeConflict               ErrorCode = "conflict"
	ErrCodeDuplicateRequest       ErrorCode = "duplicate_request"
	ErrCodeUnknownEvent           ErrorCode = "unknown_event"
	ErrCodeVersionConflict        ErrorCode = "version_conflict"
	ErrCodeBusinessRule           ErrorCode = "business_rule_violation"
)

// DomainError is a structured domain error.
type DomainError struct {
	Code    ErrorCode
	Message string
	Cause   error
}

// NewDomainError creates a new DomainError.
func NewDomainError(code ErrorCode, message string) *DomainError {
	return &DomainError{Code: code, Message: message}
}

// NewDomainErrorWithCause creates a new DomainError with a wrapped cause.
func NewDomainErrorWithCause(code ErrorCode, message string, cause error) *DomainError {
	return &DomainError{Code: code, Message: message, Cause: cause}
}

// Error implements the error interface.
func (e *DomainError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the wrapped cause.
func (e *DomainError) Unwrap() error {
	return e.Cause
}
