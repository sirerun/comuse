package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sirerun/comuse"
)

func TestConfigStrictness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host.json")
	for _, body := range []string{
		`{"library_path":"/x","approval":true}`,
		`{"library_path":"/x","library_path":"/y"}`,
		`{"library_path":"/x","writer_key":"secret"}`,
		`{"library_path":"/x","allow_input":true}`,
		`{"library_path":"relative"}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(path); err == nil {
			t.Fatalf("accepted config %s", body)
		}
	}
	if err := os.WriteFile(path, []byte(`{"library_path":"/native/library"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err != nil {
		t.Fatal(err)
	}
}

type closeFailureReader struct {
	io.Reader
	err    error
	closed int
}

func (r *closeFailureReader) Close() error {
	r.closed++
	return r.err
}

func TestConfigCloseFailureIsSanitizedAndClosedOnce(t *testing.T) {
	r := &closeFailureReader{Reader: strings.NewReader(`{"library_path":"/native/library"}`), err: io.ErrUnexpectedEOF}
	var cfg hostConfig
	err := decodeConfig(r, &cfg)
	if err == nil || err.Error() != "config close failed" || r.closed != 1 {
		t.Fatalf("decodeConfig err=%v closes=%d", err, r.closed)
	}
}

func TestPreSessionRejectionIsOneSharedEnvelope(t *testing.T) {
	var output bytes.Buffer
	if got := emitRejection(&output, "invalid_request"); got != 2 {
		t.Fatalf("exit=%d", got)
	}
	var envelope comuse.ResultEnvelope
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Status != "error" || envelope.Error == nil || envelope.Error.Code != "invalid_request" || !bytes.HasSuffix(output.Bytes(), []byte{'\n'}) {
		t.Fatalf("unexpected rejection envelope: %s", output.Bytes())
	}
	if strings.Contains(output.String(), "private") {
		t.Fatalf("unexpected private text: %s", output.String())
	}
}

func TestInvalidArgumentsDoNotReachHostOrPolluteStderr(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--config", "/private/path", "Doctor"},
		{"--config", "/private/path", "doctor", "windows"},
		{"--config", "/private/path", "--ledger"},
	} {
		var output, diagnostics bytes.Buffer
		if code := run(t.Context(), args, strings.NewReader(""), &output, &diagnostics); code != 2 {
			t.Fatalf("args=%v exit=%d", args, code)
		}
		if diagnostics.Len() != 0 || strings.Contains(output.String(), "/private/path") {
			t.Fatalf("args=%v leaked diagnostics=%q output=%q", args, diagnostics.String(), output.String())
		}
		var envelope comuse.ResultEnvelope
		if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
			t.Fatalf("args=%v envelope: %v", args, err)
		}
		if envelope.Error == nil || envelope.Error.Code != "invalid_request" || !bytes.HasSuffix(output.Bytes(), []byte{'\n'}) {
			t.Fatalf("args=%v unexpected envelope: %s", args, output.String())
		}
	}
}

func TestStateLedgerLaunchFlagReachesHostDispatcher(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "host.json")
	if err := os.WriteFile(config, []byte(`{"library_path":"/native/library"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	code := run(t.Context(), []string{"--config", config, "state", "--ledger"}, strings.NewReader(""), &output, &diagnostics)
	if code == 2 {
		t.Fatalf("state --ledger rejected before host dispatch: %s", output.String())
	}
	if diagnostics.Len() != 0 || strings.Contains(output.String(), config) {
		t.Fatalf("diagnostics=%q output=%q", diagnostics.String(), output.String())
	}
	var envelope comuse.ResultEnvelope
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error == nil || (envelope.Error.Code != "unsupported" && (runtime.GOOS != "darwin" || envelope.Error.Code != "backend_unavailable")) {
		t.Fatalf("host setup did not reach the platform backend admission gate: %s", output.String())
	}
}
