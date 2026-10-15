package tui

import (
	"encoding/json"
)

func parseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

func toJSON(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
