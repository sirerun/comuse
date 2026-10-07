package comuse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirerun/comuse"
	"github.com/sirerun/comuse/internal/backend"
	"github.com/sirerun/comuse/internal/cli"
)

type cliFixtureBackend struct {
	executions int
	closeErr   error
}

type cliWireEnvelope struct {
	Status    string  `json:"status"`
	OK        bool    `json:"ok"`
	ActionID  *string `json:"action_id"`
	Execution string  `json:"execution"`
	Error     *struct {
		Code string `json:"code"`
	} `json:"error"`
	Result json.RawMessage `json:"result"`
}

func (*cliFixtureBackend) Doctor(context.Context) (backend.Doctor, error) {
	return backend.Doctor{Capabilities: backend.Capabilities{Accessibility: true}, Permissions: map[string]string{"accessibility": "granted"}}, nil
}
func (*cliFixtureBackend) Windows(context.Context, backend.Budget) ([]backend.Window, error) {
	return []backend.Window{{Ref: "window-1", Process: backend.ProcessIdentity{PID: 7, BundleID: "comuse.fixture", LaunchID: "launch-1"}, Title: "Fixture"}}, nil
}
func (*cliFixtureBackend) Observe(_ context.Context, window string, _ backend.Budget) (backend.Snapshot, error) {
	return backend.Snapshot{WindowRef: window, StateID: strings.Repeat("a", 64), ObservedAt: time.Now(), Elements: []backend.Element{{Ref: "element-1", Role: "AXTextField", Classification: "normal", Order: 0}}, Coverage: backend.Coverage{Complete: true}}, nil
}
func (*cliFixtureBackend) ReadElement(_ context.Context, window, element, state string, _ backend.Budget) (backend.ElementContent, error) {
	return backend.ElementContent{WindowRef: window, ElementRef: element, StateID: state, Text: "fixture"}, nil
}
func (b *cliFixtureBackend) Execute(context.Context, backend.Action) (backend.ActionResult, error) {
	b.executions++
	return backend.ActionResult{}, &backend.Error{Code: "unsupported", Message: "unsupported"}
}
func (b *cliFixtureBackend) Close(context.Context) error { return b.closeErr }

func newCLIIntegrationSession(t *testing.T, b *cliFixtureBackend) *comuse.Session {
	t.Helper()
	session, err := comuse.NewSyntheticSessionForTest(comuse.Config{
		Backend: b,
		Scope:   comuse.Scope{Processes: []comuse.ProcessIdentity{{PID: 7, BundleID: "comuse.fixture", LaunchID: "launch-1"}}, ExpiresAt: time.Now().Add(time.Minute)},
		Budget:  comuse.Budget{MaxDepth: 8, MaxNodes: 32, MaxBytes: 32768, Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(context.Background()); err != nil {
			t.Errorf("close integration session: %s", comuse.ErrorCode(err))
		}
	})
	return session
}

func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv("COMUSE_CLI_TEST_HELPER") != "1" {
		return
	}
	backend := &cliFixtureBackend{}
	if os.Getenv("COMUSE_CLI_TEST_CLOSE_FAILURE") == "1" {
		backend.closeErr = errors.New("private close diagnostic")
	}
	session, err := comuse.NewSyntheticSessionForTest(comuse.Config{
		Backend: backend,
		Scope:   comuse.Scope{Processes: []comuse.ProcessIdentity{{PID: 7, BundleID: "comuse.fixture", LaunchID: "launch-1"}}, ExpiresAt: time.Now().Add(time.Minute)},
		Budget:  comuse.Budget{MaxDepth: 8, MaxNodes: 32, MaxBytes: 32768, Timeout: time.Second},
	})
	if err != nil {
		os.Exit(70)
	}
	command := os.Getenv("COMUSE_CLI_TEST_COMMAND")
	code := cli.Run(context.Background(), session, []string{command}, os.Stdin, os.Stdout, os.Stderr)
	if err := session.Close(context.Background()); err != nil {
		code = 71
	}
	os.Exit(code)
}

func TestServeSubprocessUsesSharedEnvelopeAndDecoder(t *testing.T) {
	lines := []string{
		`{"command":"doctor"}`,
		`{"command":"state"}`,
		`{"command":"windows"}`,
		`{"command":"a11y","window_ref":"window-1"}`,
		`{"command":"read-element","window_ref":"window-1","element_ref":"element-1","state_id":"` + strings.Repeat("a", 64) + `"}`,
		`{"command":"wait","window_ref":"window-1","timeout_ms":1}`,
		`{"command":"click-element","action_id":"cli-action-01","window_ref":"window-1","element_ref":"element-1","state_id":"` + strings.Repeat("a", 64) + `"}`,
		`{"command":"click-element","action_id":"cli-action-01","window_ref":"window-1","element_ref":"element-1","state_id":"` + strings.Repeat("a", 64) + `"}`,
		`{"command":"doctor","approval":true}`,
		`{"command":null}`,
		`{"command":"DOCTOR"}`,
		`{"command":"doctor","command":"windows"}`,
		`{"command":"ledger"}`,
	}
	command := exec.Command(os.Args[0], "-test.run=^TestCLIHelperProcess$")
	command.Env = append(os.Environ(), "COMUSE_CLI_TEST_HELPER=1", "COMUSE_CLI_TEST_COMMAND=serve")
	command.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("helper process: %v; stderr=%q", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("protocol polluted stderr: %q", stderr.String())
	}
	if got, want := bytes.Count(stdout.Bytes(), []byte{'\n'}), len(lines); got != want {
		t.Fatalf("newline-delimited responses=%d want=%d output=%q", got, want, stdout.String())
	}
	decoder := json.NewDecoder(&stdout)
	statuses := make([]string, 0, len(lines))
	actionIDs := make([]string, 0, len(lines))
	for range lines {
		var envelope cliWireEnvelope
		if err := decoder.Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, envelope.Status)
		if envelope.ActionID != nil {
			actionIDs = append(actionIDs, *envelope.ActionID)
		}
	}
	want := []string{"ok", "ok", "ok", "ok", "error", "ok", "error", "error", "error", "error", "error", "error", "error"}
	if strings.Join(statuses, ",") != strings.Join(want, ",") {
		t.Fatalf("statuses=%v want=%v", statuses, want)
	}
	if strings.Join(actionIDs, ",") != "cli-action-01,cli-action-01" {
		t.Fatalf("repeated action identity changed: %v", actionIDs)
	}
	if strings.Contains(stdout.String(), "approval\":true") || strings.Contains(stdout.String(), "private") {
		t.Fatalf("input or private diagnostics leaked: %s", stdout.String())
	}
}

func TestSubprocessCloseFailureDoesNotAppendPrivateEnvelope(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestCLIHelperProcess$")
	command.Env = append(os.Environ(), "COMUSE_CLI_TEST_HELPER=1", "COMUSE_CLI_TEST_COMMAND=serve", "COMUSE_CLI_TEST_CLOSE_FAILURE=1")
	command.Stdin = strings.NewReader(`{"command":"doctor"}` + "\n")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if err == nil {
		t.Fatal("close failure exited successfully")
	}
	if stderr.Len() != 0 || bytes.Count(stdout.Bytes(), []byte{'\n'}) != 1 || strings.Contains(stdout.String(), "private close") {
		t.Fatalf("close failure leaked or duplicated output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	var envelope cliWireEnvelope
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &envelope); err != nil || envelope.Status != "ok" {
		t.Fatalf("close failure changed completed domain response: envelope=%+v err=%v", envelope, err)
	}
}

func TestDispatchOutputIsBoundedAndNativeInputRemainsDisabled(t *testing.T) {
	backend := &cliFixtureBackend{}
	session := newCLIIntegrationSession(t, backend)
	var output bytes.Buffer
	code := cli.Dispatch(context.Background(), session, comuse.OperationDoctor, []byte(`{}`), &output)
	if code != 0 || output.Len() == 0 || output.Len() > 32768 || !bytes.HasSuffix(output.Bytes(), []byte{'\n'}) {
		t.Fatalf("doctor dispatch code=%d bytes=%d", code, output.Len())
	}
	var envelope comuse.ResultEnvelope
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Status != "ok" || envelope.StateStatus != string(comuse.StateUnavailable) {
		t.Fatalf("unexpected shared envelope: %+v", envelope)
	}
	if backend.executions != 0 {
		t.Fatalf("native Execute calls=%d", backend.executions)
	}
}

func TestSemanticCLICommandsRemainTypedUnsupportedWithoutTrustedAuthority(t *testing.T) {
	backend := &cliFixtureBackend{}
	session := newCLIIntegrationSession(t, backend)
	operations, err := session.SemanticOperations(context.Background())
	if err != nil || len(operations) != 0 {
		t.Fatalf("default semantic operations=%v err=%s", operations, comuse.ErrorCode(err))
	}
	stateID := strings.Repeat("a", 64)
	cases := []struct {
		command string
		request string
	}{
		{"click-element", `{"action_id":"cli-action-02","window_ref":"window-1","element_ref":"element-1","state_id":"` + stateID + `"}`},
		{"element-action", `{"action_id":"cli-action-03","window_ref":"window-1","element_ref":"element-1","state_id":"` + stateID + `","kind":"press"}`},
		{"write-element", `{"action_id":"cli-action-04","window_ref":"window-1","element_ref":"element-1","state_id":"` + stateID + `","mode":"replace","text":""}`},
		{"scroll-element", `{"action_id":"cli-action-05","window_ref":"window-1","element_ref":"element-1","state_id":"` + stateID + `","direction":"down","amount":"line"}`},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			operation := comuse.Operation(strings.ReplaceAll(tc.command, "-", "_"))
			attempts := 1
			if tc.command == "click-element" {
				attempts = 2 // Repeating the same action id remains safely unsupported.
			}
			for range attempts {
				var output bytes.Buffer
				if code := cli.Dispatch(context.Background(), session, operation, []byte(tc.request), &output); code != 3 {
					t.Fatalf("typed unsupported exit=%d output=%s", code, output.String())
				}
				var envelope comuse.ResultEnvelope
				if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error == nil || envelope.Error.Code != "unsupported" || envelope.OK || envelope.Execution != string(comuse.ExecutionNotApplied) {
					t.Fatalf("unsupported route became success: %+v", envelope)
				}
			}
		})
	}
	if backend.executions != 0 {
		t.Fatalf("disabled input dispatched %d times", backend.executions)
	}
}

type closeObservedReader struct {
	io.ReadCloser
	started chan struct{}
	closed  chan struct{}
}

func (r *closeObservedReader) Read(p []byte) (int, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	return r.ReadCloser.Read(p)
}
func (r *closeObservedReader) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return r.ReadCloser.Close()
}

func TestServeCancellationAndCloseOutcome(t *testing.T) {
	backend := &cliFixtureBackend{}
	session := newCLIIntegrationSession(t, backend)
	reader, writer := io.Pipe()
	defer func() {
		if err := writer.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("close fixture writer: %v", err)
		}
	}()
	tracked := &closeObservedReader{ReadCloser: reader, started: make(chan struct{}), closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cli.Serve(ctx, session, tracked, io.Discard) }()
	select {
	case <-tracked.started:
	case <-time.After(time.Second):
		t.Fatal("serve did not begin reading")
	}
	cancel()
	select {
	case err := <-done:
		if comuse.ErrorCode(err) != "cancelled" {
			t.Fatalf("serve cancellation code=%s", comuse.ErrorCode(err))
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not stop after cancellation")
	}
	select {
	case <-tracked.closed:
	default:
		t.Fatal("cancel did not close blocked input")
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("safe session close: %s", comuse.ErrorCode(err))
	}
}

func TestServeRejectsOverlongLineWithoutUnboundedOutput(t *testing.T) {
	backend := &cliFixtureBackend{}
	session := newCLIIntegrationSession(t, backend)
	input := strings.NewReader(`{"command":"doctor","pad":"` + strings.Repeat("x", 40000) + `"}` + "\n")
	var output bytes.Buffer
	err := cli.Serve(context.Background(), session, input, &output)
	if comuse.ErrorCode(err) != "invalid_request" {
		t.Fatalf("serve error code=%s", comuse.ErrorCode(err))
	}
	if output.Len() > 32768 || bytes.Count(output.Bytes(), []byte{'\n'}) != 1 {
		t.Fatalf("overlong response exceeded bound: %d", output.Len())
	}
	var envelope cliWireEnvelope
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &envelope); err != nil || envelope.Error == nil || envelope.Error.Code != "invalid_request" {
		t.Fatalf("oversize input did not receive canonical rejection: envelope=%+v err=%v", envelope, err)
	}
}

func TestCLIQualifiedSemanticDispatchAndReplay(t *testing.T) {
	b := newParityMCPActionBackend()
	session, err := comuse.NewSyntheticSessionForTest(comuse.Config{Backend: b, Scope: comuse.Scope{Processes: []comuse.ProcessIdentity{b.process}, ExpiresAt: time.Now().Add(time.Hour)}, Budget: comuse.Budget{MaxDepth: 8, MaxNodes: 64, MaxBytes: 16384, Timeout: time.Second}, ApprovalProvider: parityMCPApproval{}, WriterDirectory: filepath.Join(t.TempDir(), "journal"), WriterKey: bytes.Repeat([]byte{0x72}, 32), MaxActions: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		op    comuse.Operation
		extra map[string]any
	}{{comuse.OperationClickElement, nil}, {comuse.OperationElementAction, map[string]any{"kind": "press"}}, {comuse.OperationWriteElement, map[string]any{"mode": "replace", "text": ""}}, {comuse.OperationScrollElement, map[string]any{"direction": "down", "amount": "page"}}}
	for i, c := range cases {
		obs, err := session.Observe(context.Background(), "window-1")
		if err != nil {
			t.Fatal(err)
		}
		args := map[string]any{"action_id": string(c.op) + "-cli", "window_ref": "window-1", "element_ref": obs.Elements[0].Ref, "state_id": obs.StateID}
		for k, v := range c.extra {
			args[k] = v
		}
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		for repeat := 0; repeat < 2; repeat++ {
			var out bytes.Buffer
			if code := cli.Dispatch(context.Background(), session, c.op, raw, &out); code != 0 {
				t.Fatalf("%s exit%d: %s", c.op, code, out.String())
			}
			var env comuse.ResultEnvelope
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if !env.OK || env.Execution != string(backend.ExecutionApplied) || env.Usage.SerializedTextBytes == 0 {
				t.Fatalf("envelope: %s", out.String())
			}
			if len(b.executionsCopy()) != i+1 {
				t.Fatal("replay dispatched new input")
			}
		}
	}
}

type cliDeveloperFixtureBackend struct{ *parityMCPActionBackend }

func (*cliDeveloperFixtureBackend) Doctor(context.Context) (backend.Doctor, error) {
	return backend.Doctor{Capabilities: backend.Capabilities{Accessibility: true, Input: true, QualifiedInput: true, ActionKinds: []string{backend.ActionClick, backend.ActionTypeText, backend.ActionPressKey, backend.ActionCoordinateScroll, backend.ActionDrag, backend.ActionFocusWindow}}, Permissions: map[string]string{"accessibility": "granted", "input": "granted"}}, nil
}
func TestCLIDeveloperRoutesUseHostScopeAndReplay(t *testing.T) {
	b := &cliDeveloperFixtureBackend{newParityMCPActionBackend()}
	s, err := comuse.NewSyntheticSessionForTest(comuse.Config{Backend: b, Scope: comuse.Scope{Processes: []comuse.ProcessIdentity{b.process}, ExpiresAt: time.Now().Add(time.Hour)}, Budget: comuse.Budget{MaxDepth: 8, MaxNodes: 64, MaxBytes: 16384, Timeout: time.Second}, ApprovalProvider: parityMCPApproval{}, WriterDirectory: filepath.Join(t.TempDir(), "journal"), WriterKey: bytes.Repeat([]byte{0x74}, 32), MaxActions: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	cases := []struct {
		command string
		params  string
	}{
		{"click", `"point":{"x":1,"y":2},"button":"left","count":1,"hold_ms":0`},
		{"type-text", `"text":"héllo 🧭","delay_ms":0`},
		{"press-key", `"keys":["ctrl","a"],"hold_ms":0`},
		{"scroll", `"point":{"x":1,"y":2},"dx":0,"dy":1`},
		{"drag", `"start":{"x":1,"y":2},"end":{"x":3,"y":4},"steps":2,"duration_ms":1`},
		{"focus-window", ""},
	}
	for i, c := range cases {
		raw := `{"action_id":"cli-developer-` + c.command + `","window_ref":"window-1"`
		if c.params != "" {
			raw += "," + c.params
		}
		raw += "}"
		for repeat := 0; repeat < 2; repeat++ {
			var out, diag bytes.Buffer
			if code := cli.Run(context.Background(), s, []string{c.command}, strings.NewReader(raw), &out, &diag); code != 0 {
				t.Fatalf("%s exit%d: %s", c.command, code, out.String())
			}
			var env comuse.ResultEnvelope
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if !env.OK || env.Execution != "applied" || env.Usage.Actions != uint64(1-repeat) {
				t.Fatalf("wrong admission/replay: %s", out.String())
			}
			if diag.Len() != 0 || len(b.executionsCopy()) != i+1 {
				t.Fatal("duplicate dispatch or polluted protocol")
			}
		}
	}
}
