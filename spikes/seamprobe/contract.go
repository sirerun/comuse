package main

import (
	"encoding/json"
	"errors"
	"fmt"
)

type request struct {
	SchemaVersion int    `json:"schema_version"`
	RequestID     string `json:"request_id"`
	Op            string `json:"op"`
}
type response struct {
	SchemaVersion int     `json:"schema_version"`
	RequestID     string  `json:"request_id"`
	Status        string  `json:"status"`
	Result        *string `json:"result,omitempty"`
	Error         *string `json:"error,omitempty"`
}

func parseHelloResponse(bytes []byte) (response, error) {
	if len(bytes) > 64*1024 {
		return response{}, errors.New("native response exceeds 65536 bytes")
	}
	var out response
	if err := json.Unmarshal(bytes, &out); err != nil {
		return response{}, fmt.Errorf("decoding native response: %w", err)
	}
	if out.SchemaVersion != 1 || out.Status != "completed" || out.RequestID != "seamprobe-1" || out.Result == nil || *out.Result != "hello" {
		return response{}, fmt.Errorf("native hello returned unexpected envelope: status=%q request_id=%q", out.Status, out.RequestID)
	}
	return out, nil
}
