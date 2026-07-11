package inmemory

import (
	"context"
	"sync"

	"github.com/contract-to-cash/core/application/projection"
)

// Compile-time interface check.
var _ projection.CheckpointStore = (*InMemoryCheckpointStore)(nil)

// InMemoryCheckpointStore is a thread-safe in-memory implementation of
// [projection.CheckpointStore]. It is intended for tests, examples, and single-
// node development setups; production deployments should back the checkpoint
// with a durable datastore (ideally the same one that holds the projection read
// model, so the checkpoint advances atomically with the projection write).
type InMemoryCheckpointStore struct {
	mu        sync.RWMutex
	positions map[string]int64 // projectionName -> last processed global position
}

// NewInMemoryCheckpointStore creates an empty InMemoryCheckpointStore.
func NewInMemoryCheckpointStore() *InMemoryCheckpointStore {
	return &InMemoryCheckpointStore{positions: make(map[string]int64)}
}

// Load returns the stored position for projectionName, or 0 if none has been
// saved (start from the beginning).
func (s *InMemoryCheckpointStore) Load(_ context.Context, projectionName string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.positions[projectionName], nil
}

// Save persists position as the last processed global position for
// projectionName.
func (s *InMemoryCheckpointStore) Save(_ context.Context, projectionName string, position int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.positions[projectionName] = position
	return nil
}
