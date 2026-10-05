package mcpprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirerun/comuse/spikes/semanticprobe"
)

type fakeBackend struct {
	windowRef     string
	nativeStateID string
	started       chan struct{}
	cancelled     chan struct{}
	a11yCalls     atomic.Int32
}

func (backend *fakeBackend) Hello(_ context.Context, scope Scope) (HelloResult, error) {
	if scope.FixturePID != 42 || scope.FixtureBundleID != FixtureBundleID || scope.FixtureNonce != "fixture-1" {
		return HelloResult{}, errors.New("host scope was not fixed")
	}
	return HelloResult{Status: "complete", Message: "hello"}, nil
}

func (*fakeBackend) Doctor(context.Context, Scope) (DoctorResult, error) {
	return DoctorResult{Status: "complete", Checks: []Check{
		{Name: "accessibility", Status: "unavailable"},
		{Name: "event_posting", Status: "unavailable"},
	}}, nil
}

func (backend *fakeBackend) Windows(_ context.Context, _ Scope) (WindowObservation, error) {
	return WindowObservation{
		Status: "complete", ObservationID: "windows-observation-1", ProcessStartRef: "process-ref-1",
		Windows: []Window{{WindowRef: backend.windowRef, Role: "AXWindow"}},
	}, nil
}

func (backend *fakeBackend) Accessibility(ctx context.Context, _ Scope, processRef, windowRef string, includeValues bool) (semanticprobe.Snapshot, error) {
	backend.a11yCalls.Add(1)
	if backend.started != nil {
		select {
		case backend.started <- struct{}{}:
		default:
		}
	}
	if backend.cancelled != nil {
		<-ctx.Done()
		close(backend.cancelled)
		return semanticprobe.Snapshot{}, ctx.Err()
	}
	if processRef != "process-ref-1" || windowRef != backend.windowRef {
		return semanticprobe.Snapshot{}, errors.New("stale backend scope")
	}
	return semanticprobe.Snapshot{
		RequestID: "request-1", ObservationID: "observation-1", Status: semanticprobe.SnapshotComplete,
		BundleID: FixtureBundleID, FixtureNonce: "fixture-1", ProcessStartRef: processRef,
		WindowRef: windowRef, RootRefs: []string{}, Elements: []semanticprobe.Element{}, NativeStateID: backend.nativeStateID,
		Coverage: semanticprobe.Coverage{Status: "complete"}, CanonicalStateID: "canonical-1",
	}, nil
}

func TestCompleteA11yProjectionDoesNotRequireOrExposeNativeStateID(t *testing.T) {
	const nativeID = "private-native-precondition-canary"
	backend := &fakeBackend{windowRef: "window-ref-1", nativeStateID: nativeID}
	session, _ := startPair(t, backend, ProtocolVersion)
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "windows"}); err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "a11y", Arguments: map[string]any{"window_ref": "window-ref-1"}})
	if err != nil || result.IsError {
		t.Fatalf("complete read-only projection rejected: result=%v err=%v", result, err)
	}
	text := resultText(result)
	if !strings.Contains(text, `"canonical_state_id":"canonical-1"`) || strings.Contains(text, nativeID) {
		t.Fatalf("canonical projection missing or native precondition leaked: %s", text)
	}
}

func testConfig() Config {
	return Config{
		NativeLibraryPath: "/tmp/libBridgeProbe.dylib", FixturePID: 42,
		FixtureBundleID: FixtureBundleID, FixtureNonce: "fixture-1",
		ProcessLaunchGeneration: "launch-1",
	}
}

func startPair(t *testing.T, backend Backend, protocolVersion string) (*mcp.ClientSession, *Server) {
	t.Helper()
	server, err := New(testConfig(), backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	options := &mcp.ClientSessionOptions{ProtocolVersion: protocolVersion}
	session, err := client.Connect(context.Background(), clientTransport, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, server
}

func TestTrustedConfigFailsClosed(t *testing.T) {
	if _, err := New(Config{}, &fakeBackend{windowRef: "window-ref-1"}, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty config error = %v", err)
	}
	config := testConfig()
	config.FixtureNonce = "bad nonce"
	if err := ValidateConfig(config); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid nonce error = %v", err)
	}
	config = testConfig()
	config.NativeLibraryPath = "relative/path.dylib"
	if err := ValidateConfig(config); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("relative library path error = %v", err)
	}
}

func TestProtocolToolsAndReadOnlySessionScopedWindowReference(t *testing.T) {
	backend := &fakeBackend{windowRef: "window-ref-1"}
	session, _ := startPair(t, backend, "2025-03-26")
	if version := session.InitializeResult().ProtocolVersion; version != ProtocolVersion {
		t.Fatalf("negotiated protocol = %q, want exactly %q", version, ProtocolVersion)
	}
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 4 {
		t.Fatalf("listed %d tools, want exactly four", len(listed.Tools))
	}
	wantTools := map[string]bool{"hello": true, "doctor": true, "windows": true, "a11y": true}
	for _, tool := range listed.Tools {
		if !wantTools[tool.Name] {
			t.Fatalf("unexpected tool advertised: %q", tool.Name)
		}
		delete(wantTools, tool.Name)
	}
	if len(wantTools) != 0 {
		t.Fatalf("missing tools: %v", wantTools)
	}
	for _, name := range []string{"hello", "doctor"} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name})
		if err != nil || result.IsError {
			t.Fatalf("%s call: result=%v err=%v", name, result, err)
		}
	}
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "hello", Arguments: map[string]any{"pid": 99}}); err != nil {
		t.Fatal(err)
	}
	if backend.a11yCalls.Load() != 0 {
		t.Fatal("invalid host-scope argument triggered an a11y call")
	}
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "windows"}); err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "a11y", Arguments: map[string]any{"window_ref": "wrong-ref"}})
	if err != nil || !result.IsError || backend.a11yCalls.Load() != 0 {
		t.Fatalf("wrong window ref admitted: result=%v err=%v calls=%d", result, err, backend.a11yCalls.Load())
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "a11y", Arguments: map[string]any{"window_ref": "window-ref-1", "pid": 99}})
	if err != nil || !result.IsError || backend.a11yCalls.Load() != 0 {
		t.Fatalf("scope widening admitted: result=%v err=%v calls=%d", result, err, backend.a11yCalls.Load())
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "a11y", Arguments: map[string]any{"window_ref": "window-ref-1", "include_values": true}})
	if err != nil || result.IsError {
		t.Fatalf("valid a11y call: result=%v err=%v", result, err)
	}
	if resultText(result) == "" || !strings.Contains(resultText(result), `"canonical_state_id":"canonical-1"`) {
		t.Fatalf("a11y result lacks canonical semantic output: %s", resultText(result))
	}
}

func TestA11yCancellationReachesBackend(t *testing.T) {
	backend := &fakeBackend{windowRef: "window-ref-1", started: make(chan struct{}, 1), cancelled: make(chan struct{})}
	session, _ := startPair(t, backend, ProtocolVersion)
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "windows"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "a11y", Arguments: map[string]any{"window_ref": "window-ref-1"}})
	}()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("a11y backend did not start")
	}
	cancel()
	select {
	case <-backend.cancelled:
	case <-time.After(time.Second):
		t.Fatal("MCP cancellation did not reach backend")
	}
	select {
	case <-callDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled MCP call did not finish")
	}
}

func TestResultLimitAndIOFrameLimitAreExplicit(t *testing.T) {
	result, err := (&Server{}).reply("complete", strings.Repeat("x", MaxFrameBytes), nil)
	if err != nil || !result.IsError || resultText(result) != `{"status":"error","code":"result_limit"}` {
		t.Fatalf("oversized tool output was not replaced with result_limit: result=%v err=%v", result, err)
	}
	server, err := New(testConfig(), &fakeBackend{windowRef: "window-ref-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := server.stdioTransport()
	if transport.MaxLineLength != MaxFrameBytes {
		t.Fatalf("stdio max line length = %d, want %d", transport.MaxLineLength, MaxFrameBytes)
	}
	ioTransport := &mcp.IOTransport{MaxLineLength: MaxFrameBytes}
	if ioTransport.MaxLineLength != MaxFrameBytes {
		t.Fatalf("test IO transport cap = %d, want %d", ioTransport.MaxLineLength, MaxFrameBytes)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(resultText(result)), &decoded); err != nil || decoded["code"] != "result_limit" {
		t.Fatalf("result_limit is not valid structured JSON text: value=%v err=%v", decoded, err)
	}
	connection, err := (&mcp.IOTransport{
		Reader: io.NopCloser(strings.NewReader(strings.Repeat(" ", MaxFrameBytes+1) + "\n")),
		Writer: nopWriteCloser{Writer: io.Discard}, MaxLineLength: MaxFrameBytes,
	}).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Read(context.Background()); err == nil {
		t.Fatal("IOTransport accepted a JSON-RPC line above its configured limit")
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestNativeBackendUsesOnlyFixedFixtureOperations(t *testing.T) {
	client := &wireNativeClient{}
	backend, err := NewNativeBackend(client)
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{NativeLibraryPath: "/tmp/libBridgeProbe.dylib", FixturePID: 42, FixtureBundleID: FixtureBundleID, FixtureNonce: "fixture-1", ProcessLaunchGeneration: "launch-1"}
	if hello, err := backend.Hello(context.Background(), scope); err != nil || hello.Message != "hello" {
		t.Fatalf("native hello = %+v, %v", hello, err)
	}
	if doctor, err := backend.Doctor(context.Background(), scope); err != nil || len(doctor.Checks) != 2 || doctor.Checks[0].Prompted || doctor.Checks[1].Prompted {
		t.Fatalf("native doctor = %+v, %v", doctor, err)
	}
	windows, err := backend.Windows(context.Background(), scope)
	if err != nil || windows.Status != "complete" || windows.ProcessStartRef != "process-ref-1" || windows.Windows[0].WindowRef != "window-ref-1" {
		t.Fatalf("native windows = %+v, %v", windows, err)
	}
	snapshot, err := backend.Accessibility(context.Background(), scope, windows.ProcessStartRef, windows.Windows[0].WindowRef, false)
	if err != nil || snapshot.Status != semanticprobe.SnapshotComplete || snapshot.NativeStateID == "" || snapshot.CanonicalStateID == "" {
		t.Fatalf("native a11y = %+v, %v", snapshot, err)
	}
	if strings.Contains(client.lastRequest, "approval") || strings.Contains(client.lastRequest, "input") || strings.Contains(client.lastRequest, "open") {
		t.Fatalf("backend widened beyond read-only native operations: %s", client.lastRequest)
	}
}

type wireNativeClient struct{ lastRequest string }

func (client *wireNativeClient) Call(_ context.Context, raw []byte) ([]byte, error) {
	client.lastRequest = string(raw)
	var request nativeRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	var result any
	switch request.Operation {
	case "hello":
		if request.Scope != nil {
			return nil, errors.New("hello unexpectedly carried scope")
		}
		result = "hello"
	case "doctor":
		if request.Scope != nil {
			return nil, errors.New("doctor unexpectedly carried fixture scope")
		}
		result = map[string]any{
			"accessibility": map[string]any{"available": false, "prompted": false},
			"event_posting": map[string]any{"available": false, "prompted": false},
		}
	case "windows":
		if request.Scope == nil || request.Scope.PID != 42 || request.Scope.BundleID != FixtureBundleID || request.Scope.FixtureNonce != "fixture-1" {
			return nil, errors.New("windows scope widened or changed")
		}
		result = map[string]any{
			"process_start_ref": "process-ref-1", "windows": []any{map[string]any{"ref": "window-ref-1", "role": "window"}},
			"coverage": map[string]any{"status": "complete", "window_count": 1, "truncated": false, "timed_out": false},
		}
	case "a11y":
		if request.Scope == nil || request.Scope.PID != 42 || request.Scope.FixtureNonce != "fixture-1" || request.WindowRef != "window-ref-1" {
			return nil, errors.New("a11y scope widened or changed")
		}
		result = map[string]any{
			"observation_id": "observation-1", "state_id": strings.Repeat("a", 64),
			"process_start_ref": "process-ref-1", "window_ref": "window-ref-1", "root_refs": []string{"root-ref"},
			"elements": []any{map[string]any{"ref": "root-ref", "role": "AXWindow", "value_status": "omitted", "parent_ref": nil, "child_refs": []string{}}},
			"coverage": map[string]any{"status": "complete", "reason": nil, "depth_limit": semanticprobe.MaxDepth, "node_limit": semanticprobe.MaxElements, "text_byte_limit": semanticprobe.MaxTextBytes, "deadline_ms": semanticprobe.MaxDeadlineMS, "visited": 1, "text_bytes": 0, "truncated": false},
		}
	default:
		return nil, errors.New("unexpected native operation")
	}
	response, err := json.Marshal(map[string]any{"schema_version": 1, "request_id": request.RequestID, "status": "completed", "result": result, "error": nil})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func resultText(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) != 1 {
		return ""
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return text.Text
}

func TestDoctorRequiresExactUnpromptedCheckPair(t *testing.T) {
	valid := DoctorResult{Status: "complete", Checks: []Check{
		{Name: "accessibility", Status: "available"},
		{Name: "event_posting", Status: "unavailable"},
	}}
	if err := validateDoctorResult(valid); err != nil {
		t.Fatalf("valid doctor result rejected: %v", err)
	}
	invalid := []DoctorResult{
		{Status: "complete", Checks: []Check{{Name: "accessibility", Status: "available"}}},
		{Status: "complete", Checks: []Check{{Name: "accessibility", Status: "available"}, {Name: "accessibility", Status: "unavailable"}}},
		{Status: "complete", Checks: []Check{{Name: "accessibility", Status: "available"}, {Name: "event_posting", Status: "available", Prompted: true}}},
	}
	for index, result := range invalid {
		if err := validateDoctorResult(result); !errors.Is(err, ErrNativeContract) {
			t.Errorf("invalid doctor result %d = %v, want ErrNativeContract", index, err)
		}
	}
}

func TestBoundedFrameWriterCapsCompleteResponseWithNearLimitID(t *testing.T) {
	writerBuffer := &bytes.Buffer{}
	writer := &boundedFrameWriter{writer: writerBuffer, maxFrameBytes: MaxFrameBytes}
	response := &jsonrpc.Response{
		ID:     jsonrpc.StringID(strings.Repeat("r", MaxFrameBytes-200)),
		Result: map[string]any{"payload": strings.Repeat("private-response-canary", 32)},
	}
	frame, err := jsonrpc.EncodeMessage(response)
	if err != nil {
		t.Fatal(err)
	}
	frame = append(frame, '\n')
	if len(frame) <= MaxFrameBytes {
		t.Fatalf("fixture response frame length = %d, want over %d", len(frame), MaxFrameBytes)
	}
	if _, err := writer.Write(frame); err != nil {
		t.Fatalf("bounded frame write: %v", err)
	}
	if writerBuffer.Len() > MaxFrameBytes {
		t.Fatalf("outbound frame length = %d, over %d", writerBuffer.Len(), MaxFrameBytes)
	}
	message, err := jsonrpc.DecodeMessage(bytes.TrimSpace(writerBuffer.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	fallback, ok := message.(*jsonrpc.Response)
	if !ok || fallback.Error == nil {
		t.Fatalf("oversized response was not replaced by a correlated error: %#v", message)
	}
	requestID, ok := fallback.ID.Raw().(string)
	if !ok || len(requestID) != MaxFrameBytes-200 {
		t.Fatalf("fallback response ID did not preserve request correlation")
	}
	if strings.Contains(writerBuffer.String(), "private-response-canary") || fallback.Error.Error() == "" {
		t.Fatal("oversized response content leaked or explicit error missing")
	}
}

func TestBoundedFrameWriterFailsClosedWhenErrorCannotEchoID(t *testing.T) {
	writerBuffer := &bytes.Buffer{}
	writer := &boundedFrameWriter{writer: writerBuffer, maxFrameBytes: MaxFrameBytes}
	response := &jsonrpc.Response{
		ID:     jsonrpc.StringID(strings.Repeat("i", MaxFrameBytes-32)),
		Result: "nonempty-result",
	}
	frame, err := jsonrpc.EncodeMessage(response)
	if err != nil {
		t.Fatal(err)
	}
	frame = append(frame, '\n')
	if len(frame) <= MaxFrameBytes {
		t.Fatalf("fixture response frame length = %d, want over %d", len(frame), MaxFrameBytes)
	}
	if _, err := writer.Write(frame); !errors.Is(err, ErrOutboundFrame) {
		t.Fatalf("unfittable response error = %v, want ErrOutboundFrame", err)
	}
	if writerBuffer.Len() != 0 {
		t.Fatal("oversized or uncorrelated response bytes were emitted")
	}
}
