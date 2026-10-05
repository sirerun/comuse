package inputprobe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/sirerun/comuse/spikes/bridgeclient"
	"github.com/sirerun/comuse/spikes/internal/hostcap"
)

type nativeBridgeClient interface {
	Call(context.Context, []byte) ([]byte, error)
	InputCall(context.Context, bridgeclient.HostInputRequest, hostcap.Capability) ([]byte, error)
	Close(context.Context) error
}

// BridgeBackend binds a same-runtime readonly AX preflight to the typed host input transport.
type BridgeBackend struct {
	client     nativeBridgeClient
	capability hostcap.Capability
}

func NewBridgeBackend(client nativeBridgeClient) (*BridgeBackend, error) {
	if client == nil {
		return nil, errors.New("native bridge client is required")
	}
	return &BridgeBackend{client: client, capability: hostcap.New()}, nil
}

func (backend *BridgeBackend) Inspect(ctx context.Context, scope NativeTargetRequest) (NativeClassification, error) {
	var classification NativeClassification
	if backend == nil || backend.client == nil {
		return classification, errors.New("native bridge client is unavailable")
	}
	identity, _ := json.Marshal(scope)
	hash := sha256.Sum256(identity)
	requestID := "input-preflight-" + hex.EncodeToString(hash[:8])
	wire := map[string]any{
		"schema_version": 1, "request_id": requestID, "op": "a11y", "include_values": false,
		"scope": map[string]any{
			"pid": scope.PID, "bundle_id": scope.BundleID,
			"fixture_nonce": scope.FixtureNonce, "window_ref": scope.WindowRef,
		},
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return classification, err
	}
	responseData, err := backend.client.Call(ctx, data)
	if err != nil {
		return classification, err
	}
	var response struct {
		SchemaVersion int             `json:"schema_version"`
		RequestID     string          `json:"request_id"`
		Status        string          `json:"status"`
		Error         json.RawMessage `json:"error"`
		Result        struct {
			ProcessStartRef string `json:"process_start_ref"`
			WindowRef       string `json:"window_ref"`
			Coverage        struct {
				Status    string `json:"status"`
				Truncated bool   `json:"truncated"`
			} `json:"coverage"`
			Elements []struct {
				Ref        string `json:"ref"`
				Role       string `json:"role"`
				Identifier string `json:"identifier"`
				InputClass string `json:"input_classification"`
			} `json:"elements"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseData, &response); err != nil {
		return classification, errors.New("native preflight response is malformed")
	}
	if response.SchemaVersion != 1 || response.RequestID != requestID || response.Status != "completed" || len(response.Error) == 0 || string(response.Error) != "null" || response.Result.Coverage.Status != "complete" || response.Result.Coverage.Truncated {
		return classification, errors.New("native preflight is partial, failed, or mismatched")
	}
	if response.Result.ProcessStartRef != scope.ProcessStartRef || response.Result.WindowRef != scope.WindowRef {
		return classification, errors.New("native preflight process/window identity mismatch")
	}
	for _, element := range response.Result.Elements {
		if element.Ref == scope.ElementRef {
			classification = NativeClassification{Complete: true, ProcessStartRef: response.Result.ProcessStartRef, WindowRef: response.Result.WindowRef, ElementRef: element.Ref, Role: element.Role, Identifier: element.Identifier, InputClass: element.InputClass}
			return classification, nil
		}
	}
	return classification, errors.New("native preflight did not observe the exact requested element")
}

func (backend *BridgeBackend) inputCall(ctx context.Context, request bridgeclient.HostInputRequest) ([]byte, error) {
	if backend == nil || backend.client == nil {
		return nil, errors.New("native bridge client is unavailable")
	}
	return backend.client.InputCall(ctx, request, backend.capability)
}
