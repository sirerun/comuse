package main

import (
	"encoding/json"
	"errors"
	"fmt"
)

type response struct {
	SchemaVersion int     `json:"schema_version"`
	RequestID     string  `json:"request_id"`
	Status        string  `json:"status"`
	Result        *string `json:"result,omitempty"`
	Error         *string `json:"error,omitempty"`
}

func validateCompletionHandle(delivered, expected uint64) error {
	if delivered != expected {
		return fmt.Errorf("native callback handle mismatch: want %d, got %d", expected, delivered)
	}
	return nil
}

func parseHelloResponse(bytes []byte) (response, error) {
	out, err := parseTerminalResponse(bytes)
	if err != nil {
		return response{}, err
	}
	if out.Status != "completed" {
		return response{}, fmt.Errorf("native hello returned status %q", out.Status)
	}
	return out, nil
}

func parseTerminalResponse(bytes []byte) (response, error) {
	if len(bytes) > 64*1024 {
		return response{}, errors.New("native response exceeds 65536 bytes")
	}
	var out response
	if err := json.Unmarshal(bytes, &out); err != nil {
		return response{}, fmt.Errorf("decoding native response: %w", err)
	}
	if out.SchemaVersion != 1 || out.RequestID != "seamprobe-1" {
		return response{}, fmt.Errorf("native hello returned unexpected envelope: status=%q request_id=%q", out.Status, out.RequestID)
	}
	switch out.Status {
	case "completed":
		if out.Result == nil || *out.Result != "hello" || out.Error != nil {
			return response{}, errors.New("native hello returned an invalid completed envelope")
		}
		return out, nil
	case "cancelled":
		if out.Result != nil {
			return response{}, errors.New("native hello returned an invalid cancelled envelope")
		}
		return out, nil
	case "error":
		if out.Error == nil || *out.Error == "" {
			return response{}, errors.New("native hello returned an error status without a reason")
		}
		return response{}, fmt.Errorf("native hello failed: %s", *out.Error)
	default:
		return response{}, fmt.Errorf("native hello returned unexpected terminal status %q", out.Status)
	}
}

func resolveTerminalAfterCancel(bytes []byte, contextErr error, cancelStatus int32) (response, error) {
	out, err := parseTerminalResponse(bytes)
	if err != nil {
		return response{}, err
	}
	if out.Status == "completed" {
		return out, nil
	}
	if cancelStatus != 0 {
		return out, fmt.Errorf("cancel returned status %d; native request status %q: %w", cancelStatus, out.Status, contextErr)
	}
	return out, fmt.Errorf("native request status %q: %w", out.Status, contextErr)
}
