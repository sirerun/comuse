package comuse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirerun/comuse"
	"github.com/sirerun/comuse/internal/backend"
	comusemcp "github.com/sirerun/comuse/mcp"
	"path/filepath"
)

func TestMCPParityDefaultRoutesShareCoreEnvelopeAndUnsupportedActions(t *testing.T) {
	backend := &parityMCPBackend{}
	session, err := comuse.NewSession(comuse.Config{
		Backend: backend,
		Scope:   comuse.Scope{Processes: []comuse.ProcessIdentity{{PID: 88, BundleID: "com.example.fixture", LaunchID: "fixture-88"}}, ExpiresAt: time.Now().Add(time.Hour)},
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
	server := comusemcp.NewServer(session)
	client := parityMCPClient(t, server)
	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"computer_a11y", "computer_read_element", "computer_state", "computer_wait", "computer_windows"}
	if len(listed.Tools) != len(want) {
		t.Fatalf("default tool count=%d want=%d", len(listed.Tools), len(want))
	}
	for index, tool := range listed.Tools {
		if tool.Name != want[index] {
			t.Fatalf("tool[%d]=%q want=%q", index, tool.Name, want[index])
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s has no shared response schema", tool.Name)
		}
		var expected any
		if err := json.Unmarshal(comuse.ResponseSchema(), &expected); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		wantSchema, err := json.Marshal(expected)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(wantSchema) {
			t.Errorf("%s output schema is not ResponseSchema", tool.Name)
		}
	}
	state, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "computer_state"})
	if err != nil {
		t.Fatal(err)
	}
	if state.IsError {
		t.Fatal("doctor call marked error")
	}
	assertSameMCPEnvelope(t, state)
	text := state.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, `"action":"state"`) || !strings.Contains(text, `"usage"`) {
		t.Fatalf("not the canonical Session.Call envelope: %s", text)
	}
	var stateEnvelope map[string]any
	if err := json.Unmarshal([]byte(text), &stateEnvelope); err != nil {
		t.Fatal(err)
	}
	usage := stateEnvelope["usage"].(map[string]any)
	if usage["serialized_text_bytes"].(float64) <= 0 {
		t.Fatalf("response accounting missing: %#v", usage)
	}

	unsupported, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "computer_click_element", Arguments: map[string]any{
		"action_id": "unsupported-action", "window_ref": "window-1", "element_ref": "element-1", "state_id": strings.Repeat("a", 64),
	}})
	if err != nil {
		t.Fatalf("known semantic tool returned protocol error: %v", err)
	}
	if !unsupported.IsError {
		t.Fatal("unqualified action did not return IsError")
	}
	assertSameMCPEnvelope(t, unsupported)
	var response map[string]any
	if err := json.Unmarshal([]byte(unsupported.Content[0].(*sdk.TextContent).Text), &response); err != nil {
		t.Fatal(err)
	}
	if response["action"] != "click_element" || response["error"].(map[string]any)["code"] != "unsupported" {
		t.Fatalf("typed unsupported response=%#v", response)
	}
	if backend.executions != 0 {
		t.Fatalf("unqualified call dispatched %d times", backend.executions)
	}
	if _, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "computer_unknown"}); err == nil {
		t.Fatal("unknown MCP tool did not remain an SDK protocol error")
	}
}

func TestMCPHostCompositionDoesNotAdvertiseDeveloperRoutes(t *testing.T) {
	session, err := comuse.NewSession(comuse.Config{
		Backend: &parityMCPBackend{},
		Scope:   comuse.Scope{Processes: []comuse.ProcessIdentity{{PID: 88, BundleID: "com.example.fixture", LaunchID: "fixture-88"}}, ExpiresAt: time.Now().Add(time.Hour)},
		Budget:  comuse.Budget{MaxDepth: 8, MaxNodes: 64, MaxBytes: 16384, Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(context.Background()); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()
	server, err := comusemcp.NewServerForHost(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	client := parityMCPClient(t, server)
	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 5 {
		t.Fatalf("readonly host advertised %d tools, want 5", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if tool.Name == "computer_click" || tool.Name == "computer_type_text" || tool.Name == "computer_press_key" || tool.Name == "computer_scroll" || tool.Name == "computer_drag" || tool.Name == "computer_focus_window" {
			t.Errorf("developer route advertised: %s", tool.Name)
		}
	}
}

func TestMCPHostAdvertisesOnlyIndividuallyQualifiedSemanticOperations(t *testing.T) {
	backend := &parityMCPBackend{qualified: true}
	session, err := comuse.NewSyntheticSessionForTest(comuse.Config{
		Backend:          backend,
		Scope:            comuse.Scope{Processes: []comuse.ProcessIdentity{{PID: 88, BundleID: "com.example.fixture", LaunchID: "fixture-88"}}, ExpiresAt: time.Now().Add(time.Hour)},
		Budget:           comuse.Budget{MaxDepth: 8, MaxNodes: 64, MaxBytes: 16384, Timeout: time.Second},
		ApprovalProvider: parityMCPApproval{},
		WriterDirectory:  t.TempDir(),
		WriterKey:        bytes.Repeat([]byte{0x4a}, 32),
		MaxActions:       4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(context.Background()); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	server, err := comusemcp.NewServerForHost(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	client := parityMCPClient(t, server)
	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"computer_state": true, "computer_windows": true, "computer_a11y": true, "computer_read_element": true, "computer_wait": true,
		"computer_click_element": true, "computer_element_action": true, "computer_write_element": true, "computer_scroll_element": true,
	}
	if len(listed.Tools) != len(want) {
		t.Fatalf("qualified tool count=%d want=%d", len(listed.Tools), len(want))
	}
	for _, tool := range listed.Tools {
		if !want[tool.Name] {
			t.Errorf("unqualified tool advertised: %s", tool.Name)
		}
		delete(want, tool.Name)
	}
	for missing := range want {
		t.Errorf("missing qualified tool: %s", missing)
	}
}

func TestMCPQualifiedSemanticCallsUseExactArgumentsAndReplayCore(t *testing.T) {
	backend := newParityMCPActionBackend()
	root := filepath.Join(t.TempDir(), "journal")
	session, err := comuse.NewSyntheticSessionForTest(comuse.Config{
		Backend:          backend,
		Scope:            comuse.Scope{Processes: []comuse.ProcessIdentity{backend.process}, ExpiresAt: time.Now().Add(time.Hour)},
		Budget:           comuse.Budget{MaxDepth: 8, MaxNodes: 64, MaxBytes: 16384, Timeout: time.Second},
		ApprovalProvider: parityMCPApproval{},
		WriterDirectory:  root,
		WriterKey:        bytes.Repeat([]byte{0x53}, 32),
		MaxActions:       8,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(context.Background()); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	server, err := comusemcp.NewServerForHost(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	client := parityMCPClient(t, server)
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}

	callAction := func(tool, id string, extra map[string]any) *sdk.CallToolResult {
		t.Helper()
		observation, err := session.Observe(context.Background(), "window-1")
		if err != nil {
			t.Fatalf("fixture observe: %v", err)
		}
		args := map[string]any{"action_id": id, "window_ref": "window-1", "element_ref": observation.Elements[0].Ref, "state_id": observation.StateID}
		for key, value := range extra {
			args[key] = value
		}
		result, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("CallTool(%s): %v", tool, err)
		}
		return result
	}
	click := callAction("computer_click_element", "mcp-click-1", nil)
	if click.IsError {
		t.Fatalf("click result: %s", click.Content[0].(*sdk.TextContent).Text)
	}
	assertSameMCPEnvelope(t, click)
	clickText := click.Content[0].(*sdk.TextContent).Text
	var first map[string]any
	if err := json.Unmarshal([]byte(clickText), &first); err != nil {
		t.Fatal(err)
	}
	if first["action_id"] != "mcp-click-1" || first["action"] != "click_element" {
		t.Fatalf("click envelope=%#v", first)
	}
	firstUsage := first["usage"].(map[string]any)
	if firstUsage["actions"].(float64) != 1 || firstUsage["serialized_text_bytes"].(float64) <= 0 {
		t.Fatalf("click accounting=%#v", firstUsage)
	}
	// Repeating the same route and exact arguments is resolved by the canonical
	// durable replay path. The adapter does not dispatch a second native action.
	observation, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "computer_click_element", Arguments: map[string]any{
		"action_id": "mcp-click-1", "window_ref": "window-1", "element_ref": observation.Elements[0].Ref, "state_id": observation.StateID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if replay.IsError {
		t.Fatalf("replay result: %#v", replay.Content)
	}
	var replayEnvelope map[string]any
	if err := json.Unmarshal([]byte(replay.Content[0].(*sdk.TextContent).Text), &replayEnvelope); err != nil {
		t.Fatal(err)
	}
	if replayEnvelope["action_id"] != "mcp-click-1" || replayEnvelope["execution"] != "applied" {
		t.Fatalf("replay envelope=%#v", replayEnvelope)
	}
	if replayEnvelope["usage"].(map[string]any)["actions"].(float64) != 0 {
		t.Fatalf("replay repeated action accounting: %#v", replayEnvelope["usage"])
	}

	for _, item := range []struct {
		tool, id string
		extra    map[string]any
	}{
		{"computer_element_action", "mcp-element-2", map[string]any{"kind": "press"}},
		{"computer_write_element", "mcp-write-3", map[string]any{"mode": "replace", "text": "fixture"}},
		{"computer_scroll_element", "mcp-scroll-4", map[string]any{"direction": "up", "amount": "line"}},
	} {
		result := callAction(item.tool, item.id, item.extra)
		if result.IsError {
			t.Errorf("%s returned error: %#v", item.tool, result.Content)
			continue
		}
		assertSameMCPEnvelope(t, result)
	}
	if got := len(backend.executionsCopy()); got != 4 {
		t.Fatalf("native semantic dispatch count=%d want 4 (one replay)", got)
	}
	for _, action := range backend.executionsCopy() {
		if action.StateID != "private-native-state" {
			t.Errorf("core did not use private native binding: %#v", action)
		}
	}
}

func parityMCPClient(t *testing.T, server *sdk.Server) *sdk.ClientSession {
	t.Helper()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "comuse-parity-test", Version: "1"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, &sdk.ClientSessionOptions{ProtocolVersion: "2025-06-18"})
	if err != nil {
		t.Fatal(err)
	}
	if clientSession.InitializeResult().ProtocolVersion != "2025-06-18" {
		t.Fatalf("protocol=%s", clientSession.InitializeResult().ProtocolVersion)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func assertSameMCPEnvelope(t *testing.T, result *sdk.CallToolResult) {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("content length=%d", len(result.Content))
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("text content type %T", result.Content[0])
	}
	var fromText, fromStructured any
	if err := json.Unmarshal([]byte(text.Text), &fromText); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &fromStructured); err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(fromText)
	right, _ := json.Marshal(fromStructured)
	if string(left) != string(right) {
		t.Fatalf("content projections differ: %s != %s", left, right)
	}
}

type parityMCPBackend struct {
	executions int
	qualified  bool
}

func (b *parityMCPBackend) Doctor(context.Context) (backend.Doctor, error) {
	capabilities := backend.Capabilities{Accessibility: true}
	if b.qualified {
		capabilities.Input = true
		capabilities.QualifiedInput = true
		capabilities.ActionKinds = []string{backend.ActionPress, backend.ActionPick, backend.ActionFocus, backend.ActionReplace, backend.ActionInsert, backend.ActionScroll}
	}
	return backend.Doctor{Capabilities: capabilities, Permissions: map[string]string{"accessibility": "granted", "input": "granted"}}, nil
}
func (*parityMCPBackend) Windows(context.Context, backend.Budget) ([]backend.Window, error) {
	return []backend.Window{}, nil
}
func (*parityMCPBackend) Observe(context.Context, string, backend.Budget) (backend.Snapshot, error) {
	return backend.Snapshot{}, &backend.Error{Code: "unsupported", Message: "unsupported"}
}
func (*parityMCPBackend) ReadElement(context.Context, string, string, string, backend.Budget) (backend.ElementContent, error) {
	return backend.ElementContent{}, &backend.Error{Code: "unsupported", Message: "unsupported"}
}
func (b *parityMCPBackend) Execute(context.Context, backend.Action) (backend.ActionResult, error) {
	b.executions++
	return backend.ActionResult{}, errors.New("unexpected input dispatch")
}
func (*parityMCPBackend) Close(context.Context) error { return nil }

var _ comuse.Backend = (*parityMCPBackend)(nil)

type parityMCPApproval struct{}

func (parityMCPApproval) Approve(_ context.Context, request comuse.ApprovalRequest) (comuse.Approval, error) {
	return comuse.Approval{SessionID: request.SessionID, Action: request.Action, Process: request.Process, ObservedAt: request.ObservedAt, PolicyVersion: request.PolicyVersion, ExpiresAt: request.ExpiresAt}, nil
}

type parityMCPActionBackend struct {
	mu         sync.Mutex
	process    comuse.ProcessIdentity
	executions []backend.Action
}

func newParityMCPActionBackend() *parityMCPActionBackend {
	return &parityMCPActionBackend{process: comuse.ProcessIdentity{PID: 89, BundleID: "com.example.fixture", LaunchID: "fixture-89"}}
}
func (b *parityMCPActionBackend) Doctor(context.Context) (backend.Doctor, error) {
	return backend.Doctor{Capabilities: backend.Capabilities{
		Accessibility: true, Input: true, QualifiedInput: true,
		ActionKinds: []string{backend.ActionPress, backend.ActionPick, backend.ActionFocus, backend.ActionReplace, backend.ActionInsert, backend.ActionScroll},
	}, Permissions: map[string]string{"accessibility": "granted", "input": "granted"}}, nil
}
func (b *parityMCPActionBackend) Windows(context.Context, backend.Budget) ([]backend.Window, error) {
	return []backend.Window{{Ref: "window-1", Process: b.process, Title: "Fixture"}}, nil
}
func (*parityMCPActionBackend) Observe(_ context.Context, window string, _ backend.Budget) (backend.Snapshot, error) {
	enabled := true
	return backend.Snapshot{WindowRef: window, StateID: "private-native-state", ObservedAt: time.Now(), Coverage: backend.Coverage{Complete: true}, Elements: []backend.Element{{
		Ref: "normal-1", Role: "AXTextField", Label: "Name", Enabled: &enabled, Actions: []string{backend.ActionPress, backend.ActionReplace, backend.ActionInsert, backend.ActionScroll}, Classification: "normal",
	}}}, nil
}
func (*parityMCPActionBackend) ReadElement(context.Context, string, string, string, backend.Budget) (backend.ElementContent, error) {
	return backend.ElementContent{}, &backend.Error{Code: "unsupported", Message: "unsupported"}
}
func (b *parityMCPActionBackend) Execute(_ context.Context, action backend.Action) (backend.ActionResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.executions = append(b.executions, action)
	return backend.ActionResult{ActionID: action.ID, Execution: backend.ExecutionApplied, Verification: backend.Verification{Status: backend.VerificationUnavailable, Reason: "verification_unavailable"}, StateStatus: backend.StateUnavailable, Cleanup: backend.CleanupComplete}, nil
}
func (*parityMCPActionBackend) Close(context.Context) error { return nil }
func (b *parityMCPActionBackend) executionsCopy() []backend.Action {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]backend.Action(nil), b.executions...)
}
