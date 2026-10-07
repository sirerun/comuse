package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirerun/comuse"
	"github.com/sirerun/comuse/internal/backend"
)

func TestSDKListsOnlyReadOnlyToolsAndReturnsCanonicalEnvelope(t *testing.T) {
	session := testMCPReadSession(t)
	client := connectMCPClient(t, NewServer(session))
	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"computer_state", "computer_windows", "computer_a11y", "computer_read_element", "computer_wait"}
	if len(listed.Tools) != len(want) {
		t.Fatalf("tools=%d want %d", len(listed.Tools), len(want))
	}
	for i, tool := range listed.Tools {
		if tool.Name != want[i] {
			t.Fatalf("tool[%d]=%q want %q", i, tool.Name, want[i])
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s has no shared output schema", tool.Name)
		}
	}
	result, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "computer_state"})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("state result marked error: %#v", result)
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content type %T", result.Content[0])
	}
	var fromText map[string]any
	if err := json.Unmarshal([]byte(text.Text), &fromText); err != nil {
		t.Fatal(err)
	}
	fromStructured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content type %T", result.StructuredContent)
	}
	if !jsonEqual(t, fromText, fromStructured) {
		t.Fatalf("text and structured envelopes differ: %#v / %#v", fromText, fromStructured)
	}
	if fromText["action"] != "state" || fromText["ok"] != true {
		t.Fatalf("canonical envelope=%s", text.Text)
	}
}

func TestKnownUnavailableSemanticRouteIsTypedUnsupportedAndUnknownStaysProtocolError(t *testing.T) {
	backend := &mcpFakeBackend{}
	session := testMCPReadSessionWithBackend(t, backend)
	client := connectMCPClient(t, NewServer(session))
	result, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "computer_click_element", Arguments: map[string]any{
		"action_id": "action-1", "window_ref": "window-1", "element_ref": "element-1", "state_id": strings.Repeat("a", 64),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("unsupported semantic result was not marked error")
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].(*sdk.TextContent).Text), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["action"] != "click_element" || envelope["ok"] != false || envelope["error"].(map[string]any)["code"] != "unsupported" {
		t.Fatalf("typed unsupported envelope=%#v", envelope)
	}
	if backend.executeCalls != 0 {
		t.Fatalf("unsupported route dispatched %d times", backend.executeCalls)
	}
	malformed, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "computer_a11y", Arguments: map[string]any{
		"window_ref": "window-1", "include_values": true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !malformed.IsError {
		t.Fatal("unknown argument was accepted")
	}
	var invalid map[string]any
	if err := json.Unmarshal([]byte(malformed.Content[0].(*sdk.TextContent).Text), &invalid); err != nil {
		t.Fatal(err)
	}
	if invalid["error"].(map[string]any)["code"] != "invalid_request" {
		t.Fatalf("strict argument rejection=%#v", invalid)
	}
	if _, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "computer_not_a_tool"}); err == nil {
		t.Fatal("unknown tool name was converted from an SDK protocol error")
	}
}

func TestServeClosesOwnedStreamOnCancellationAndBoundsFrames(t *testing.T) {
	stream := &mcpBlockingStream{closed: make(chan struct{}), started: make(chan struct{})}
	session := testMCPReadSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, session, stream) }()
	select {
	case <-stream.started:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not read")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop")
	}
	if stream.closeCount != 1 {
		t.Fatalf("stream closed %d times", stream.closeCount)
	}
}

func TestEnvelopeResultTextAndStructuredShareOneCanonicalEncoding(t *testing.T) {
	envelope, err := comuse.RejectionEnvelope("invalid_request")
	if err != nil {
		t.Fatal(err)
	}
	result, err := envelopeResult(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("rejection did not set IsError")
	}
	text := result.Content[0].(*sdk.TextContent).Text
	var fromText, fromStructured map[string]any
	if err := json.Unmarshal([]byte(text), &fromText); err != nil {
		t.Fatal(err)
	}
	fromStructured = result.StructuredContent.(map[string]any)
	if !jsonEqual(t, fromText, fromStructured) {
		t.Fatal("text/structured MCP forms diverged")
	}
}

func testMCPReadSession(t *testing.T) *comuse.Session {
	return testMCPReadSessionWithBackend(t, &mcpFakeBackend{})
}
func testMCPReadSessionWithBackend(t *testing.T, b *mcpFakeBackend) *comuse.Session {
	t.Helper()
	session, err := comuse.NewSession(comuse.Config{
		Backend: b,
		Scope:   comuse.Scope{Processes: []comuse.ProcessIdentity{{PID: 1, BundleID: "com.example.fixture", LaunchID: "launch-1"}}, ExpiresAt: time.Now().Add(time.Hour)},
		Budget:  comuse.Budget{MaxDepth: 8, MaxNodes: 64, MaxBytes: 16384, Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(context.Background()); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	return session
}

func connectMCPClient(t *testing.T, server *sdk.Server) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "comuse-mcp-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, &sdk.ClientSessionOptions{ProtocolVersion: "2025-06-18"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	left, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	right, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(left, right)
}

type mcpFakeBackend struct {
	mu           sync.Mutex
	executeCalls int
}

func (b *mcpFakeBackend) Doctor(context.Context) (backend.Doctor, error) {
	return backend.Doctor{Capabilities: backend.Capabilities{Accessibility: true}, Permissions: map[string]string{"accessibility": "granted"}}, nil
}
func (b *mcpFakeBackend) Windows(context.Context, backend.Budget) ([]backend.Window, error) {
	return []backend.Window{}, nil
}
func (b *mcpFakeBackend) Observe(context.Context, string, backend.Budget) (backend.Snapshot, error) {
	return backend.Snapshot{}, &backend.Error{Code: "unsupported", Message: "unsupported"}
}
func (b *mcpFakeBackend) ReadElement(context.Context, string, string, string, backend.Budget) (backend.ElementContent, error) {
	return backend.ElementContent{}, &backend.Error{Code: "unsupported", Message: "unsupported"}
}
func (b *mcpFakeBackend) Execute(context.Context, backend.Action) (backend.ActionResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.executeCalls++
	return backend.ActionResult{}, &backend.Error{Code: "unsupported", Message: "unsupported"}
}
func (b *mcpFakeBackend) Close(context.Context) error { return nil }

type mcpBlockingStream struct {
	closed     chan struct{}
	started    chan struct{}
	once       sync.Once
	closeCount int
}

func (s *mcpBlockingStream) Read([]byte) (int, error) {
	s.once.Do(func() { close(s.started) })
	<-s.closed
	return 0, io.EOF
}
func (*mcpBlockingStream) Write(p []byte) (int, error) { return len(p), nil }
func (s *mcpBlockingStream) Close() error {
	s.closeCount++
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

var _ io.ReadWriteCloser = (*mcpBlockingStream)(nil)
