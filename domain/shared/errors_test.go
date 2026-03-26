package shared

import (
	"errors"
	"fmt"
	"testing"
)

func TestDomainError_Error(t *testing.T) {
	err := NewDomainError(ErrCodeValidation, "name is required")
	expected := "[validation_error] name is required"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestDomainError_WithCause(t *testing.T) {
	cause := fmt.Errorf("underlying error")
	err := NewDomainErrorWithCause(ErrCodeNotFound, "contract not found", cause)
	if !errors.Is(err, cause) {
		t.Error("expected to unwrap to cause")
	}
	if err.Cause != cause {
		t.Error("expected Cause to be set")
	}
}

func TestDomainError_Unwrap(t *testing.T) {
	cause := fmt.Errorf("root cause")
	err := NewDomainErrorWithCause(ErrCodeConflict, "conflict", cause)
	unwrapped := errors.Unwrap(err)
	if unwrapped != cause {
		t.Errorf("expected %v, got %v", cause, unwrapped)
	}
}
