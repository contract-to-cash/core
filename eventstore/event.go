package eventstore

import (
	"encoding/json"
	"time"
)

// EventType identifies the type of a domain event.
type EventType string

// DomainEvent is implemented by all typed domain events.
type DomainEvent interface {
	EventType() EventType
}

// Event is the persisted representation of a domain event.
type Event struct {
	ID             string          `json:"id"`
	StreamID       string          `json:"stream_id"`
	Type           EventType       `json:"type"`
	Version        int             `json:"version"`
	SchemaVersion  int             `json:"schema_version"`
	Data           json.RawMessage `json:"data"`
	Metadata       EventMetadata   `json:"metadata"`
	OccurredAt     time.Time       `json:"occurred_at"`
	RecordedAt     time.Time       `json:"recorded_at"`
	GlobalPosition int64           `json:"global_position,omitempty"`
}

// EventMetadata holds audit information for an event.
//
// Historical note: earlier versions carried CorrelationID / CausationID string
// fields intended for future tracing. They were never populated by any core
// flow and were removed in issue #116 (Option B) until a concrete consumer
// appears. Removal is read-safe: metadata is deserialized independently from
// the schema-versioned event Data payload, so no Upcaster is involved, and
// json.Unmarshal ignores the now-unknown "correlation_id" / "causation_id"
// keys that may still be present in previously stored events. Re-adding a
// tracing field later is a purely additive (non-breaking) change.
type EventMetadata struct {
	UserID    string  `json:"user_id"`
	IPAddress *string `json:"ip_address,omitempty"`
	UserAgent *string `json:"user_agent,omitempty"`
}
