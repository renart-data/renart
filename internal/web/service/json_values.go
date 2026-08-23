package service

import "encoding/json"

// cloneJSONValue copies the JSON-shaped values shared by notebook runtime
// parameters and source templates without retaining caller-owned maps/slices.
func cloneJSONValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var result any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return value
	}
	return result
}
