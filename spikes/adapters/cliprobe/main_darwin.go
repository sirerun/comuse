//go:build darwin && cgo

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/sirerun/comuse/spikes/bridgeclient"
	"github.com/sirerun/comuse/spikes/semanticprobe"
)

const (
	maxConfigBytes = 8 * 1024
	maxOutputBytes = 64 * 1024
	exitOK         = 0
	exitPartial    = 2
	exitUsage      = 64
	exitConfig     = 65
	exitNative     = 70
)

// The executable starts on the process main thread on Darwin. Pin that goroutine
// before any work; bridgeclient.Open also takes a balanced lock for its runtime.
func init() { runtime.LockOSThread() }

type trustedConfig struct {
	LibraryPath string `json:"library_path"`
	Fixture     struct {
		PID      int32  `json:"pid"`
		BundleID string `json:"bundle_id"`
		Nonce    string `json:"nonce"`
	} `json:"fixture"`
}

type nativeClient interface {
	Call(context.Context, []byte) ([]byte, error)
	Pump(time.Duration) error
	Close(context.Context) error
}
type clientFactory func(string) (nativeClient, error)

type cliOptions struct {
	configPath    string
	includeValues bool
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, openNative)) }

func openNative(libraryPath string) (nativeClient, error) {
	return bridgeclient.Open(libraryPath)
}

func run(args []string, stdout, stderr io.Writer, open clientFactory) int {
	flags := flag.NewFlagSet("cliprobe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options cliOptions
	flags.StringVar(&options.configPath, "config", "", "trusted local config file")
	flags.BoolVar(&options.includeValues, "include-values", false, "include allowlisted synthetic fixture values (a11y only)")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || options.configPath == "" {
		fmt.Fprintln(stderr, "usage: cliprobe --config <trusted-file> [--include-values] hello|doctor|windows|a11y")
		return exitUsage
	}
	op := flags.Arg(0)
	if op != "hello" && op != "doctor" && op != "windows" && op != "a11y" {
		fmt.Fprintln(stderr, "unsupported read operation")
		return exitUsage
	}
	if options.includeValues && op != "a11y" {
		fmt.Fprintln(stderr, "operation-specific flags are invalid")
		return exitUsage
	}
	config, err := readTrustedConfig(options.configPath)
	if err != nil {
		fmt.Fprintln(stderr, "config:", err)
		return exitConfig
	}
	client, err := open(config.LibraryPath)
	if err != nil {
		fmt.Fprintln(stderr, "native runtime:", err)
		return exitNative
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, callErr := dispatch(ctx, client, op, options, config)
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	closeErr := client.Close(closeCtx)
	closeCancel()
	if callErr != nil {
		fmt.Fprintln(stderr, "probe:", callErr)
		return exitNative
	}
	if closeErr != nil {
		fmt.Fprintln(stderr, "runtime close:", closeErr)
		return exitNative
	}
	if len(response) == 0 || len(response) > maxOutputBytes {
		fmt.Fprintln(stderr, "probe response exceeded output limit")
		return exitNative
	}
	if _, err := stdout.Write(append(response, '\n')); err != nil {
		fmt.Fprintln(stderr, "stdout:", err)
		return exitNative
	}
	var envelope nativeEnvelope
	if err := json.Unmarshal(response, &envelope); err != nil {
		fmt.Fprintln(stderr, "invalid native response")
		return exitNative
	}
	if envelope.Status == "partial" || envelope.Status == "cancelled" {
		return exitPartial
	}
	if envelope.Status != "completed" {
		return exitNative
	}
	return exitOK
}

type nativeEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Status        string          `json:"status"`
	Error         json.RawMessage `json:"error"`
	Result        json.RawMessage `json:"result"`
}

func dispatch(ctx context.Context, client nativeClient, op string, options cliOptions, config trustedConfig) ([]byte, error) {
	scope := map[string]any{"pid": config.Fixture.PID, "bundle_id": config.Fixture.BundleID, "fixture_nonce": config.Fixture.Nonce}
	if op == "hello" || op == "doctor" || op == "windows" {
		request := map[string]any{"schema_version": 1, "request_id": "cliprobe-1", "op": op}
		if op == "windows" {
			request["scope"] = scope
		}
		return callAndPump(ctx, client, request)
	}
	windowResponse, err := callAndPump(ctx, client, map[string]any{"schema_version": 1, "request_id": "cliprobe-windows", "op": "windows", "scope": scope})
	if err != nil {
		return nil, err
	}
	var windows nativeEnvelope
	if err := json.Unmarshal(windowResponse, &windows); err != nil {
		return nil, err
	}
	if windows.Status != "completed" {
		return windowResponse, nil
	}
	var windowResult struct {
		ProcessStartRef string `json:"process_start_ref"`
		Windows         []struct {
			Ref string `json:"ref"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(windows.Result, &windowResult); err != nil {
		return nil, err
	}
	if len(windowResult.Windows) != 1 {
		return nil, errors.New("fixture window selection is ambiguous")
	}
	selectedRef := windowResult.Windows[0].Ref
	if selectedRef == "" || windowResult.ProcessStartRef == "" {
		return nil, errors.New("selected window reference is empty")
	}
	request := map[string]any{"schema_version": 1, "request_id": "cliprobe-a11y", "op": "a11y", "scope": scope, "window_ref": selectedRef, "include_values": options.includeValues}
	response, err := callAndPump(ctx, client, request)
	if err != nil {
		return nil, err
	}
	var observation nativeEnvelope
	if err := json.Unmarshal(response, &observation); err != nil {
		return nil, err
	}
	var observed struct {
		WindowRef string `json:"window_ref"`
	}
	if err := json.Unmarshal(observation.Result, &observed); err != nil {
		return nil, err
	}
	if observation.Status == "completed" && observed.WindowRef != selectedRef {
		return nil, errors.New("selected window identity changed between enumeration and observation")
	}
	if observation.Status != "completed" && observation.Status != "partial" {
		return response, nil
	}
	snapshot, err := semanticprobe.NormalizeEnvelope(response, semanticprobe.ExpectedScope{
		RequestID: "cliprobe-a11y", PID: config.Fixture.PID, BundleID: config.Fixture.BundleID,
		FixtureNonce: config.Fixture.Nonce, ProcessLaunchGeneration: windowResult.ProcessStartRef,
		ProcessStartRef: windowResult.ProcessStartRef, WindowRef: selectedRef,
	}, semanticprobe.Options{IncludeSyntheticNormalValue: options.includeValues})
	if err != nil {
		return json.Marshal(map[string]any{"schema_version": 1, "request_id": "cliprobe-a11y", "status": "error", "error": "semantic_projection_failed"})
	}
	status := "completed"
	if snapshot.Status == semanticprobe.SnapshotPartial {
		status = "partial"
	}
	return json.Marshal(map[string]any{"schema_version": 1, "request_id": snapshot.RequestID, "status": status, "result": snapshot})
}

func callAndPump(ctx context.Context, client nativeClient, request map[string]any) ([]byte, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if len(payload) > 32*1024 {
		return nil, errors.New("native request exceeds size limit")
	}
	type callResult struct {
		response []byte
		err      error
	}
	result := make(chan callResult, 1)
	go func() { response, err := client.Call(ctx, payload); result <- callResult{response, err} }()
	for {
		select {
		case completed := <-result:
			requestID, _ := request["request_id"].(string)
			envelope, validationErr := validateNativeTerminal(completed.response, requestID)
			if validationErr != nil {
				return nil, errors.Join(completed.err, validationErr)
			}
			if completed.err != nil {
				var nativeErr *bridgeclient.NativeError
				if !errors.As(completed.err, &nativeErr) || (nativeErr.Status != "error" && nativeErr.Status != "cancelled") || envelope.Status != nativeErr.Status {
					return nil, completed.err
				}
			}
			return completed.response, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			if err := client.Pump(10 * time.Millisecond); err != nil {
				return nil, err
			}
		}
	}
}

func validateNativeTerminal(data []byte, expectedRequestID string) (nativeEnvelope, error) {
	var envelope nativeEnvelope
	if len(data) == 0 || len(data) > maxOutputBytes || expectedRequestID == "" {
		return envelope, errors.New("native response size or request identity is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return envelope, fmt.Errorf("invalid native response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return envelope, errors.New("native response contains trailing JSON")
	}
	if envelope.SchemaVersion != 1 || envelope.RequestID != expectedRequestID {
		return envelope, errors.New("native response identity mismatch")
	}
	switch envelope.Status {
	case "completed", "partial":
		if len(envelope.Result) == 0 || string(envelope.Result) == "null" || (len(envelope.Error) != 0 && string(envelope.Error) != "null") {
			return envelope, errors.New("native response has an invalid success terminal")
		}
	case "error", "cancelled":
		if len(envelope.Error) == 0 || string(envelope.Error) == "null" {
			return envelope, errors.New("native response has an invalid failure terminal")
		}
	default:
		return envelope, errors.New("native response has an unsupported terminal status")
	}
	return envelope, nil
}

func readTrustedConfig(path string) (trustedConfig, error) {
	var config trustedConfig
	if !filepath.IsAbs(path) {
		return config, errors.New("config path must be absolute")
	}
	parentInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return config, err
	}
	parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 || !ok || parentStat.Uid != uint32(os.Geteuid()) || parentInfo.Mode().Perm()&0022 != 0 {
		return config, errors.New("config directory must be a current-user-owned real directory not writable by group or others")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return config, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return config, errors.New("config descriptor is invalid")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return config, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return config, errors.New("config must be a regular file not writable by group or others")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return config, errors.New("config must be owned by the current user")
	}
	if info.Size() <= 0 || info.Size() > maxConfigBytes {
		return config, errors.New("config size is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return config, err
	}
	if len(data) == 0 || len(data) > maxConfigBytes {
		return config, errors.New("config size is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return config, errors.New("config contains trailing data")
	}
	if !filepath.IsAbs(config.LibraryPath) || config.Fixture.PID <= 0 || config.Fixture.BundleID != "com.sirerun.comuse.fixture" || len(config.Fixture.Nonce) < 1 || len(config.Fixture.Nonce) > 64 {
		return config, errors.New("config must contain an absolute library path and fixed fixture identity")
	}
	for _, char := range config.Fixture.Nonce {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char)) {
			return config, errors.New("fixture nonce has invalid characters")
		}
	}
	return config, nil
}
