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
	ID            string          `json:"id"`
	StreamID      string          `json:"stream_id"`
	Type          EventType       `json:"type"`
	Version       int             `json:"version"`
	SchemaVersion int             `json:"schema_version"`
	Data          json.RawMessage `json:"data"`
	Metadata      EventMetadata   `json:"metadata"`
	OccurredAt    time.Time       `json:"occurred_at"`
	RecordedAt    time.Time       `json:"recorded_at"`
}

// EventMetadata holds audit and tracing information for an event.
type EventMetadata struct {
	UserID        string  `json:"user_id"`
	IPAddress     *string `json:"ip_address,omitempty"`
	UserAgent     *string `json:"user_agent,omitempty"`
	CorrelationID string  `json:"correlation_id,omitempty"`
	CausationID   string  `json:"causation_id,omitempty"`
}
