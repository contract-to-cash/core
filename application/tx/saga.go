package tx

import (
	"context"
	"errors"
)

// CompensationAction is a function that undoes a previously completed step.
type CompensationAction func(ctx context.Context) error

// Saga tracks compensation actions for operations involving external side-effects
// that cannot participate in database transactions (e.g., gateway charges).
type Saga struct {
	compensations []CompensationAction
}

// NewSaga creates an empty Saga.
func NewSaga() *Saga {
	return &Saga{}
}

// AddCompensation registers a compensation action to be run on failure.
func (s *Saga) AddCompensation(c CompensationAction) {
	s.compensations = append(s.compensations, c)
}

// Compensate runs all registered compensations in reverse order.
// All compensations are attempted regardless of individual failures.
// Returns a joined error containing all failures, or nil if all succeeded.
func (s *Saga) Compensate(ctx context.Context) error {
	var errs []error
	for i := len(s.compensations) - 1; i >= 0; i-- {
		if err := s.compensations[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
