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
	registry.Register(&testEvent{})

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
