package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const defaultMaxJSONRequestBytes int64 = 4 << 20

// decodeJSONObject is the handler boundary for ordinary JSON requests. It
// applies a bounded body before enforcing exactly one non-null object and
// rejecting unknown fields. Streaming uploads and intentionally optional or
// polymorphic bodies use separate explicit paths.
func decodeJSONObject[T any](writer http.ResponseWriter, request *http.Request, maxBytes int64) (T, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxJSONRequestBytes
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxBytes)
	return decodeStrictJSONObject[T](request.Body)
}

// decodeStrictJSONObject accepts exactly one non-null JSON object and rejects
// unknown fields. Behavior-changing request bodies use this instead of letting
// malformed input silently degrade to zero-value defaults.
func decodeStrictJSONObject[T any](reader io.Reader) (T, error) {
	var zero T
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	var decoded *T
	if err := decoder.Decode(&decoded); err != nil {
		return zero, err
	}
	if decoded == nil {
		return zero, errors.New("request body must be a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("request body must contain a single JSON object")
		}
		return zero, fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return *decoded, nil
}
