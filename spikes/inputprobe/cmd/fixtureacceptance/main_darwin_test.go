//go:build darwin && cgo

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/sirerun/comuse/spikes/inputprobe"
)

const validConfigJSON = `{"schema_version":1,"scenario":"read_normal_value","library_path":"/tmp/libBridgeProbe.dylib","fixture_pid":42,"fixture_nonce":"nonce-1","private_journal_root":"/tmp/private-journal","journal_key_file":"/tmp/journal.key","action_commitment_key_file":"/tmp/action.key"}`

func TestReadTrustedConfigRejectsDuplicateUnknownAndTrailingData(t *testing.T) {
	fixtures := map[string]string{
		"duplicate": stringsReplaceOnce(validConfigJSON, `"schema_version":1`, `"schema_version":1,"schema_version":1`),
		"unknown":   stringsReplaceOnce(validConfigJSON, `"fixture_nonce":"nonce-1"`, `"fixture_nonce":"nonce-1","model_approval":true`),
		"trailing":  validConfigJSON + ` {}`,
	}
	for name, content := range fixtures {
		t.Run(name, func(t *testing.T) {
			path := writePrivateTestFile(t, t.TempDir(), "config.json", []byte(content), 0o600)
			if _, err := readTrustedConfig(path); err == nil {
				t.Fatal("accepted ambiguous or extended config")
			}
		})
	}
	path := writePrivateTestFile(t, privateTestDir(t), "valid.json", []byte(validConfigJSON), 0o600)
	if _, err := readTrustedConfig(path); err != nil {
		t.Fatalf("valid closed schema was refused: %v", err)
	}
}

func TestValidateTrustedConfigRejectsInvalidScenarioPIDAndNonce(t *testing.T) {
	base := trustedConfig{
		SchemaVersion: 1, Scenario: inputprobe.FixtureReadNormalValue,
		LibraryPath: "/tmp/libBridgeProbe.dylib", FixturePID: 42, FixtureNonce: "nonce-1",
		PrivateJournalRoot: "/tmp/journal", JournalKeyFile: "/tmp/journal.key", CommitmentKeyFile: "/tmp/action.key",
	}
	tests := map[string]func(*trustedConfig){
		"scenario":      func(config *trustedConfig) { config.Scenario = "arbitrary_action" },
		"zero pid":      func(config *trustedConfig) { config.FixturePID = 0 },
		"oversized pid": func(config *trustedConfig) { config.FixturePID = int64(^uint32(0)) },
		"invalid nonce": func(config *trustedConfig) { config.FixtureNonce = "nonce/with/path" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			config := base
			mutate(&config)
			if err := validateTrustedConfig(config, "/tmp/config.json"); err == nil {
				t.Fatal("accepted invalid fixed fixture configuration")
			}
		})
	}
}

func TestReadPrivateKeyRequiresNoFollowRegular0600Exact32Bytes(t *testing.T) {
	dir := privateTestDir(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	path := writePrivateTestFile(t, dir, "key", key, 0o600)
	got, err := readPrivateKey(path)
	if err != nil || len(got) != keyBytes || !bytes.Equal(got, key) {
		t.Fatalf("valid key read = (%d bytes, %v)", len(got), err)
	}
	clearBytes(got)

	for _, test := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{name: "short", data: key[:31], mode: 0o600},
		{name: "long", data: append(append([]byte(nil), key...), '!'), mode: 0o600},
		{name: "permissive", data: key, mode: 0o644},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := writePrivateTestFile(t, privateTestDir(t), "key", test.data, test.mode)
			if _, err := readPrivateKey(candidate); err == nil {
				t.Fatal("accepted non-private or wrong-size key")
			}
		})
	}

	link := filepath.Join(dir, "key-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateKey(link); err == nil {
		t.Fatal("followed a key symlink")
	}
}

func TestPrivateDirectoryRequiresOwned0700NonSymlink(t *testing.T) {
	dir := privateTestDir(t)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	uid := uint32(syscall.Getuid())
	if err := validatePrivateDirectoryInfo(info, uid); err != nil {
		t.Fatalf("current-owner directory rejected: %v", err)
	}
	if err := validatePrivateDirectoryInfo(info, uid+1); err == nil {
		t.Fatal("accepted a mismatched directory owner")
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateDirectory(dir); err == nil {
		t.Fatal("accepted a non-private directory mode")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "private-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateDirectory(link); err == nil {
		t.Fatal("accepted a symlinked private directory")
	}
}

func TestPrivateFileInfoRequiresCurrentOwner(t *testing.T) {
	path := writePrivateTestFile(t, privateTestDir(t), "key", []byte("0123456789abcdef0123456789abcdef"), 0o600)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	uid := uint32(syscall.Getuid())
	if err := validatePrivateFileInfo(info, keyBytes, uid); err != nil {
		t.Fatalf("current-owner key rejected: %v", err)
	}
	if err := validatePrivateFileInfo(info, keyBytes, uid+1); err == nil {
		t.Fatal("accepted a mismatched key owner")
	}
}

func TestValidateDistinctKeysRequiresDifferentBytesAndPaths(t *testing.T) {
	first := []byte("0123456789abcdef0123456789abcdef")
	second := []byte("abcdef0123456789abcdef0123456789")
	if err := validateDistinctKeys("/private/journal.key", "/private/action.key", first, second); err != nil {
		t.Fatalf("distinct caller keys refused: %v", err)
	}
	if err := validateDistinctKeys("/private/journal.key", "/private/action.key", first, first); err == nil {
		t.Fatal("accepted identical key material")
	}
	if err := validateDistinctKeys("/private/same.key", "/private/same.key", first, second); err == nil {
		t.Fatal("accepted one key path for two commitments")
	}
}

func TestBoundedReportRejectsUnknownValuesCapsOutputAndReturnsWriterErrors(t *testing.T) {
	report := inputprobe.FixtureAcceptanceReport{
		SchemaVersion: 1, Scenario: inputprobe.FixtureReadNormalValue, Status: "held",
		Execution: "unknown", Verification: "unavailable", StateStatus: "unavailable", Cleanup: "unknown",
	}
	var output bytes.Buffer
	report.StateStatus = "private-path-canary"
	if err := writeBoundedReport(&output, report); err == nil || output.Len() != 0 {
		t.Fatal("accepted an unsafe report field")
	}
	if bytes.Contains(output.Bytes(), []byte("private-path-canary")) {
		t.Fatal("unsafe report leaked a private value")
	}
	if err := writeBoundedBytes(&output, bytes.Repeat([]byte("x"), maxReportBytes+1)); err == nil || output.Len() != 0 {
		t.Fatal("accepted an oversized report")
	}
	if err := writeBoundedBytes(failingWriter{}, []byte("bounded")); err == nil {
		t.Fatal("ignored report writer failure")
	}
	report.StateStatus = "unavailable"
	report.ErrorCode = "safe_code"
	if err := writeBoundedReport(&output, report); err != nil {
		t.Fatalf("rejected a bounded safe report: %v", err)
	}
}

func TestRunUsesGenericErrorsWithoutPrivateConfigPathOrKeyContent(t *testing.T) {
	dir := privateTestDir(t)
	secretPath := filepath.Join(dir, "private-path-canary", "config.json")
	secret := []byte("private-key-content-canary")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--config", secretPath}, &stdout, &stderr)
	if code == 0 || stdout.Len() != 0 {
		t.Fatalf("missing config run = code %d, stdout %q", code, stdout.String())
	}
	if bytes.Contains(stderr.Bytes(), []byte(secretPath)) || bytes.Contains(stderr.Bytes(), secret) {
		t.Fatal("printed private path or key content in generic error")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("writer failure") }

func privateTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writePrivateTestFile(t *testing.T, dir, name string, data []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func stringsReplaceOnce(value, old, replacement string) string {
	return strings.Replace(value, old, replacement, 1)
}
