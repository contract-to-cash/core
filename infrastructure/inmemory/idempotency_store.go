package inmemory

import (
	"context"
	"fmt"
	"sync"

	"github.com/contract-to-cash/core/application/port"
)

// Compile-time interface check.
var _ port.IdempotencyStore = (*InMemoryIdempotencyStore)(nil)

// InMemoryIdempotencyStore is a thread-safe in-memory implementation of
// [port.IdempotencyStore]. It is intended for tests, examples, and single-
// node development setups; production deployments should back the store with
// the same durable datastore as the payment repository so marker writes can
// participate in the saga compensation transaction.
type InMemoryIdempotencyStore struct {
	mu      sync.RWMutex
	mapping map[string]string // originalKey -> effectiveKey
}

// NewInMemoryIdempotencyStore creates an empty InMemoryIdempotencyStore.
func NewInMemoryIdempotencyStore() *InMemoryIdempotencyStore {
	return &InMemoryIdempotencyStore{mapping: make(map[string]string)}
}

// MarkCompensated records the (originalKey → effectiveKey) mapping.
//
// First-call-wins semantics: if the original key already has a stored
// effective key, the existing value is preserved and the call is a no-op.
// This matches the IdempotencyStore contract and ensures concurrent
// compensations converge on a single stable effective key.
func (s *InMemoryIdempotencyStore) MarkCompensated(_ context.Context, originalKey, effectiveKey string) error {
	if originalKey == "" {
		return fmt.Errorf("idempotency store: original key must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.mapping[originalKey]; exists {
		return nil
	}
	s.mapping[originalKey] = effectiveKey
	return nil
}

// ResolveEffectiveKey returns the stored effective key for the given
// original key, or ("", false, nil) if the original key has not been
// compensated.
func (s *InMemoryIdempotencyStore) ResolveEffectiveKey(_ context.Context, originalKey string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	eff, ok := s.mapping[originalKey]
	return eff, ok, nil
}
