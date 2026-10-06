package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirerun/comuse"
	"github.com/sirerun/comuse/internal/backend"
)

func TestDecodeArgsStrictObject(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want bool
	}{
		{name: "omitted empty arguments", want: true},
		{name: "empty object", raw: json.RawMessage(`{}`), want: true},
		{name: "valid window", raw: json.RawMessage(`{"window_ref":"w1"}`), want: true},
		{name: "unknown field", raw: json.RawMessage(`{"window_ref":"w1","pid":10}`)},
		{name: "duplicate field", raw: json.RawMessage(`{"window_ref":"w1","window_ref":"w2"}`)},
		{name: "trailing object", raw: json.RawMessage(`{} {}`)},
		{name: "wrong root", raw: json.RawMessage(`[]`)},
		{name: "wrong field type", raw: json.RawMessage(`{"window_ref":3}`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var args windowArgs
			err := decodeArgs(tc.raw, &args)
			if got := err == nil; got != tc.want {
				t.Fatalf("decodeArgs() success = %v, want %v (err %v)", got, tc.want, err)
			}
		})
	}
}

func TestSDKListsOnlyReadOnlySemanticTools(t *testing.T) {
	ctx := context.Background()
	session := newTestSession(t, &fakeBackend{})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := NewServer(session).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "comuse-test", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, &sdk.ClientSessionOptions{ProtocolVersion: "2025-06-18"})
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	if got := clientSession.InitializeResult().ProtocolVersion; got != "2025-06-18" {
		t.Fatalf("negotiated protocol = %q, want 2025-06-18", got)
	}

	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"computer_state": true, "computer_windows": true, "computer_a11y": true,
		"computer_read_element": true, "computer_wait": true,
	}
	if len(listed.Tools) != len(want) {
		t.Fatalf("ListTools returned %d tools, want %d", len(listed.Tools), len(want))
	}
	for _, item := range listed.Tools {
		if !want[item.Name] {
			t.Errorf("unexpected tool advertised: %q", item.Name)
		}
		delete(want, item.Name)
		schema, ok := item.InputSchema.(map[string]any)
		if !ok || schema["additionalProperties"] != false {
			t.Errorf("tool %s schema must reject additional properties: %#v", item.Name, item.InputSchema)
		}
	}
	for name := range want {
		t.Errorf("missing tool %q", name)
	}
	invalid, err := clientSession.CallTool(ctx, &sdk.CallToolParams{
		Name: "computer_a11y", Arguments: map[string]any{"window_ref": "window-1", "include_values": true},
	})
	if err == nil && (invalid == nil || !invalid.IsError) {
		t.Fatal("unknown field was accepted by MCP tool")
	}
}

func TestSDKToolsDelegateToSharedSession(t *testing.T) {
	backend := &fakeBackend{}
	session := newTestSession(t, backend)
	clientSession := connectClient(t, NewServer(session))
	defer clientSession.Close()

	callTool(t, clientSession, "computer_state", nil)
	callTool(t, clientSession, "computer_windows", nil)
	a11y := callTool(t, clientSession, "computer_a11y", map[string]any{"window_ref": "window-1"})
	result, ok := a11y.Result.(map[string]any)
	if !ok {
		t.Fatalf("a11y result = %T, want object", a11y.Result)
	}
	stateID, ok := result["state_id"].(string)
	if !ok || stateID == "" {
		t.Fatalf("a11y state_id = %#v", result["state_id"])
	}
	callTool(t, clientSession, "computer_read_element", map[string]any{
		"window_ref": "window-1", "element_ref": "element-1", "state_id": stateID,
	})
	callTool(t, clientSession, "computer_wait", map[string]any{"window_ref": "window-1", "timeout_ms": 10})

	wantCalls := []string{"doctor", "windows", "observe", "read_element", "observe"}
	if got := backend.callSnapshot(); !equalStrings(got, wantCalls) {
		t.Fatalf("backend calls = %v, want %v", got, wantCalls)
	}
}

func TestSDKCancellationReachesSharedSession(t *testing.T) {
	ctx := context.Background()
	backend := &fakeBackend{blockObserve: true, observeStarted: make(chan struct{})}
	session := newTestSession(t, backend)
	clientSession := connectClient(t, NewServer(session))
	defer clientSession.Close()

	callTool(t, clientSession, "computer_windows", nil)
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	callErr := make(chan error, 1)
	go func() {
		_, err := clientSession.CallTool(callCtx, &sdk.CallToolParams{
			Name: "computer_a11y", Arguments: map[string]any{"window_ref": "window-1"},
		})
		callErr <- err
	}()
	select {
	case <-backend.observeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("semantic backend did not begin observation")
	}
	cancel()
	select {
	case err := <-callErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled tool call error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled tool call did not return")
	}
	if !backend.cancelSeen() {
		t.Fatal("session backend did not observe cancellation")
	}
}

func TestServeUsesOfficialSDKStdioTransport(t *testing.T) {
	backend := &fakeBackend{}
	session := newTestSession(t, backend)
	serverConn, clientConn := net.Pipe()
	serveErr := make(chan error, 1)
	go func() { serveErr <- Serve(context.Background(), session, serverConn) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "comuse-stdio-test", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(context.Background(), &sdk.IOTransport{Reader: clientConn, Writer: clientConn}, &sdk.ClientSessionOptions{ProtocolVersion: "2025-06-18"})
	if err != nil {
		_ = clientConn.Close()
		t.Fatalf("stdio protocol initialize: %v", err)
	}
	if got := clientSession.InitializeResult().ProtocolVersion; got != "2025-06-18" {
		t.Fatalf("stdio negotiated protocol = %q, want 2025-06-18", got)
	}
	callTool(t, clientSession, "computer_state", nil)
	_ = clientSession.Close()
	_ = clientConn.Close()
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop after stdio close")
	}
}

func TestServeCancellationClosesOwnedStreamOnce(t *testing.T) {
	stream := &blockingStream{closed: make(chan struct{}), readStarted: make(chan struct{})}
	session := newTestSession(t, &fakeBackend{})
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- Serve(ctx, session, stream) }()
	select {
	case <-stream.readStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("MCP Serve did not begin reading the stream")
	}
	cancel()
	select {
	case <-serveErr:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling Serve did not unblock its read")
	}
	if got := stream.closeCount(); got != 1 {
		t.Fatalf("underlying stream closed %d times, want once", got)
	}
}

func TestTextResultCarriesSharedEnvelope(t *testing.T) {
	envelope := envelopeError("invalid_request")
	result := textResult(envelope)
	if !result.IsError {
		t.Fatal("error envelope did not set MCP isError")
	}
	if got, ok := result.StructuredContent.(comuse.Envelope); !ok || got.Status != "error" || got.Error.Code != "invalid_request" {
		t.Fatalf("StructuredContent = %#v, want shared envelope", result.StructuredContent)
	}
	if len(result.Content) != 1 {
		t.Fatalf("Content length = %d, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("Content[0] type = %T, want text", result.Content[0])
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
		t.Fatalf("decode text envelope: %v", err)
	}
	if decoded["status"] != "error" || decoded["schema_version"] != float64(1) {
		t.Fatalf("text envelope = %#v", decoded)
	}
}

func TestErrorsAreSanitizedAtMCPBoundary(t *testing.T) {
	backend := &fakeBackend{doctorErr: errors.New("private-native-payload-secret")}
	clientSession := connectClient(t, NewServer(newTestSession(t, backend)))
	defer clientSession.Close()
	result, err := clientSession.CallTool(context.Background(), &sdk.CallToolParams{Name: "computer_state"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("error tool result IsError = false: %#v", result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("error content length = %d", len(result.Content))
	}
	text := result.Content[0].(*sdk.TextContent).Text
	if bytes.Contains([]byte(text), []byte("private-native-payload-secret")) {
		t.Fatal("backend error payload leaked through MCP")
	}
	if !bytes.Contains([]byte(text), []byte(`"code":"internal_error"`)) {
		t.Fatalf("sanitized result lacks stable code: %s", text)
	}
}

func connectClient(t *testing.T, server *sdk.Server) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "comuse-test", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, &sdk.ClientSessionOptions{ProtocolVersion: "2025-06-18"})
	if err != nil {
		t.Fatal(err)
	}
	if got := clientSession.InitializeResult().ProtocolVersion; got != "2025-06-18" {
		t.Fatalf("negotiated protocol = %q, want 2025-06-18", got)
	}
	return clientSession
}

func callTool(t *testing.T, client *sdk.ClientSession, name string, args map[string]any) comuse.Envelope {
	t.Helper()
	result, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if result.IsError {
		t.Fatalf("CallTool(%s) returned error content: %#v", name, result.Content)
	}
	if len(result.Content) != 1 {
		t.Fatalf("CallTool(%s) content count = %d", name, len(result.Content))
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s) content type = %T", name, result.Content[0])
	}
	var envelope comuse.Envelope
	if err := json.Unmarshal([]byte(text.Text), &envelope); err != nil {
		t.Fatalf("CallTool(%s) envelope: %v", name, err)
	}
	if envelope.Status != "ok" || envelope.SchemaVersion != comuse.SchemaVersion {
		t.Fatalf("CallTool(%s) envelope = %#v", name, envelope)
	}
	return envelope
}

func newTestSession(t *testing.T, b *fakeBackend) *comuse.Session {
	t.Helper()
	process := comuse.ProcessIdentity{PID: 123, BundleID: "com.example.fixture", LaunchID: "launch-1"}
	session, err := comuse.NewSession(comuse.Config{
		Backend: b,
		Scope:   comuse.Scope{Processes: []comuse.ProcessIdentity{process}, ExpiresAt: time.Now().Add(time.Hour)},
		Budget:  comuse.Budget{MaxDepth: 16, MaxNodes: 256, MaxBytes: 1 << 20, Timeout: time.Second},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	return session
}

type fakeBackend struct {
	mu             sync.Mutex
	calls          []string
	blockObserve   bool
	observeStarted chan struct{}
	cancelled      bool
	doctorErr      error
}

type blockingStream struct {
	mu          sync.Mutex
	closed      chan struct{}
	readStarted chan struct{}
	started     bool
	closeN      int
	once        sync.Once
}

func (s *blockingStream) Read([]byte) (int, error) {
	s.mu.Lock()
	if !s.started {
		s.started = true
		close(s.readStarted)
	}
	s.mu.Unlock()
	<-s.closed
	return 0, io.EOF
}

func (s *blockingStream) Write(p []byte) (int, error) { return len(p), nil }

func (s *blockingStream) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closeN++
		s.mu.Unlock()
		close(s.closed)
	})
	return nil
}

func (s *blockingStream) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeN
}

func (b *fakeBackend) Doctor(context.Context) (backend.Doctor, error) {
	b.record("doctor")
	if b.doctorErr != nil {
		return backend.Doctor{}, b.doctorErr
	}
	return backend.Doctor{Capabilities: backend.Capabilities{Accessibility: true}}, nil
}

func (b *fakeBackend) Windows(context.Context, backend.Budget) ([]backend.Window, error) {
	b.record("windows")
	return []backend.Window{{Ref: "window-1", Process: testProcess(), Title: "Fixture"}}, nil
}

func (b *fakeBackend) Observe(ctx context.Context, windowRef string, _ backend.Budget) (backend.Snapshot, error) {
	b.record("observe")
	if b.blockObserve {
		select {
		case <-b.observeStarted:
		default:
			close(b.observeStarted)
		}
		<-ctx.Done()
		b.mu.Lock()
		b.cancelled = true
		b.mu.Unlock()
		return backend.Snapshot{}, ctx.Err()
	}
	return backend.Snapshot{
		WindowRef: windowRef,
		StateID:   "native-state-1",
		Elements: []backend.Element{{
			Ref: "element-1", Role: "textfield", Label: "Fixture field", Classification: "normal",
		}},
		Coverage: backend.Coverage{Complete: true},
	}, nil
}

func (b *fakeBackend) ReadElement(context.Context, string, string, string, backend.Budget) (backend.ElementContent, error) {
	b.record("read_element")
	return backend.ElementContent{WindowRef: "window-1", ElementRef: "element-1", StateID: "native-state-1", Text: "fixture"}, nil
}

func (b *fakeBackend) Execute(context.Context, backend.Action) (backend.ActionResult, error) {
	b.record("execute")
	return backend.ActionResult{}, errors.New("MCP adapter must not call Execute")
}

func (b *fakeBackend) Close(context.Context) error { return nil }

func (b *fakeBackend) record(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, name)
}

func (b *fakeBackend) callSnapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.calls...)
}

func (b *fakeBackend) cancelSeen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cancelled
}

func testProcess() backend.ProcessIdentity {
	return backend.ProcessIdentity{PID: 123, BundleID: "com.example.fixture", LaunchID: "launch-1"}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
