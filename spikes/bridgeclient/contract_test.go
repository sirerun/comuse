package bridgeclient

import "testing"

func TestValidateRequestBoundaries(t *testing.T) {
	valid := []byte(`{"schema_version":1,"request_id":"r1","op":"hello"}`)
	if _, err := validateRequest(valid); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	for _, request := range [][]byte{
		[]byte(`{"schema_version":2,"request_id":"r1","op":"hello"}`),
		[]byte(`{"schema_version":1,"request_id":"r1","op":"click"}`),
		[]byte(`{"schema_version":1,"request_id":"","op":"hello"}`),
		make([]byte, maxRequestBytes+1),
	} {
		if _, err := validateRequest(request); err == nil {
			t.Errorf("expected invalid request rejection: %q", request[:min(len(request), 80)])
		}
	}
}

func TestValidateResponsePreservesTerminalOutcomes(t *testing.T) {
	good := []byte(`{"schema_version":1,"request_id":"r1","status":"completed","error":null,"result":"hello"}`)
	if _, err := validateResponse(good, "r1"); err != nil {
		t.Fatalf("valid completed response: %v", err)
	}
	for _, response := range [][]byte{
		[]byte(`{"schema_version":1,"request_id":"r2","status":"completed","error":null,"result":"hello"}`),
		[]byte(`{"schema_version":1,"request_id":"r1","status":"cancelled","error":null,"result":null}`),
		[]byte(`{"schema_version":1,"request_id":"r1","status":"mystery","error":null,"result":null}`),
	} {
		if _, err := validateResponse(response, "r1"); err == nil {
			t.Errorf("expected invalid terminal response rejection: %s", response)
		}
	}
}

func TestValidatePartialResponseRequiresAndPreservesResult(t *testing.T) {
	partial := []byte(`{"schema_version":1,"request_id":"r1","status":"partial","error":null,"result":{"coverage":{"status":"partial"}}}`)
	response, err := validateResponse(partial, "r1")
	if err != nil {
		t.Fatalf("valid partial response: %v", err)
	}
	if response.Status != StatusPartial || string(response.Result) != `{"coverage":{"status":"partial"}}` {
		t.Fatalf("partial outcome was not preserved: status=%q result=%s", response.Status, response.Result)
	}
	missingResult := []byte(`{"schema_version":1,"request_id":"r1","status":"partial","error":null,"result":null}`)
	if _, err := validateResponse(missingResult, "r1"); err == nil {
		t.Fatal("partial response without a result was accepted")
	}
}
