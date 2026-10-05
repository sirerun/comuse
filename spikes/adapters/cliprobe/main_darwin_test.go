//go:build darwin && cgo

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClient struct {
	mu          sync.Mutex
	requests    []map[string]any
	responses   [][]byte
	callErr     error
	callDelay   time.Duration
	closed      bool
	pumps       int
	closeBudget time.Duration
}

func (client *fakeClient) Call(_ context.Context, payload []byte) ([]byte, error) {
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	client.mu.Lock()
	client.requests = append(client.requests, request)
	if len(client.responses) == 0 {
		client.mu.Unlock()
		return nil, io.EOF
	}
	response := client.responses[0]
	client.responses = client.responses[1:]
	callErr, callDelay := client.callErr, client.callDelay
	client.mu.Unlock()
	if callDelay > 0 {
		time.Sleep(callDelay)
	}
	return response, callErr
}
func (client *fakeClient) Pump(time.Duration) error {
	client.mu.Lock()
	client.pumps++
	client.mu.Unlock()
	return nil
}
func (client *fakeClient) Close(ctx context.Context) error {
	client.mu.Lock()
	client.closed = true
	if deadline, ok := ctx.Deadline(); ok {
		client.closeBudget = time.Until(deadline)
	}
	client.mu.Unlock()
	return nil
}

func writeConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cliprobe.json")
	contents := `{"library_path":"/tmp/libBridge.dylib","fixture":{"pid":123,"bundle_id":"com.sirerun.comuse.fixture","nonce":"nonce-1"}}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHelloUsesTrustedConfigAndBoundedJSONOutput(t *testing.T) {
	client := &fakeClient{responses: [][]byte{[]byte(`{"schema_version":1,"request_id":"cliprobe-1","status":"completed","result":{"version":"v1"}}`)}}
	var stdout, stderr strings.Builder
	opened := ""
	status := run([]string{"--config", writeConfig(t), "hello"}, &stdout, &stderr, func(path string) (nativeClient, error) { opened = path; return client, nil })
	if status != exitOK || opened != "/tmp/libBridge.dylib" || !client.closed || !strings.Contains(stdout.String(), `"status":"completed"`) || stderr.Len() != 0 {
		t.Fatalf("status=%d opened=%q closed=%v stdout=%q stderr=%q", status, opened, client.closed, stdout.String(), stderr.String())
	}
}

func TestA11yResolvesSelectedWindowInsideOneRuntime(t *testing.T) {
	client := &fakeClient{responses: [][]byte{
		[]byte(`{"schema_version":1,"request_id":"cliprobe-windows","status":"completed","result":{"process_start_ref":"process-ref","windows":[{"ref":"opaque-window"}]}}`),
		[]byte(`{"schema_version":1,"request_id":"cliprobe-a11y","status":"completed","result":{"observation_id":"obs-1","state_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","process_start_ref":"process-ref","window_ref":"opaque-window","root_refs":["root"],"elements":[{"ref":"root","role":"AXWindow","value_status":"omitted","child_refs":[],"parent_ref":null}],"coverage":{"status":"complete","reason":null,"depth_limit":16,"node_limit":256,"text_byte_limit":16384,"deadline_ms":250,"visited":1,"text_bytes":0,"truncated":false}}}`),
	}}
	var stdout, stderr strings.Builder
	status := run([]string{"--config", writeConfig(t), "--window-index", "0", "--include-values", "a11y"}, &stdout, &stderr, func(string) (nativeClient, error) { return client, nil })
	if status != exitOK || !client.closed || len(client.requests) != 2 || client.requests[1]["window_ref"] != "opaque-window" || client.requests[1]["include_values"] != true || !strings.Contains(stdout.String(), `"canonical_state_id"`) {
		t.Fatalf("status=%d closed=%v requests=%#v stdout=%q stderr=%q", status, client.closed, client.requests, stdout.String(), stderr.String())
	}
}

func TestPartialWindowEnumerationIsEmittedWithPartialExitAndNoFollowup(t *testing.T) {
	partial := []byte(`{"schema_version":1,"request_id":"cliprobe-windows","status":"partial","result":{"windows":[]}}`)
	client := &fakeClient{responses: [][]byte{partial}}
	var stdout, stderr strings.Builder
	status := run([]string{"--config", writeConfig(t), "--window-index", "0", "a11y"}, &stdout, &stderr, func(string) (nativeClient, error) { return client, nil })
	if status != exitPartial || len(client.requests) != 1 || !strings.Contains(stdout.String(), `"status":"partial"`) {
		t.Fatalf("status=%d requests=%d stdout=%q stderr=%q", status, len(client.requests), stdout.String(), stderr.String())
	}
}

func TestCommandCannotOverrideFixedFixtureScope(t *testing.T) {
	var stdout, stderr strings.Builder
	opened := false
	status := run([]string{"--config", writeConfig(t), "--pid", "999", "windows"}, &stdout, &stderr, func(string) (nativeClient, error) { opened = true; return nil, nil })
	if status != exitUsage || opened || stdout.Len() != 0 {
		t.Fatalf("status=%d opened=%v stdout=%q stderr=%q", status, opened, stdout.String(), stderr.String())
	}
}

func TestMalformedNativeBytesWithErrorAreNotAccepted(t *testing.T) {
	for _, response := range [][]byte{
		[]byte(`{"schema_version":1,"request_id":"wrong-id","status":"completed","result":{}}`),
		[]byte(`{"schema_version":1,"request_id":"cliprobe-1","status":"completed","result":`),
	} {
		client := &fakeClient{responses: [][]byte{response}, callErr: errors.New("native validation failure")}
		var stdout, stderr strings.Builder
		status := run([]string{"--config", writeConfig(t), "hello"}, &stdout, &stderr, func(string) (nativeClient, error) { return client, nil })
		if status != exitNative || stdout.Len() != 0 {
			t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
		}
	}
}

func TestCloseGetsFreshBudgetAfterSlowOperation(t *testing.T) {
	client := &fakeClient{
		responses: [][]byte{[]byte(`{"schema_version":1,"request_id":"cliprobe-1","status":"completed","result":{}}`)},
		callDelay: 2100 * time.Millisecond,
	}
	var stdout, stderr strings.Builder
	status := run([]string{"--config", writeConfig(t), "hello"}, &stdout, &stderr, func(string) (nativeClient, error) { return client, nil })
	if status != exitOK || client.closeBudget < time.Second {
		t.Fatalf("status=%d close_budget=%s stderr=%q", status, client.closeBudget, stderr.String())
	}
}

func TestMultipleFixtureWindowsAreRejectedAsAmbiguous(t *testing.T) {
	client := &fakeClient{responses: [][]byte{[]byte(`{"schema_version":1,"request_id":"cliprobe-windows","status":"completed","result":{"process_start_ref":"process-ref","windows":[{"ref":"first"},{"ref":"second"}]}}`)}}
	var stdout, stderr strings.Builder
	status := run([]string{"--config", writeConfig(t), "--window-index", "0", "a11y"}, &stdout, &stderr, func(string) (nativeClient, error) { return client, nil })
	if status != exitNative || len(client.requests) != 1 || stdout.Len() != 0 {
		t.Fatalf("status=%d requests=%d stdout=%q stderr=%q", status, len(client.requests), stdout.String(), stderr.String())
	}
}
