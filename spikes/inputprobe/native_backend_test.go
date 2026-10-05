package inputprobe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sirerun/comuse/spikes/bridgeclient"
	"github.com/sirerun/comuse/spikes/internal/hostcap"
)

type inspectOnlyClient struct {
	request map[string]any
}

func (client *inspectOnlyClient) Call(_ context.Context, request []byte) ([]byte, error) {
	if err := json.Unmarshal(request, &client.request); err != nil {
		return nil, err
	}
	requestID := client.request["request_id"]
	response := map[string]any{
		"schema_version": 1, "request_id": requestID, "status": "completed", "error": nil,
		"result": map[string]any{
			"state_id":          "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"process_start_ref": "process-1", "window_ref": "window-1",
			"coverage": map[string]any{"status": "complete", "truncated": false},
			"elements": []any{map[string]any{
				"ref": "element-1", "role": "AXTextField", "identifier": "textfield",
				"input_classification": "fixture_normal_text_field",
			}},
		},
	}
	return json.Marshal(response)
}

func (*inspectOnlyClient) InputCall(context.Context, bridgeclient.HostInputRequest, hostcap.Capability) ([]byte, error) {
	return nil, nil
}

func TestInspectRequestsMetadataOnlyAndDoesNotBindProjectionState(t *testing.T) {
	client := &inspectOnlyClient{}
	backend, err := NewBridgeBackend(client)
	if err != nil {
		t.Fatal(err)
	}
	classification, err := backend.Inspect(context.Background(), NativeTargetRequest{
		PID: 123, BundleID: "com.sirerun.comuse.fixture", FixtureNonce: "nonce-1",
		ProcessStartRef: "process-1", WindowRef: "window-1", ElementRef: "element-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.request["include_values"] != false {
		t.Fatalf("preflight must not request values: %#v", client.request)
	}
	if !classification.Complete || classification.ProcessStartRef != "process-1" ||
		classification.WindowRef != "window-1" || classification.ElementRef != "element-1" ||
		classification.InputClass != "fixture_normal_text_field" {
		t.Fatalf("classification=%+v", classification)
	}
}
