package eventstore

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

// EventRegistry maps EventType to Go types for deserialization.
// Thread-safe. Register は通常パッケージ初期化時に呼ばれるが、実行時の動的登録もサポートする。
type EventRegistry struct {
	mu    sync.RWMutex
	types map[EventType]reflect.Type
}

// NewEventRegistry creates a new empty EventRegistry.
func NewEventRegistry() *EventRegistry {
	return &EventRegistry{
		types: make(map[EventType]reflect.Type),
	}
}

// Register registers a DomainEvent type.
// The event's EventType() is used as the key.
// Goroutine-safe.
func (r *EventRegistry) Register(event DomainEvent) {
	t := reflect.TypeOf(event)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types[event.EventType()] = t
}

// Deserialize converts a json.RawMessage to a typed DomainEvent.
// Goroutine-safe.
func (r *EventRegistry) Deserialize(eventType EventType, data json.RawMessage) (DomainEvent, error) {
	r.mu.RLock()
	t, ok := r.types[eventType]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown event type: %s", eventType)
	}

	ptr := reflect.New(t)
	if err := json.Unmarshal(data, ptr.Interface()); err != nil {
		return nil, fmt.Errorf("failed to deserialize event type %s: %w", eventType, err)
	}

	event, ok := ptr.Interface().(DomainEvent)
	if !ok {
		return nil, fmt.Errorf("type %s does not implement DomainEvent", t.Name())
	}

	return event, nil
}
