package eventstore

import (
	"encoding/json"
	"errors"
	"testing"
)

// renameUpcaster bumps schema version 1 -> 2 for a given event type, rewriting
// the "old_name" field to "name".
type renameUpcaster struct {
	eventType EventType
}

func (u renameUpcaster) CanUpcast(eventType EventType, fromVersion int) bool {
	return eventType == u.eventType && fromVersion == 1
}

func (u renameUpcaster) Upcast(event Event) (Event, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return Event{}, err
	}
	if v, ok := payload["old_name"]; ok {
		payload["name"] = v
		delete(payload, "old_name")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	event.Data = data
	event.SchemaVersion = 2
	return event, nil
}

// addFieldUpcaster bumps schema version 2 -> 3, adding a default "value" field.
type addFieldUpcaster struct {
	eventType EventType
}

func (u addFieldUpcaster) CanUpcast(eventType EventType, fromVersion int) bool {
	return eventType == u.eventType && fromVersion == 2
}

func (u addFieldUpcaster) Upcast(event Event) (Event, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return Event{}, err
	}
	payload["value"] = json.RawMessage(`42`)
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	event.Data = data
	event.SchemaVersion = 3
	return event, nil
}

// failingUpcaster always returns an error when it upcasts.
type failingUpcaster struct {
	err error
}

func (u failingUpcaster) CanUpcast(eventType EventType, fromVersion int) bool { return true }
func (u failingUpcaster) Upcast(event Event) (Event, error)                   { return Event{}, u.err }

func TestUpcasterChain_AppliesInSequence(t *testing.T) {
	const et EventType = "test.event"
	chain := NewUpcasterChain(
		renameUpcaster{eventType: et},
		addFieldUpcaster{eventType: et},
	)

	in := Event{
		Type:          et,
		SchemaVersion: 1,
		Data:          json.RawMessage(`{"old_name":"hello"}`),
	}
	out, err := chain.Upcast(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.SchemaVersion != 3 {
		t.Errorf("expected schema version 3, got %d", out.SchemaVersion)
	}

	var result testEvent
	if err := json.Unmarshal(out.Data, &result); err != nil {
		t.Fatalf("failed to unmarshal upcast data: %v", err)
	}
	if result.Name != "hello" {
		t.Errorf("expected name %q after rename upcast, got %q", "hello", result.Name)
	}
	if result.Value != 42 {
		t.Errorf("expected value 42 after add-field upcast, got %d", result.Value)
	}
}

func TestUpcasterChain_SkipsNonMatching(t *testing.T) {
	chain := NewUpcasterChain(
		renameUpcaster{eventType: "other.event"},
	)
	in := Event{
		Type:          "test.event",
		SchemaVersion: 1,
		Data:          json.RawMessage(`{"name":"unchanged","value":7}`),
	}
	out, err := chain.Upcast(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.SchemaVersion != 1 {
		t.Errorf("expected schema version to remain 1, got %d", out.SchemaVersion)
	}
	if string(out.Data) != string(in.Data) {
		t.Errorf("expected data unchanged, got %s", out.Data)
	}
}

func TestUpcasterChain_Empty(t *testing.T) {
	chain := NewUpcasterChain()
	in := Event{Type: "test.event", SchemaVersion: 1, Data: json.RawMessage(`{}`)}
	out, err := chain.Upcast(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.SchemaVersion != 1 {
		t.Errorf("expected unchanged event from empty chain, got version %d", out.SchemaVersion)
	}
}

func TestUpcasterChain_PropagatesError(t *testing.T) {
	sentinel := errors.New("upcast failed")
	chain := NewUpcasterChain(failingUpcaster{err: sentinel})
	_, err := chain.Upcast(Event{Type: "test.event", SchemaVersion: 1})
	if err == nil {
		t.Fatal("expected error from failing upcaster, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

// TestUpcasterChain_StopsAfterError verifies the chain returns the zero Event and
// does not run subsequent upcasters once one fails.
func TestUpcasterChain_StopsAfterError(t *testing.T) {
	sentinel := errors.New("boom")
	var ran bool
	chain := NewUpcasterChain(
		failingUpcaster{err: sentinel},
		spyUpcaster{onUpcast: func() { ran = true }},
	)
	if _, err := chain.Upcast(Event{Type: "test.event"}); err == nil {
		t.Fatal("expected error")
	}
	if ran {
		t.Error("expected chain to stop after first upcaster error")
	}
}

type spyUpcaster struct {
	onUpcast func()
}

func (u spyUpcaster) CanUpcast(eventType EventType, fromVersion int) bool { return true }
func (u spyUpcaster) Upcast(event Event) (Event, error) {
	u.onUpcast()
	return event, nil
}
