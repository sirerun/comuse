package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirerun/comuse"
	"github.com/sirerun/comuse/internal/jsonwire"
)

const (
	serverName       = "comuse"
	serverVersion    = "0.1.0-dev"
	maxArgumentBytes = 16 * 1024
	maxWait          = 30 * time.Second
)

// NewServer constructs the read-only semantic MCP server for one host-owned
// session. Approval and policy remain inside the trusted host session.
func NewServer(session *comuse.Session) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, nil)
	server.AddTool(tool("computer_state", "Read current scoped session and permission state.", nil, nil), handler(session, stateTool))
	server.AddTool(tool("computer_windows", "List scoped windows in the current session.", nil, nil), handler(session, windowsTool))
	server.AddTool(tool("computer_a11y", "Read a bounded semantic snapshot for a scoped window.", map[string]any{
		"window_ref": stringProperty(128),
	}, []string{"window_ref"}), handler(session, a11yTool))
	server.AddTool(tool("computer_read_element", "Read text from a scoped element using its current semantic state identifier.", map[string]any{
		"window_ref":  stringProperty(128),
		"element_ref": stringProperty(128),
		"state_id":    stringProperty(128),
	}, []string{"window_ref", "element_ref", "state_id"}), handler(session, readElementTool))
	server.AddTool(tool("computer_wait", "Wait for a bounded semantic observation update for a scoped window.", map[string]any{
		"window_ref": stringProperty(128),
		"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": int(maxWait / time.Millisecond)},
	}, []string{"window_ref", "timeout_ms"}), handler(session, waitTool))
	return server
}

// Serve runs a fresh official SDK server over the supplied stdio-like stream.
func Serve(ctx context.Context, session *comuse.Session, stream io.ReadWriteCloser) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	if session == nil {
		return errors.New("nil session")
	}
	if stream == nil {
		return errors.New("nil MCP stream")
	}
	closeOnce := &streamClose{stream: stream}
	transport := &mcp.IOTransport{
		Reader: &ownedStreamReader{stream: stream, close: closeOnce},
		Writer: ownedStreamWriter{stream: stream},
	}
	return NewServer(session).Run(ctx, transport)
}

type streamClose struct {
	stream io.Closer
	once   sync.Once
	err    error
}

func (c *streamClose) closeStream() error {
	c.once.Do(func() { c.err = c.stream.Close() })
	return c.err
}

type ownedStreamReader struct {
	stream io.ReadWriteCloser
	close  *streamClose
}

func (r *ownedStreamReader) Read(p []byte) (int, error) { return r.stream.Read(p) }
func (r *ownedStreamReader) Close() error               { return r.close.closeStream() }

type ownedStreamWriter struct{ stream io.ReadWriteCloser }

func (w ownedStreamWriter) Write(p []byte) (int, error) { return w.stream.Write(p) }
func (ownedStreamWriter) Close() error                  { return nil }

type toolHandler func(context.Context, *comuse.Session, json.RawMessage) (any, error)

func handler(session *comuse.Session, run toolHandler) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if request == nil || request.Params == nil {
			return textResult(envelopeError("invalid_request")), nil
		}
		if session == nil {
			return textResult(envelopeError("session_closed")), nil
		}
		result, err := run(ctx, session, request.Params.Arguments)
		if err != nil {
			return textResult(envelopeError(errorCode(ctx, err))), nil
		}
		return textResult(comuse.Envelope{SchemaVersion: comuse.SchemaVersion, Status: "ok", Result: result}), nil
	}
}

func stateTool(ctx context.Context, session *comuse.Session, raw json.RawMessage) (any, error) {
	var args emptyArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, errInvalidRequest
	}
	return session.State(ctx)
}

func windowsTool(ctx context.Context, session *comuse.Session, raw json.RawMessage) (any, error) {
	var args emptyArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, errInvalidRequest
	}
	return session.Windows(ctx)
}

func a11yTool(ctx context.Context, session *comuse.Session, raw json.RawMessage) (any, error) {
	var args windowArgs
	if err := decodeArgs(raw, &args); err != nil || args.WindowRef == "" {
		return nil, errInvalidRequest
	}
	return session.Observe(ctx, args.WindowRef)
}

func readElementTool(ctx context.Context, session *comuse.Session, raw json.RawMessage) (any, error) {
	var args readElementArgs
	if err := decodeArgs(raw, &args); err != nil || args.WindowRef == "" || args.ElementRef == "" || args.StateID == "" {
		return nil, errInvalidRequest
	}
	return session.ReadElement(ctx, args.WindowRef, args.ElementRef, args.StateID)
}

func waitTool(ctx context.Context, session *comuse.Session, raw json.RawMessage) (any, error) {
	var args waitArgs
	if err := decodeArgs(raw, &args); err != nil || args.WindowRef == "" || args.TimeoutMS < 1 || args.TimeoutMS > int64(maxWait/time.Millisecond) {
		return nil, errInvalidRequest
	}
	return session.Wait(ctx, args.WindowRef, time.Duration(args.TimeoutMS)*time.Millisecond)
}

type emptyArgs struct{}
type windowArgs struct {
	WindowRef string `json:"window_ref"`
}
type readElementArgs struct {
	WindowRef  string `json:"window_ref"`
	ElementRef string `json:"element_ref"`
	StateID    string `json:"state_id"`
}
type waitArgs struct {
	WindowRef string `json:"window_ref"`
	TimeoutMS int64  `json:"timeout_ms"`
}

var errInvalidRequest = errors.New("invalid_request")

func errorCode(ctx context.Context, err error) string {
	if errors.Is(err, errInvalidRequest) {
		return "invalid_request"
	}
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled"
	}
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "budget_exceeded"
	}
	return comuse.ErrorCode(err)
}

func decodeArgs(raw json.RawMessage, dst any) error {
	_, empty := dst.(*emptyArgs)
	if len(raw) == 0 {
		if empty {
			return nil
		}
		return errInvalidRequest
	}
	if empty && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	if len(raw) > maxArgumentBytes {
		return errInvalidRequest
	}
	if err := jsonwire.Decode(bytes.NewReader(raw), maxArgumentBytes, dst); err != nil {
		return errInvalidRequest
	}
	return nil
}

func tool(name, description string, properties map[string]any, required []string) *mcp.Tool {
	if properties == nil {
		properties = map[string]any{}
	}
	if required == nil {
		required = []string{}
	}
	return &mcp.Tool{
		Name:        name,
		Description: description,
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": false,
		},
		OutputSchema: sourceEnvelopeSchema(),
	}
}

// sourceEnvelopeSchema describes the current shared source-phase envelope.
// It intentionally does not claim the RFC release envelope's usage/duration
// or compact-state fields, which remain unqualified.
func sourceEnvelopeSchema() map[string]any {
	versionSchema := map[string]any{"type": "integer", "enum": []int{comuse.SchemaVersion}}
	errorSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code":    map[string]any{"type": "string", "enum": errorCodeVocabulary()},
			"message": map[string]any{"type": "string"},
		},
		"required":             []string{"code", "message"},
		"additionalProperties": false,
	}
	resultSchema := map[string]any{}
	success := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"schema_version": versionSchema,
			"status":         map[string]any{"type": "string", "enum": []string{"ok"}},
			"result":         resultSchema,
		},
		"required":             []string{"schema_version", "status", "result"},
		"additionalProperties": false,
	}
	failure := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"schema_version": versionSchema,
			"status":         map[string]any{"type": "string", "enum": []string{"error"}},
			"error":          errorSchema,
		},
		"required":             []string{"schema_version", "status", "error"},
		"additionalProperties": false,
	}
	return map[string]any{"type": "object", "oneOf": []any{success, failure}}
}

func errorCodeVocabulary() []string {
	return []string{
		"invalid_request", "policy_refused", "approval_required", "element_stale", "state_expired",
		"permission_denied", "unsupported", "backend_unavailable", "desktop_busy", "rate_limited",
		"budget_exceeded", "cancelled", "session_closed", "unknown_outcome", "internal_error",
	}
}

func stringProperty(maxLength int) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": maxLength}
}

func envelopeError(code string) comuse.Envelope {
	if _, ok := publicErrorCodes[code]; !ok {
		code = "internal_error"
	}
	return comuse.Envelope{
		SchemaVersion: comuse.SchemaVersion,
		Status:        "error",
		Error:         &comuse.Error{Code: code, Message: safeMessage(code)},
	}
}

var publicErrorCodes = map[string]struct{}{
	"invalid_request": {}, "policy_refused": {}, "approval_required": {}, "element_stale": {},
	"state_expired": {}, "permission_denied": {}, "unsupported": {}, "backend_unavailable": {},
	"desktop_busy": {}, "rate_limited": {}, "budget_exceeded": {}, "cancelled": {},
	"session_closed": {}, "unknown_outcome": {}, "internal_error": {},
}

func safeMessage(code string) string {
	messages := map[string]string{
		"invalid_request": "The request is invalid.", "policy_refused": "The request was refused by policy.",
		"approval_required": "Trusted host approval is required.", "element_stale": "The element reference is stale.",
		"state_expired": "The semantic state has expired.", "permission_denied": "Required permission is unavailable.",
		"unsupported": "The operation is unsupported.", "backend_unavailable": "The backend is unavailable.",
		"desktop_busy": "The desktop is busy.", "rate_limited": "The request rate limit was reached.",
		"budget_exceeded": "The operation exceeded its budget.", "cancelled": "The operation was cancelled.",
		"session_closed": "The session is closed.", "unknown_outcome": "The operation outcome is unknown.",
		"internal_error": "The operation failed.",
	}
	if message, ok := messages[code]; ok {
		return message
	}
	return "The operation failed."
}

func textResult(envelope comuse.Envelope) *mcp.CallToolResult {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		encoded = []byte(`{"schema_version":1,"status":"error","error":{"code":"internal_error","message":"The operation failed."}}`)
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		StructuredContent: envelope,
		IsError:           envelope.Status == "error",
	}
}
