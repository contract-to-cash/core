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

// SchemaVersioned is an optional interface a DomainEvent may implement to
// self-declare the schema version its current in-memory payload serializes to.
// BaseAggregate.RaiseEvent stamps the returned value onto the Event envelope;
// events that do not implement it default to schema version 1.
//
// Implement this on an event whose current payload shape has diverged from its
// original v1 form — i.e. an Upcaster exists to migrate legacy payloads up to
// the current shape. Declaring the true version means freshly written events are
// stamped at that version and therefore skip the Upcaster on replay (the
// Upcaster's CanUpcast returns false for them) instead of being re-migrated on
// every replay. Without this, a future non-idempotent Upcaster would corrupt
// freshly written events that were mis-stamped as v1.
type SchemaVersioned interface {
	CurrentSchemaVersion() int
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
