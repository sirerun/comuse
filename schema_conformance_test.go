package comuse

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestActualDomainEnvelopeConformanceVectors(t *testing.T) {
	b := &fakeBackend{process: testProcess(), nativeState: "schema-fixture", elements: testElements()}
	s := newTestSession(t, b, false)
	if _, err := s.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	observed, err := s.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	testWireStateID := observed.StateID
	vectors := make(map[string]json.RawMessage)
	tests := []struct {
		name string
		op   Operation
		raw  string
	}{

		{"doctor", OperationDoctor, `{}`},
		{"state", OperationState, `{}`},
		{"windows", OperationWindows, `{}`},
		{"a11y", OperationObserve, `{"window_ref":"window-1"}`},
		{"read_element", OperationReadElement, `{"window_ref":"window-1","element_ref":"normal-1","state_id":"` + testWireStateID + `"}`},
		{"wait_legacy", OperationWait, `{"window_ref":"window-1","timeout_ms":30000}`},
		{"wait_appears", OperationWait, `{"condition":"window_appears","process_ref":"p","timeout_ms":1,"title":"App"}`},
		{"wait_closed", OperationWait, `{"condition":"window_closed","window_ref":"window-1","timeout_ms":1}`},
		{"wait_exists", OperationWait, `{"condition":"element_exists","window_ref":"window-1","element_ref":"normal-1","state_id":"` + testWireStateID + `","timeout_ms":1}`},
		{"wait_enabled_false", OperationWait, `{"condition":"element_enabled","window_ref":"window-1","element_ref":"normal-1","state_id":"` + testWireStateID + `","expected":false,"timeout_ms":1}`},
		{"wait_checked_true", OperationWait, `{"condition":"element_checked","window_ref":"window-1","element_ref":"normal-1","state_id":"` + testWireStateID + `","expected":true,"timeout_ms":1}`},
		{"ledger", OperationLedger, `{}`},
		{"click_element", OperationClickElement, `{"action_id":"a","window_ref":"window-1","element_ref":"normal-1","state_id":"` + testWireStateID + `"}`},
		{"element_action", OperationElementAction, `{"action_id":"a","window_ref":"window-1","element_ref":"normal-1","state_id":"` + testWireStateID + `","kind":"press"}`},
		{"write_replace_empty", OperationWriteElement, `{"action_id":"a","window_ref":"window-1","element_ref":"normal-1","state_id":"` + testWireStateID + `","mode":"replace","text":""}`},
		{"scroll_element", OperationScrollElement, `{"action_id":"a","window_ref":"window-1","element_ref":"normal-1","state_id":"` + testWireStateID + `","direction":"down","amount":"line"}`},
		{"click_zero_point", OperationClick, `{"action_id":"a","window_ref":"window-1","point":{"x":0,"y":0},"button":"left","count":1,"hold_ms":0}`},
		{"type_text", OperationTypeText, `{"action_id":"a","window_ref":"window-1","text":"x","delay_ms":0}`},
		{"press_key", OperationPressKey, `{"action_id":"a","window_ref":"window-1","keys":["a"],"hold_ms":0}`},
		{"scroll_zero_coordinate_nonzero_delta", OperationScroll, `{"action_id":"a","window_ref":"window-1","dx":1,"dy":0,"point":{"x":0,"y":0}}`},
		{"drag", OperationDrag, `{"action_id":"a","window_ref":"window-1","start":{"x":0,"y":0},"end":{"x":1,"y":1},"steps":2,"duration_ms":1}`},
		{"focus_window", OperationFocusWindow, `{"action_id":"a","window_ref":"window-1"}`},
	}
	for _, tc := range tests {
		request, err := DecodeRequest(tc.op, []byte(tc.raw))
		if err != nil {
			t.Fatalf("%s decode: %v", tc.name, err)
		}
		envelope, callErr := s.Call(context.Background(), request)
		if envelope.SchemaVersion != 1 {
			t.Fatalf("%s invalid envelope: %v", tc.name, callErr)
		}
		body, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		_, present := wire["action_id"]
		if present != request.mutation() {
			t.Fatalf("%s action_id presence=%t mutation=%t", tc.name, present, request.mutation())
		}
		vectors[tc.name] = body
		if request.mutation() && request.Operation != OperationClickElement && request.Operation != OperationElementAction && request.Operation != OperationWriteElement && request.Operation != OperationScrollElement {
			qualified, _ := newRawTestSession(t, allRawKinds(), nil)
			applied, err := qualified.Call(context.Background(), request)
			if err != nil || applied.Execution != "applied" {
				t.Fatalf("%s qualified source: %v", tc.name, err)
			}
			vectors[tc.name+"_applied"], err = json.Marshal(applied)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := qualified.Call(context.Background(), request)
			if err != nil || replayed.Execution != "applied" || replayed.Usage.Actions != 0 {
				t.Fatalf("%s replay: %v", tc.name, err)
			}
			vectors[tc.name+"_replay"], err = json.Marshal(replayed)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	rejected, err := RejectionEnvelope("invalid_request")
	if err != nil {
		t.Fatal(err)
	}
	vectors["transport_rejection"], err = json.Marshal(rejected)
	if err != nil {
		t.Fatal(err)
	}
	// Conformance must cover available inspected state, not only the legacy
	// backend's unavailable compact-context branch.
	contextBackend := &desktopContextBackend{fakeBackend: &fakeBackend{process: testProcess(), nativeState: "context-schema", elements: testElements()}}
	contextBackend.setDesktop(testDesktopContext(testProcess()))
	contextSession := newTestSession(t, contextBackend.fakeBackend, false)
	contextSession.backend = contextBackend
	appendContextVector := func(name string, request Request) {
		t.Helper()
		envelope, err := contextSession.Call(context.Background(), request)
		if err != nil || envelope.StateStatus != string(StateAvailable) {
			t.Fatalf("%s inspected context: status=%s err=%v", name, envelope.StateStatus, err)
		}
		vectors[name], err = json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
	}
	appendContextVector("doctor_inspected_context", Request{Operation: OperationDoctor})
	appendContextVector("state_inspected_context", Request{Operation: OperationState})
	if _, err := contextSession.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	appendContextVector("a11y_inspected_context", Request{Operation: OperationObserve, Observe: &ObserveParams{WindowRef: "window-1", Mode: "full"}})
	noFocus := testDesktopContext(testProcess())
	noFocus.FocusedWindow = nil
	contextBackend.setDesktop(noFocus)
	appendContextVector("state_inspected_no_focus", Request{Operation: OperationState})
	if destination := os.Getenv("COMUSE_SCHEMA_FIXTURE_OUTPUT"); destination != "" {
		body, err := json.Marshal(vectors)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
