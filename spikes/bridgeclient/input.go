package bridgeclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// HostInputRequest is a typed, trusted-host-only command. It deliberately has no raw JSON or approval field.
type HostInputRequest struct {
	RequestID, ActionID, Operation                                                  string
	PID                                                                             int32
	BundleID, FixtureNonce, ProcessStartRef, WindowRef, ElementRef, ExpectedStateID string
	Text                                                                            string
}

type inputWireRequest struct {
	SchemaVersion int    `json:"schema_version"`
	RequestID     string `json:"request_id"`
	ActionID      string `json:"action_id"`
	Op            string `json:"op"`
	Scope         struct {
		PID             int32  `json:"pid"`
		BundleID        string `json:"bundle_id"`
		FixtureNonce    string `json:"fixture_nonce"`
		ProcessStartRef string `json:"process_start_ref"`
		WindowRef       string `json:"window_ref"`
		ElementRef      string `json:"element_ref"`
		ExpectedStateID string `json:"expected_state_id"`
	} `json:"scope"`
	Text string `json:"text,omitempty"`
}

type inputResponseEnvelope struct {
	SchemaVersion string          `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	ActionID      string          `json:"action_id"`
	Action        string          `json:"action"`
	Execution     string          `json:"execution"`
	Error         json.RawMessage `json:"error"`
	Result        json.RawMessage `json:"result"`
}

func marshalHostInputRequest(request HostInputRequest) ([]byte, error) {
	if request.RequestID == "" || request.ActionID == "" || request.PID <= 0 || request.BundleID != "com.sirerun.comuse.fixture" || !validFixtureNonce(request.FixtureNonce) || request.ProcessStartRef == "" || request.WindowRef == "" || request.ElementRef == "" || !validStateID(request.ExpectedStateID) {
		return nil, errors.New("host input requires exact fixture process, nonce, window, element, and state identity")
	}
	switch request.Operation {
	case "read_value", "replace", "press":
	default:
		return nil, fmt.Errorf("unsupported host input operation %q", request.Operation)
	}
	if request.Operation == "replace" && len(request.Text) > 4096 {
		return nil, errors.New("replacement text exceeds 4096 bytes")
	}
	wire := inputWireRequest{SchemaVersion: 1, RequestID: request.RequestID, ActionID: request.ActionID, Op: request.Operation, Text: request.Text}
	wire.Scope.PID, wire.Scope.BundleID, wire.Scope.FixtureNonce = request.PID, request.BundleID, request.FixtureNonce
	wire.Scope.ProcessStartRef, wire.Scope.WindowRef = request.ProcessStartRef, request.WindowRef
	wire.Scope.ElementRef, wire.Scope.ExpectedStateID = request.ElementRef, request.ExpectedStateID
	data, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	if len(data) > maxRequestBytes {
		return nil, errors.New("host input request exceeds native request limit")
	}
	return data, nil
}

func validateHostInputResponse(data []byte, request HostInputRequest) (inputResponseEnvelope, error) {
	var response inputResponseEnvelope
	if len(data) == 0 || len(data) > maxResponseBytes {
		return response, errors.New("native input response size is invalid")
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return response, fmt.Errorf("decode native input response: %w", err)
	}
	if response.SchemaVersion != "fixture.v0" || response.RequestID != request.RequestID || response.ActionID != request.ActionID || response.Action != request.Operation {
		return response, errors.New("native input response identity mismatch")
	}
	switch response.Execution {
	case "applied", "not_applied", "partial", "unknown":
	default:
		return response, errors.New("native input execution state is invalid")
	}
	if len(response.Error) == 0 || string(response.Error) == "null" {
		if len(response.Result) == 0 || string(response.Result) == "null" {
			return response, errors.New("native input success response has no result")
		}
	} else {
		var code string
		if err := json.Unmarshal(response.Error, &code); err != nil || strings.TrimSpace(code) == "" || (len(response.Result) != 0 && string(response.Result) != "null") {
			return response, errors.New("native input error/result fields are invalid")
		}
	}
	return response, nil
}

func validFixtureNonce(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		if r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validStateID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' {
			continue
		}
		return false
	}
	return true
}
