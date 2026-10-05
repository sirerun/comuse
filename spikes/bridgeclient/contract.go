// Package bridgeclient is the spike-only Go owner for BridgeProbe's versioned C ABI.
package bridgeclient

import (
	"encoding/json"
	"errors"
	"fmt"
)

type requestEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	RequestID     string `json:"request_id"`
	Op            string `json:"op"`
}

type responseEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Status        string          `json:"status"`
	Result        json.RawMessage `json:"result"`
	Error         json.RawMessage `json:"error"`
}

func validateRequest(data []byte) (requestEnvelope, error) {
	if len(data) == 0 || len(data) > 32*1024 {
		return requestEnvelope{}, errors.New("request must contain at most 32768 bytes")
	}
	var request requestEnvelope
	if err := json.Unmarshal(data, &request); err != nil {
		return requestEnvelope{}, fmt.Errorf("decoding native request: %w", err)
	}
	if request.SchemaVersion != 1 || request.RequestID == "" {
		return requestEnvelope{}, errors.New("request requires schema_version 1 and a nonempty request_id")
	}
	switch request.Op {
	case "hello", "doctor", "windows", "a11y":
		return request, nil
	default:
		return requestEnvelope{}, fmt.Errorf("unsupported spike operation %q", request.Op)
	}
}

func validateResponse(data []byte, requestID string) (responseEnvelope, error) {
	if len(data) == 0 || len(data) > 64*1024 {
		return responseEnvelope{}, errors.New("native response must contain at most 65536 bytes")
	}
	var response responseEnvelope
	if err := json.Unmarshal(data, &response); err != nil {
		return responseEnvelope{}, fmt.Errorf("decoding native response: %w", err)
	}
	if response.SchemaVersion != 1 || response.RequestID != requestID {
		return responseEnvelope{}, fmt.Errorf("native response identity mismatch: schema_version=%d request_id=%q", response.SchemaVersion, response.RequestID)
	}
	switch response.Status {
	case "completed":
		if len(response.Result) == 0 || string(response.Result) == "null" {
			return responseEnvelope{}, errors.New("completed native response has no result")
		}
	case "cancelled", "error":
		if len(response.Error) == 0 || string(response.Error) == "null" {
			return responseEnvelope{}, fmt.Errorf("native %s response has no error", response.Status)
		}
	default:
		return responseEnvelope{}, fmt.Errorf("unexpected native terminal status %q", response.Status)
	}
	return response, nil
}

// NativeError preserves the terminal native status while allowing callers to inspect cancellation.
type NativeError struct {
	Status string
	Detail string
}

func (e *NativeError) Error() string {
	if e.Detail == "" {
		return "native request " + e.Status
	}
	return "native request " + e.Status + ": " + e.Detail
}
