package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/sirerun/comuse"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	}{{"permission_denied", 3}, {"policy_refused", 4}, {"invalid_request", 2}, {"cancelled", 1}, {"private native contents", 1}} {
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
