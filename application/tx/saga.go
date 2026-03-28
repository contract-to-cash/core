package tx

import "context"

// CompensationAction is a rollback action for a completed external
// side-effect (e.g., voiding a gateway charge after a local save failure).
type CompensationAction func(ctx context.Context) error

// Saga collects compensation actions during a multi-step operation
// and executes them in reverse order on failure.
type Saga struct {
	compensations []CompensationAction
}

// AddCompensation registers a compensation to run if the saga fails.
func (s *Saga) AddCompensation(c CompensationAction) {
	s.compensations = append(s.compensations, c)
}

// Compensate runs all registered compensations in reverse order.
// It attempts every compensation even if earlier ones fail.
// Returns the first error encountered.
func (s *Saga) Compensate(ctx context.Context) error {
	var firstErr error
	for i := len(s.compensations) - 1; i >= 0; i-- {
		if err := s.compensations[i](ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// HasCompensations returns true if any compensations have been registered.
func (s *Saga) HasCompensations() bool {
	return len(s.compensations) > 0
}
