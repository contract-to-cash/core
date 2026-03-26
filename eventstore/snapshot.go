package eventstore

import (
	"encoding/json"
	"time"
)

// Snapshot represents a snapshot of aggregate state at a point in time.
type Snapshot struct {
	StreamID  string          `json:"stream_id"`
	Version   int             `json:"version"`
	State     json.RawMessage `json:"state"`
	AsOf      time.Time       `json:"as_of"`
	CreatedAt time.Time       `json:"created_at"`
}
