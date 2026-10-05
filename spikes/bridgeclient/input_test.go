package bridgeclient

import (
	"encoding/json"
	"strings"
	"testing"
)

func validHostInput() HostInputRequest {
	return HostInputRequest{RequestID: "r1", ActionID: "a1", Operation: "replace", PID: 123, BundleID: "com.sirerun.comuse.fixture", FixtureNonce: "fixture-1", ProcessStartRef: "p1", WindowRef: "w1", ElementRef: "e1", ExpectedStateID: strings.Repeat("a", 64), Text: "synthetic replacement"}
}

func TestHostInputSerializesTypedBoundRequest(t *testing.T) {
	data, err := marshalHostInputRequest(validHostInput())
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request["op"] != "replace" || request["request_id"] != "r1" || request["action_id"] != "a1" {
		t.Fatalf("request=%s", data)
	}
	if _, ok := request["approval"]; ok {
		t.Fatal("approval crossed native boundary")
	}
}

func TestHostInputRejectsInsertAndIncompleteIdentity(t *testing.T) {
	request := validHostInput()
	request.Operation = "insert"
	if _, err := marshalHostInputRequest(request); err == nil {
		t.Fatal("insert was accepted")
	}
	request = validHostInput()
	request.ExpectedStateID = ""
	if _, err := marshalHostInputRequest(request); err == nil {
		t.Fatal("missing state identity was accepted")
	}
}

func TestHostInputResponseRequiresExactIdentityAndExclusiveResultError(t *testing.T) {
	request := validHostInput()
	good := []byte(`{"schema_version":"fixture.v0","request_id":"r1","action_id":"a1","action":"replace","execution":"applied","error":null,"result":{}}`)
	if _, err := validateHostInputResponse(good, request); err != nil {
		t.Fatal(err)
	}
	bad := []byte(`{"schema_version":"fixture.v0","request_id":"wrong","action_id":"a1","action":"replace","execution":"applied","error":null,"result":{}}`)
	if _, err := validateHostInputResponse(bad, request); err == nil {
		t.Fatal("mismatched response accepted")
	}
	bad = []byte(`{"schema_version":"fixture.v0","request_id":"r1","action_id":"a1","action":"replace","execution":"not_applied","error":"denied","result":{}}`)
	if _, err := validateHostInputResponse(bad, request); err == nil {
		t.Fatal("error response with result accepted")
	}
}
