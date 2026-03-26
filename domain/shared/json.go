package shared

import "encoding/json"

// marshalJSON is a helper to marshal to JSON.
func marshalJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// unmarshalJSON is a helper to unmarshal from JSON.
func unmarshalJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
