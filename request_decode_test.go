package comuse

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDecodeRequestAllOperations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		op   Operation
		raw  string
	}{
		{"doctor", OperationDoctor, `{}`},
		{"state", OperationState, `{}`},
		{"windows", OperationWindows, `{}`},
		{"a11y", OperationObserve, `{"window_ref":"w"}`},
		{"read_element", OperationReadElement, `{"window_ref":"w","element_ref":"e","state_id":"` + testWireStateID + `"}`},
		{"wait_legacy", OperationWait, `{"window_ref":"w","timeout_ms":30000}`},
		{"wait_appears", OperationWait, `{"condition":"window_appears","process_ref":"p","timeout_ms":1,"title":"App"}`},
		{"wait_closed", OperationWait, `{"condition":"window_closed","window_ref":"w","timeout_ms":1}`},
		{"wait_exists", OperationWait, `{"condition":"element_exists","window_ref":"w","element_ref":"e","state_id":"` + testWireStateID + `","timeout_ms":1}`},
		{"wait_enabled_false", OperationWait, `{"condition":"element_enabled","window_ref":"w","element_ref":"e","state_id":"` + testWireStateID + `","expected":false,"timeout_ms":1}`},
		{"wait_checked_true", OperationWait, `{"condition":"element_checked","window_ref":"w","element_ref":"e","state_id":"` + testWireStateID + `","expected":true,"timeout_ms":1}`},
		{"ledger", OperationLedger, `{}`},
		{"click_element", OperationClickElement, `{"action_id":"a","window_ref":"w","element_ref":"e","state_id":"` + testWireStateID + `"}`},
		{"element_action", OperationElementAction, `{"action_id":"a","window_ref":"w","element_ref":"e","state_id":"` + testWireStateID + `","kind":"press"}`},
		{"write_replace_empty", OperationWriteElement, `{"action_id":"a","window_ref":"w","element_ref":"e","state_id":"` + testWireStateID + `","mode":"replace","text":""}`},
		{"scroll_element", OperationScrollElement, `{"action_id":"a","window_ref":"w","element_ref":"e","state_id":"` + testWireStateID + `","direction":"down","amount":"line"}`},
		{"click_zero_point", OperationClick, `{"action_id":"a","window_ref":"w","point":{"x":0,"y":0},"button":"left","count":1,"hold_ms":0}`},
		{"type_text", OperationTypeText, `{"action_id":"a","window_ref":"w","text":"x","delay_ms":0}`},
		{"press_key", OperationPressKey, `{"action_id":"a","window_ref":"w","keys":["a"],"hold_ms":0}`},
		{"scroll_zero_coordinate_nonzero_delta", OperationScroll, `{"action_id":"a","window_ref":"w","dx":1,"dy":0,"point":{"x":0,"y":0}}`},
		{"drag", OperationDrag, `{"action_id":"a","window_ref":"w","start":{"x":0,"y":0},"end":{"x":1,"y":1},"steps":2,"duration_ms":1}`},
		{"focus_window", OperationFocusWindow, `{"action_id":"a","window_ref":"w"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := DecodeRequest(tc.op, []byte(tc.raw))
			if err != nil {
				t.Fatalf("DecodeRequest: %v", err)
			}
			if r.Operation != tc.op {
				t.Fatalf("operation = %q, want %q", r.Operation, tc.op)
			}
			if err := r.Validate(); err != nil {
				t.Fatalf("decoded request does not validate: %v", err)
			}
		})
	}
}

const testWireStateID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestDecodeRequestRejectsMalformedOrUnauthorizedInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		op   Operation
		raw  []byte
	}{
		{"unknown operation", Operation("unknown"), []byte(`{}`)},
		{"wrong top-level shape", OperationDoctor, []byte(`[]`)},
		{"trailing value", OperationDoctor, []byte(`{} {}`)},
		{"duplicate field", OperationObserve, []byte(`{"window_ref":"w","window_ref":"x"}`)},
		{"case alias", OperationObserve, []byte(`{"Window_ref":"w"}`)},
		{"missing required", OperationObserve, []byte(`{}`)},
		{"wrong field shape", OperationReadElement, []byte(`{"window_ref":1,"element_ref":"e","state_id":"` + testWireStateID + `"}`)},
		{"null optional", OperationObserve, []byte(`{"window_ref":"w","mode":null}`)},
		{"unknown field", OperationDoctor, []byte(`{"surprise":1}`)},
		{"operation injection", OperationDoctor, []byte(`{"operation":"click"}`)},
		{"confirmation authority", OperationClick, []byte(`{"action_id":"a","window_ref":"w","point":{"x":1,"y":2},"button":"left","count":1,"hold_ms":0,"confirmation":"yes"}`)},
		{"approval authority", OperationFocusWindow, []byte(`{"action_id":"a","window_ref":"w","approval":"yes"}`)},
		{"writer authority", OperationFocusWindow, []byte(`{"action_id":"a","window_ref":"w","writer":"x"}`)},
		{"config authority", OperationFocusWindow, []byte(`{"action_id":"a","window_ref":"w","config":{}}`)},
		{"key authority", OperationFocusWindow, []byte(`{"action_id":"a","window_ref":"w","key":"x"}`)},
		{"library authority", OperationFocusWindow, []byte(`{"action_id":"a","window_ref":"w","library":"x"}`)},
		{"native reference", OperationFocusWindow, []byte(`{"action_id":"a","window_ref":"w","native_ref":"x"}`)},
		{"pid authority", OperationWait, []byte(`{"condition":"window_appears","timeout_ms":1,"process_ref":"p","pid":1}`)},
		{"scope authority", OperationFocusWindow, []byte(`{"action_id":"a","window_ref":"w","scope":"x"}`)},
		{"environment authority", OperationFocusWindow, []byte(`{"action_id":"a","window_ref":"w","environment":"x"}`)},
		{"oversized", OperationDoctor, []byte("{" + strings.Repeat(" ", maxRequestBytes) + "}")},
		{"invalid utf8", OperationDoctor, []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}},
		{"unpaired high surrogate", OperationObserve, []byte(`{"window_ref":"\uD800"}`)},
		{"unpaired low surrogate", OperationObserve, []byte(`{"window_ref":"\uDC00"}`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeRequest(tc.op, tc.raw); err == nil {
				t.Fatalf("DecodeRequest(%q, %s) accepted invalid input", tc.op, tc.raw)
			} else {
				var safe *Error
				if !errors.As(err, &safe) || safe.Code != "invalid_request" || safe.Message != "invalid_request" {
					t.Fatalf("error is not safe invalid_request: %T %v", err, err)
				}
			}
		})
	}
}

func TestDecodeRequestChecksRequiredPresenceAndOptionalDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		op   Operation
		raw  string
	}{
		{"missing action id", OperationClickElement, `{"window_ref":"w","element_ref":"e","state_id":"` + testWireStateID + `"}`},
		{"missing point x", OperationClick, `{"action_id":"a","window_ref":"w","point":{"y":0},"button":"left","count":1,"hold_ms":0}`},
		{"missing point y", OperationClick, `{"action_id":"a","window_ref":"w","point":{"x":0},"button":"left","count":1,"hold_ms":0}`},
		{"null point coordinate", OperationClick, `{"action_id":"a","window_ref":"w","point":{"x":null,"y":0},"button":"left","count":1,"hold_ms":0}`},
		{"missing drag start coordinate", OperationDrag, `{"action_id":"a","window_ref":"w","start":{"y":0},"end":{"x":1,"y":1},"steps":2,"duration_ms":1}`},
		{"omitted expected", OperationWait, `{"condition":"element_enabled","window_ref":"w","element_ref":"e","state_id":"` + testWireStateID + `","timeout_ms":1}`},
		{"explicit zero poll", OperationWait, `{"condition":"window_closed","window_ref":"w","timeout_ms":1,"poll_interval_ms":0}`},
		{"poll forbidden on legacy wait", OperationWait, `{"window_ref":"w","timeout_ms":1,"poll_interval_ms":100}`},
		{"empty explicit mode", OperationObserve, `{"window_ref":"w","mode":""}`},
		{"empty explicit title", OperationWait, `{"condition":"window_appears","process_ref":"p","timeout_ms":1,"title":""}`},
		{"explicit empty state id", OperationObserve, `{"window_ref":"w","state_id":""}`},
		{"forbidden wait field despite empty value", OperationWait, `{"condition":"window_closed","window_ref":"w","timeout_ms":1,"process_ref":""}`},
		{"write missing text", OperationWriteElement, `{"action_id":"a","window_ref":"w","element_ref":"e","state_id":"` + testWireStateID + `","mode":"replace"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeRequest(tc.op, []byte(tc.raw)); err == nil {
				t.Fatalf("accepted invalid input: %s", tc.raw)
			}
		})
	}
	for _, op := range []Operation{OperationDoctor, OperationState, OperationWindows, OperationLedger} {
		t.Run("empty raw default/"+string(op), func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeRequest(op, nil); err != nil {
				t.Fatalf("empty raw should normalize to {}: %v", err)
			}
		})
	}
	if _, err := DecodeRequest(OperationObserve, nil); err == nil {
		t.Fatal("empty raw accepted for nonempty operation")
	}
}

func TestDecodeRequestKeepsValidSurrogatePairsAndFalsePresence(t *testing.T) {
	t.Parallel()
	r, err := DecodeRequest(OperationWait, []byte(`{"condition":"element_enabled","window_ref":"w","element_ref":"e","state_id":"`+testWireStateID+`","expected":false,"timeout_ms":1}`))
	if err != nil || r.Wait.Expected == nil || *r.Wait.Expected {
		t.Fatalf("false expected presence lost: request=%+v err=%v", r.Wait, err)
	}
	r, err = DecodeRequest(OperationWait, []byte(`{"condition":"window_appears","process_ref":"p","timeout_ms":1,"title":"\uD83D\uDE00"}`))
	if err != nil || r.Wait.Title != "😀" {
		t.Fatalf("valid surrogate pair rejected or changed: request=%+v err=%v", r.Wait, err)
	}
}

func TestDecodeRequestReturnsNoPartiallyDecodedRequestOnFailure(t *testing.T) {
	r, err := DecodeRequest(OperationObserve, json.RawMessage(`{"window_ref":"w","mode":"invalid"}`))
	if err == nil || r.Operation != "" || r.Observe != nil {
		t.Fatalf("failure returned partial request: %+v %v", r, err)
	}
}
