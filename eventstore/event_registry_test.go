package eventstore

import (
	"encoding/json"
	"testing"
)

// testEvent is a sample domain event for testing.
type testEvent struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func (e *testEvent) EventType() EventType { return "test.event" }

func TestEventRegistry_RegisterAndDeserialize(t *testing.T) {
	registry := NewEventRegistry()
	if err := registry.Register(&testEvent{}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	original := testEvent{Name: "test", Value: 42}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	deserialized, err := registry.Deserialize("test.event", data)
	if err != nil {
		t.Fatal(err)
	}

	result, ok := deserialized.(*testEvent)
	if !ok {
		t.Fatalf("expected *testEvent type, got %T", deserialized)
	}
	if result.Name != "test" || result.Value != 42 {
		t.Errorf("expected {test, 42}, got {%s, %d}", result.Name, result.Value)
	}
}

func TestEventRegistry_UnknownType(t *testing.T) {
	registry := NewEventRegistry()
	_, err := registry.Deserialize("unknown.event", json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for unknown event type")
	}
}

// otherEvent is a distinct Go type claiming the same EventType as testEvent, used
// to exercise duplicate-registration rejection.
type otherEvent struct{}

func (e *otherEvent) EventType() EventType { return "test.event" }

func TestEventRegistry_Register_RejectsDuplicate(t *testing.T) {
	registry := NewEventRegistry()
	if err := registry.Register(&testEvent{}); err != nil {
		t.Fatalf("first registration should succeed, got %v", err)
	}

	// Re-registering the same EventType (even a different Go type) must error
	// rather than silently overwrite.
	if err := registry.Register(&otherEvent{}); err == nil {
		t.Fatal("expected error re-registering an already-registered event type, got nil")
	}

	// The original mapping must be preserved (not overwritten).
	deserialized, err := registry.Deserialize("test.event", json.RawMessage(`{"name":"x","value":1}`))
	if err != nil {
		t.Fatalf("unexpected deserialize error: %v", err)
	}
	if _, ok := deserialized.(*testEvent); !ok {
		t.Errorf("expected original *testEvent mapping preserved, got %T", deserialized)
	}
}
