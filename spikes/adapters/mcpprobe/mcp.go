// Package mcpprobe adapts a host-owned, fixture-scoped read-only backend to MCP.
// It owns no native initialization and never accepts model-supplied scope data.
package mcpprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sirerun/comuse/spikes/semanticprobe"
)

const (
	ProtocolVersion = "2025-06-18"
	FixtureBundleID = semanticprobe.FixtureBundleID
	MaxFrameBytes   = 64 * 1024
	maxToolFrame    = MaxFrameBytes - 1024
)

var (
	ErrInvalidConfig = errors.New("invalid trusted MCP host configuration")
	ErrInvalidArgs   = errors.New("invalid tool arguments")
	ErrStaleWindow   = errors.New("window reference is not from the current fixture observation")
	ErrResultLimit   = errors.New("tool result exceeds the bounded MCP response size")
)

// Config is supplied by trusted host startup code. Tool inputs cannot change it.
type Config struct {
	NativeLibraryPath       string
	FixturePID              int
	FixtureBundleID         string
	FixtureNonce            string
	ProcessLaunchGeneration string
}

// Scope is fixed for the lifetime of one server instance.
type Scope struct {
	NativeLibraryPath       string `json:"-"`
	FixturePID              int    `json:"fixture_pid"`
	FixtureBundleID         string `json:"fixture_bundle_id"`
	FixtureNonce            string `json:"fixture_nonce"`
	ProcessLaunchGeneration string `json:"process_launch_generation"`
}

type Check struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Prompted bool   `json:"prompted"`
}

type HelloResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type DoctorResult struct {
	Status string  `json:"status"`
	Checks []Check `json:"checks"`
}

type Window struct {
	WindowRef string `json:"window_ref"`
	Role      string `json:"role"`
}

type WindowsResult struct {
	Status          string   `json:"status"`
	ObservationID   string   `json:"observation_id,omitempty"`
	ProcessStartRef string   `json:"process_start_ref,omitempty"`
	Windows         []Window `json:"windows,omitempty"`
	WindowCount     int      `json:"window_count"`
}

// WindowObservation is a bounded semantic-only response from the host backend.
// A successful result contains refs from one current native windows observation.
type WindowObservation struct {
	Status          string   `json:"status"`
	ObservationID   string   `json:"observation_id"`
	ProcessStartRef string   `json:"process_start_ref"`
	Windows         []Window `json:"windows"`
}

// Backend is already bound to the host's pinned main-thread native runtime.
// Implementations must dispatch native work through its main-thread pump and
// revalidate the configured process/window identity for every operation.
type Backend interface {
	Hello(context.Context, Scope) (HelloResult, error)
	Doctor(context.Context, Scope) (DoctorResult, error)
	Windows(context.Context, Scope) (WindowObservation, error)
	Accessibility(context.Context, Scope, string, string, bool) (semanticprobe.Snapshot, error)
}

type Server struct {
	server           *mcp.Server
	config           Scope
	backend          Backend
	mu               sync.Mutex
	windows          *WindowObservation
	windowSession    string
	windowGeneration uint64
}

type toolReply struct {
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
	Data   any    `json:"data,omitempty"`
}

type emptyArgs struct{}

type accessibilityArgs struct {
	WindowRef     string `json:"window_ref"`
	IncludeValues bool   `json:"include_values,omitempty"`
}

func New(config Config, backend Backend, logger *slog.Logger) (*Server, error) {
	if backend == nil || ValidateConfig(config) != nil {
		return nil, ErrInvalidConfig
	}
	scope := Scope{
		NativeLibraryPath: config.NativeLibraryPath, FixturePID: config.FixturePID,
		FixtureBundleID: config.FixtureBundleID, FixtureNonce: config.FixtureNonce,
		ProcessLaunchGeneration: config.ProcessLaunchGeneration,
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "comuse-fixture", Version: "0.1.0"}, &mcp.ServerOptions{
		Logger:                    logger,
		Capabilities:              &mcp.ServerCapabilities{},
		SupportedProtocolVersions: []string{ProtocolVersion},
	})
	adapter := &Server{server: server, config: scope, backend: backend}
	adapter.registerTools()
	return adapter, nil
}

func ValidateConfig(config Config) error {
	if config.FixturePID <= 0 || config.FixturePID > int(1<<31-1) || config.FixtureBundleID != FixtureBundleID ||
		!validNonce(config.FixtureNonce) || !safeToken(config.ProcessLaunchGeneration, 128) ||
		!filepath.IsAbs(config.NativeLibraryPath) || strings.TrimSpace(config.NativeLibraryPath) != config.NativeLibraryPath || strings.ContainsRune(config.NativeLibraryPath, '\x00') {
		return ErrInvalidConfig
	}
	return nil
}

// RunStdio serves the official SDK stdio transport with an explicit inbound cap.
// Call it on an MCP worker goroutine while the host pumps native main-thread work.
func (server *Server) RunStdio(ctx context.Context) error {
	return server.server.Run(ctx, server.stdioTransport())
}

func (server *Server) stdioTransport() *mcp.StdioTransport {
	return &mcp.StdioTransport{MaxLineLength: MaxFrameBytes}
}

func (server *Server) registerTools() {
	server.server.AddTool(&mcp.Tool{Name: "hello", Description: "Report fixture probe readiness.", InputSchema: objectSchema(nil)}, server.handleHello)
	server.server.AddTool(&mcp.Tool{Name: "doctor", Description: "Report bounded fixture probe checks.", InputSchema: objectSchema(nil)}, server.handleDoctor)
	server.server.AddTool(&mcp.Tool{Name: "windows", Description: "List the configured synthetic fixture window.", InputSchema: objectSchema(nil)}, server.handleWindows)
	server.server.AddTool(&mcp.Tool{Name: "a11y", Description: "Read a semantic snapshot of a window returned by this session's latest complete windows call.", InputSchema: objectSchema(map[string]any{
		"window_ref":     map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		"include_values": map[string]any{"type": "boolean"},
	}, []string{"window_ref"})}, server.handleAccessibility)
}

func (server *Server) handleHello(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := decodeArgs(request.Params.Arguments, &emptyArgs{}); err != nil {
		return server.reply("error", nil, ErrInvalidArgs)
	}
	result, err := server.backend.Hello(ctx, server.config)
	if err == nil && (result.Status != "complete" || result.Message != "hello") {
		err = ErrNativeContract
	}
	return server.reply(result.Status, result, err)
}

func (server *Server) handleDoctor(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := decodeArgs(request.Params.Arguments, &emptyArgs{}); err != nil {
		return server.reply("error", nil, ErrInvalidArgs)
	}
	result, err := server.backend.Doctor(ctx, server.config)
	if err == nil {
		if result.Status != "complete" || len(result.Checks) > 2 {
			err = ErrNativeContract
		} else {
			for _, check := range result.Checks {
				if (check.Name != "accessibility" && check.Name != "event_posting") ||
					(check.Status != "available" && check.Status != "unavailable") || check.Prompted {
					err = ErrNativeContract
					break
				}
			}
		}
	}
	return server.reply(result.Status, result, err)
}

func (server *Server) handleWindows(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := decodeArgs(request.Params.Arguments, &emptyArgs{}); err != nil {
		return server.reply("error", nil, ErrInvalidArgs)
	}
	server.clearWindows()
	observation, err := server.backend.Windows(ctx, server.config)
	if err != nil {
		return server.reply("error", nil, err)
	}
	if len(observation.Windows) > 32 {
		return server.reply("error", nil, ErrResultLimit)
	}
	if observation.Status != "complete" && observation.Status != "partial" {
		return server.reply("error", nil, ErrNativeContract)
	}
	result := WindowsResult{Status: normalizeStatus(observation.Status), WindowCount: len(observation.Windows)}
	if observation.Status != "complete" {
		server.clearWindows()
		return server.reply(result.Status, result, nil)
	}
	if len(observation.Windows) != 1 {
		result.Status = "ambiguous"
		server.clearWindows()
		return server.reply(result.Status, result, nil)
	}
	if !safeToken(observation.ObservationID, 128) || !safeToken(observation.ProcessStartRef, 128) {
		server.clearWindows()
		return server.reply("error", nil, ErrNativeContract)
	}
	window := observation.Windows[0]
	if !safeToken(window.WindowRef, 128) || window.Role != "AXWindow" {
		server.clearWindows()
		return server.reply("error", nil, ErrStaleWindow)
	}
	server.rememberWindows(request.Session.ID(), observation)
	result.ObservationID = observation.ObservationID
	result.ProcessStartRef = observation.ProcessStartRef
	result.Windows = []Window{window}
	return server.reply("complete", result, nil)
}

func (server *Server) handleAccessibility(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args accessibilityArgs
	if err := decodeArgs(request.Params.Arguments, &args); err != nil || !safeToken(args.WindowRef, 128) {
		return server.reply("error", nil, ErrInvalidArgs)
	}
	observation, generation, ok := server.currentWindows(request.Session.ID())
	if !ok || len(observation.Windows) != 1 || observation.Windows[0].WindowRef != args.WindowRef {
		return server.reply("error", nil, ErrStaleWindow)
	}
	snapshot, err := server.backend.Accessibility(ctx, server.config, observation.ProcessStartRef, args.WindowRef, args.IncludeValues)
	if err != nil {
		return server.reply("error", nil, err)
	}
	if !server.windowsStillCurrent(request.Session.ID(), generation) {
		return server.reply("error", nil, ErrStaleWindow)
	}
	if snapshot.BundleID != server.config.FixtureBundleID || snapshot.FixtureNonce != server.config.FixtureNonce ||
		snapshot.ProcessStartRef != observation.ProcessStartRef || snapshot.WindowRef != args.WindowRef {
		return server.reply("error", nil, ErrNativeContract)
	}
	if snapshot.Status == semanticprobe.SnapshotPartial {
		if snapshot.NativeStateID != "" || snapshot.CanonicalStateID != "" {
			return server.reply("error", nil, ErrNativeContract)
		}
		return server.reply("partial", snapshot, nil)
	}
	if snapshot.Status != semanticprobe.SnapshotComplete || snapshot.Coverage.Status != "complete" || snapshot.NativeStateID == "" || snapshot.CanonicalStateID == "" {
		return server.reply("error", nil, ErrNativeContract)
	}
	return server.reply("complete", snapshot, nil)
}

func (server *Server) rememberWindows(sessionID string, observation WindowObservation) {
	server.mu.Lock()
	defer server.mu.Unlock()
	copy := observation
	server.windows = &copy
	server.windowSession = sessionID
	server.windowGeneration++
}

func (server *Server) currentWindows(sessionID string) (WindowObservation, uint64, bool) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.windows == nil || server.windowSession != sessionID {
		return WindowObservation{}, 0, false
	}
	return *server.windows, server.windowGeneration, true
}

func (server *Server) windowsStillCurrent(sessionID string, generation uint64) bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.windows != nil && server.windowSession == sessionID && server.windowGeneration == generation
}

func (server *Server) clearWindows() {
	server.mu.Lock()
	server.windows = nil
	server.windowSession = ""
	server.windowGeneration++
	server.mu.Unlock()
}

func (server *Server) reply(status string, data any, err error) (*mcp.CallToolResult, error) {
	status = normalizeStatus(status)
	reply := toolReply{Status: status, Data: data}
	isError := err != nil || status == "error"
	if err != nil {
		reply.Status = "error"
		if isCancelledError(err) {
			reply.Status = "cancelled"
		}
		reply.Code = errorCode(err)
		reply.Data = nil
	} else if status == "error" {
		reply.Code = "backend_unavailable"
		reply.Data = nil
	}
	encoded, marshalErr := json.Marshal(reply)
	if marshalErr != nil {
		encoded = []byte(`{"status":"error","code":"encode_error"}`)
		isError = true
	}
	result := &mcp.CallToolResult{IsError: isError, Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}
	wire, wireErr := json.Marshal(result)
	if wireErr != nil || len(wire) > maxToolFrame {
		result = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: `{"status":"error","code":"result_limit"}`}}}
	}
	return result, nil
}

func errorCode(err error) string {
	if code := nativeErrorCode(err); code != "" {
		return mapNativeCode(code)
	}
	var nativeErr nativeError
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case nativeCancelled(err):
		return "cancelled"
	case errors.As(err, &nativeErr) && nativeErr.status == "partial":
		return "partial_coverage"
	case errors.Is(err, ErrNativeContract):
		return "native_contract"
	case errors.Is(err, semanticprobe.ErrPermissionDenied):
		return "scope_denied"
	case errors.Is(err, semanticprobe.ErrExpiredReference):
		return "reference_expired"
	case errors.Is(err, semanticprobe.ErrStaleReference):
		return "reference_stale"
	case errors.As(err, &nativeErr) && nativeErr.code != "":
		return mapNativeCode(nativeErr.code)
	case errors.Is(err, ErrStaleWindow):
		return "stale_window"
	case errors.Is(err, ErrInvalidArgs):
		return "invalid_arguments"
	case errors.Is(err, ErrResultLimit):
		return "result_limit"
	default:
		return "backend_unavailable"
	}
}

func isCancelledError(err error) bool {
	return errors.Is(err, context.Canceled) || nativeCancelled(err)
}

func mapNativeCode(code string) string {
	switch code {
	case "cancelled":
		return "cancelled"
	case "permission_denied", "scope_or_permission_denied", "scope_mismatch":
		return "scope_denied"
	case "reference_expired":
		return "reference_expired"
	case "reference_stale":
		return "reference_stale"
	case "reference_unavailable", "fixture_window_unavailable", "windows_unavailable":
		return "reference_unavailable"
	case "request_limit_exceeded":
		return "request_limit"
	case "response_limit_exceeded":
		return "result_limit"
	case "reference_limit_exceeded":
		return "reference_limit"
	case "invalid_request":
		return "invalid_request"
	case "scope_required":
		return "scope_denied"
	default:
		return "backend_unavailable"
	}
}

func normalizeStatus(status string) string {
	switch status {
	case "complete", "partial", "ambiguous", "cancelled":
		return status
	default:
		return "error"
	}
}

func decodeArgs(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgs, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidArgs
	}
	return nil
}

func objectSchema(properties map[string]any, required ...[]string) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": false}
	if properties != nil {
		schema["properties"] = properties
	}
	if len(required) > 0 && len(required[0]) > 0 {
		schema["required"] = required[0]
	}
	return schema
}

func safeToken(value string, max int) bool {
	return value != "" && len(value) <= max && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func validNonce(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, char := range []byte(value) {
		if !((char >= '0' && char <= '9') || (char >= 'A' && char <= 'Z') ||
			(char >= 'a' && char <= 'z') || char == '.' || char == '_' || char == '-') {
			return false
		}
	}
	return true
}
