package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/sirerun/comuse"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInvalidRequestsAreBoundedAndPrivate(t *testing.T) {
	for _, args := range [][]string{{}, {"--config", "/not-present-private-path", "doctor"}, {"--config", "x", "write"}} {
		var out, err bytes.Buffer
		exit := run(context.Background(), args, strings.NewReader(""), &out, &err)
		if exit == 0 {
			t.Fatal("invalid command succeeded")
		}
		if strings.Contains(out.String(), "private-path") {
			t.Fatal("path leaked")
		}
	}
}
func TestConfigStrictness(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "host.json")
	for _, text := range []string{`{"library_path":"/x","approval":true}`, `{"library_path":"/x","library_path":"/y"}`, `{"library_path":"relative"}`} {
		if e := os.WriteFile(p, []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := loadConfig(p); e == nil {
			t.Fatal("accepted invalid config")
		}
	}
	if e := os.WriteFile(p, []byte(`{"library_path":"/native/library"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := loadConfig(p); e != nil {
		t.Fatal(e)
	}
}
func TestErrorEnvelopeAndExitCodes(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"permission_denied", 3}, {"policy_refused", 2}, {"approval_required", 2}, {"desktop_busy", 4}, {"rate_limited", 1}, {"budget_exceeded", 1}, {"invalid_request", 2}, {"cancelled", 1}, {"private native contents", 1}} {
		var out bytes.Buffer
		if n := emitError(&out, tc.code); n != tc.exit {
			t.Fatalf("exit %d", n)
		}
		var env comuse.Envelope
		if e := json.Unmarshal(out.Bytes(), &env); e != nil {
			t.Fatal(e)
		}
		if env.Status != "error" || env.SchemaVersion != 1 || env.Error == nil {
			t.Fatalf("bad envelope %+v", env)
		}
		if tc.code == "private native contents" && strings.Contains(out.String(), "native contents") {
			t.Fatal("untrusted code leaked")
		}
	}
}

func TestPersistentCLIReadSession(t *testing.T) {
	b := &cliBackend{}
	s, e := comuse.NewSession(comuse.Config{Backend: b, Scope: comuse.Scope{Processes: []comuse.ProcessIdentity{{PID: 7, BundleID: "test.fixture", LaunchID: "launch-1"}}, ExpiresAt: time.Now().Add(time.Minute)}, Budget: comuse.Budget{MaxDepth: 8, MaxNodes: 32, MaxBytes: 32768, Timeout: time.Second}, AllowValues: true})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	commands := `{"command":"windows"}` + "\n" + `{"command":"a11y","window_ref":"w1"}` + "\n" + `{"command":"a11y","window_ref":"w1","unknown":true}` + "\n"
	var out bytes.Buffer
	if e = serveCLI(context.Background(), s, strings.NewReader(commands), &out); e != nil {
		t.Fatal(e)
	}
	dec := json.NewDecoder(&out)
	var envelopes []comuse.Envelope
	for dec.More() {
		var env comuse.Envelope
		if e = dec.Decode(&env); e != nil {
			t.Fatal(e)
		}
		envelopes = append(envelopes, env)
	}
	if len(envelopes) != 3 || envelopes[0].Status != "ok" || envelopes[1].Status != "ok" || envelopes[2].Status != "error" {
		t.Fatalf("responses %+v", envelopes)
	}
	if b.executions != 0 {
		t.Fatal("readonly CLI dispatched input")
	}
}

type cliBackend struct{ executions int }

func (*cliBackend) Doctor(context.Context) (comuse.DoctorReport, error) {
	return comuse.DoctorReport{Capabilities: comuse.Capabilities{Accessibility: true}, Permissions: map[string]string{"accessibility": "available"}}, nil
}
func (*cliBackend) Windows(context.Context, comuse.Budget) ([]comuse.Window, error) {
	return []comuse.Window{{Ref: "w1", Process: comuse.ProcessIdentity{PID: 7, BundleID: "test.fixture", LaunchID: "launch-1"}, Title: "Fixture"}}, nil
}
func (*cliBackend) Observe(context.Context, string, comuse.Budget) (comuse.Observation, error) {
	v := "fixture"
	return comuse.Observation{WindowRef: "w1", StateID: "native-1", ObservedAt: time.Now(), Elements: []comuse.Element{{Ref: "e1", Role: "AXTextField", Classification: "normal", Value: &v}}, Coverage: comuse.Coverage{Complete: true}}, nil
}
func (*cliBackend) ReadElement(context.Context, string, string, string, comuse.Budget) (comuse.ElementContent, error) {
	return comuse.ElementContent{WindowRef: "w1", ElementRef: "e1", StateID: "native-1", Text: "fixture"}, nil
}
func (b *cliBackend) Execute(context.Context, comuse.Action) (comuse.ActionResult, error) {
	b.executions++
	return comuse.ActionResult{}, &comuse.Error{Code: "unsupported", Message: "unsupported"}
}
func (*cliBackend) Close(context.Context) error { return nil }

func newCLISession(t *testing.T) *comuse.Session {
	t.Helper()
	s, e := comuse.NewSession(comuse.Config{Backend: &cliBackend{}, Scope: comuse.Scope{Processes: []comuse.ProcessIdentity{{PID: 7, BundleID: "test.fixture", LaunchID: "launch-1"}}, ExpiresAt: time.Now().Add(time.Minute)}, Budget: comuse.Budget{MaxDepth: 8, MaxNodes: 32, MaxBytes: 32768, Timeout: time.Second}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}
func TestPersistentCLIFailedOutput(t *testing.T) {
	s := newCLISession(t)
	if e := serveCLI(context.Background(), s, strings.NewReader(`{"unknown":true}`), failedWriter{}); e == nil {
		t.Fatal("output failure reported success")
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("private transport failure") }
func TestPersistentCLICancelClosesRead(t *testing.T) {
	s := newCLISession(t)
	ctx, cancel := context.WithCancel(context.Background())
	raw, w := io.Pipe()
	r := &observedReader{ReadCloser: raw, started: make(chan struct{}), closed: make(chan struct{})}
	defer w.Close()
	done := make(chan error, 1)
	go func() { done <- serveCLI(ctx, s, r, io.Discard) }()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("reader did not start")
	}
	cancel()
	select {
	case e := <-done:
		select {
		case <-r.closed:
		default:
			t.Fatal("reader not closed")
		}
		if comuse.ErrorCode(e) != "cancelled" {
			t.Fatalf("cancel: %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled read did not exit")
	}
}
func TestPersistentCLIErrorSanitization(t *testing.T) {
	env := errorEnvelope("private backend payload")
	b, e := json.Marshal(env)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "private backend") {
		t.Fatal("untrusted code leaked")
	}
	if env.Error.Code != "internal_error" {
		t.Fatal("unknown code not normalized")
	}
}

type observedReader struct {
	io.ReadCloser
	started   chan struct{}
	closed    chan struct{}
	readOnce  sync.Once
	closeOnce sync.Once
}

func (r *observedReader) Read(p []byte) (int, error) {
	r.readOnce.Do(func() { close(r.started) })
	return r.ReadCloser.Read(p)
}
func (r *observedReader) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return r.ReadCloser.Close()
}

func boolPointer(value bool) *bool { return &value }

func TestInvokeExplicitReadUsesSessionState(t *testing.T) {
	s, err := comuse.NewSession(comuse.Config{Backend: &cliBackend{}, Scope: cliScope(), Budget: backend.Budget{MaxDepth: 8, MaxNodes: 64, MaxBytes: 8192, Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	if _, err = invoke(context.Background(), s, request{Command: "windows"}); err != nil {
		t.Fatal(err)
	}
	observed, err := invoke(context.Background(), s, request{Command: "a11y", WindowRef: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	state := observed.(comuse.Observation)
	content, err := invoke(context.Background(), s, request{Command: "read-element", WindowRef: "w1", ElementRef: "e1", StateID: state.StateID})
	if err != nil {
		t.Fatal(err)
	}
	read := content.(comuse.ElementContent)
	if read.Text != "explicit text" || read.StateID != state.StateID || read.StateID == "native-s" {
		t.Fatalf("unexpected read: %#v", read)
	}
}
