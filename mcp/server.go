package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirerun/comuse"
)

const (
	serverName            = "comuse"
	serverVersion         = "0.1.0-dev"
	maxArgumentBytes      = 16 * 1024
	maxFrameBytes         = 64 * 1024
	maxOutboundFrameBytes = 16 * 1024 * 1024
	ledgerResourceURI     = "comuse://session/ledger"
)

var errOutboundFrameTooLarge = errors.New("outbound MCP frame exceeds configured maximum")

type route struct {
	operation   comuse.Operation
	request     string
	description string
}

var readonlyRoutes = []route{
	{comuse.OperationState, "EmptyRequest", "Read current scoped session and permission state."},
	{comuse.OperationWindows, "EmptyRequest", "List scoped windows in the current session."},
	{comuse.OperationObserve, "ObserveRequest", "Read a bounded semantic snapshot for a scoped window."},
	{comuse.OperationReadElement, "ReadElementRequest", "Read text from a scoped element using its current semantic state identifier."},
	{comuse.OperationWait, "WaitRequest", "Wait for a bounded semantic observation update or condition."},
}

var semanticRoutes = []route{
	{comuse.OperationClickElement, "ClickElementRequest", "Press a currently observed semantic element."},
	{comuse.OperationElementAction, "ElementActionRequest", "Perform one qualified semantic element action."},
	{comuse.OperationWriteElement, "WriteElementRequest", "Replace or insert text in a currently observed semantic element."},
	{comuse.OperationScrollElement, "ScrollElementRequest", "Scroll one qualified semantic element by one bounded unit."},
}

var knownSemanticNames = map[string]comuse.Operation{
	"computer_click_element":  comuse.OperationClickElement,
	"computer_element_action": comuse.OperationElementAction,
	"computer_write_element":  comuse.OperationWriteElement,
	"computer_scroll_element": comuse.OperationScrollElement,
}

// NewServer returns the default read-only MCP surface and performs no
// capability probe. Host mutation approval and policy remain in Session.
func NewServer(session *comuse.Session) *sdk.Server {
	return newServer(session, readonlyRoutes)
}

// NewServerForHost adds only semantic action routes qualified by the trusted
// host session. It never exposes developer coordinate or keyboard routes.
func NewServerForHost(ctx context.Context, session *comuse.Session) (*sdk.Server, error) {
	if ctx == nil || session == nil {
		return nil, errors.New("invalid MCP host session")
	}
	qualified, err := session.SemanticOperations(ctx)
	if err != nil {
		return nil, err
	}
	allowed := make(map[comuse.Operation]bool, len(qualified))
	for _, operation := range qualified {
		allowed[operation] = true
	}
	routes := append([]route(nil), readonlyRoutes...)
	for _, candidate := range semanticRoutes {
		if allowed[candidate.operation] {
			routes = append(routes, candidate)
		}
	}
	return newServer(session, routes), nil
}

func newServer(session *comuse.Session, routes []route) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: serverName, Version: serverVersion}, nil)
	for _, item := range routes {
		item := item
		name := toolName(item.operation)
		server.AddTool(&sdk.Tool{
			Name:         name,
			Description:  item.description,
			InputSchema:  inputSchema(item.request),
			OutputSchema: outputSchema(),
		}, routeHandler(session, item.operation))
	}
	server.AddResource(&sdk.Resource{
		URI:         ledgerResourceURI,
		Name:        "comuse_ledger",
		MIMEType:    "application/json",
		Description: "Read the cumulative ledger for this host-owned session.",
	}, ledgerResourceHandler(session))
	server.AddReceivingMiddleware(unsupportedSemanticMiddleware(session))
	return server
}

func ledgerResourceHandler(session *comuse.Session) sdk.ResourceHandler {
	return func(ctx context.Context, request *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		if request == nil || request.Params == nil || request.Params.URI != ledgerResourceURI {
			return nil, sdk.ResourceNotFoundError(ledgerResourceURI)
		}
		snapshot, err := session.Ledger(ctx)
		if err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: comuse.ErrorCode(err)}
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal_error"}
		}
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{
			URI: ledgerResourceURI, MIMEType: "application/json", Text: string(encoded),
		}}}, nil
	}
}

// Serve runs the official SDK over an owned bounded stdio-like stream.
func Serve(ctx context.Context, session *comuse.Session, stream io.ReadWriteCloser) error {
	if ctx == nil || session == nil || stream == nil {
		return errors.New("invalid MCP serve configuration")
	}
	closeOnce := &streamClose{stream: stream}
	transport := &sdk.IOTransport{
		Reader:        &ownedStreamReader{stream: stream, close: closeOnce},
		Writer:        &boundedFrameWriter{stream: stream, close: closeOnce, maxBytes: maxOutboundFrameBytes},
		MaxLineLength: maxFrameBytes,
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

type boundedFrameWriter struct {
	stream   io.Writer
	close    *streamClose
	maxBytes int
}

func (w *boundedFrameWriter) Write(p []byte) (int, error) {
	if len(p) > w.maxBytes {
		_ = w.close.closeStream()
		return 0, errOutboundFrameTooLarge
	}
	n, err := w.stream.Write(p)
	if err != nil || n != len(p) {
		_ = w.close.closeStream()
		if err == nil {
			err = io.ErrShortWrite
		}
	}
	return n, err
}
func (w *boundedFrameWriter) Close() error { return w.close.closeStream() }

func routeHandler(session *comuse.Session, operation comuse.Operation) sdk.ToolHandler {
	return func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if request == nil || request.Params == nil {
			return rejectionResult("invalid_request")
		}
		return dispatch(ctx, session, operation, request.Params.Arguments)
	}
}

func dispatch(ctx context.Context, session *comuse.Session, operation comuse.Operation, raw []byte) (*sdk.CallToolResult, error) {
	if len(raw) > maxArgumentBytes {
		return rejectionResult("invalid_request")
	}
	request, err := comuse.DecodeRequest(operation, raw)
	if err != nil {
		return rejectionResult("invalid_request")
	}
	if session == nil {
		return rejectionResult("session_closed")
	}
	envelope, _ := session.Call(ctx, request)
	return envelopeResult(envelope)
}

func rejectionResult(code string) (*sdk.CallToolResult, error) {
	envelope, err := comuse.RejectionEnvelope(code)
	if err != nil {
		return nil, fmt.Errorf("build MCP rejection envelope: %w", err)
	}
	return envelopeResult(envelope)
}

func envelopeResult(envelope comuse.ResultEnvelope) (*sdk.CallToolResult, error) {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode canonical MCP envelope: %w", err)
	}
	var structured any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&structured); err != nil {
		return nil, fmt.Errorf("decode canonical MCP envelope: %w", err)
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: string(encoded)}},
		StructuredContent: structured,
		IsError:           !envelope.OK,
	}, nil
}

func unsupportedSemanticMiddleware(session *comuse.Session) sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, request)
			}
			call, ok := request.(*sdk.CallToolRequest)
			if !ok || call.Params == nil {
				return next(ctx, method, request)
			}
			operation, known := knownSemanticNames[call.Params.Name]
			if !known {
				return next(ctx, method, request)
			}
			result, err := dispatch(ctx, session, operation, call.Params.Arguments)
			if err != nil {
				return nil, err
			}
			return result, nil
		}
	}
}

func toolName(operation comuse.Operation) string {
	if operation == comuse.OperationState {
		return "computer_state"
	}
	return "computer_" + string(operation)
}

func inputSchema(definition string) map[string]any {
	var contract map[string]any
	if err := json.Unmarshal(comuse.ResponseSchema(), &contract); err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	}
	defs, _ := contract["$defs"].(map[string]any)
	return map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "$defs": defs, "$ref": "#/$defs/" + definition, "type": "object"}
}

func outputSchema() map[string]any {
	var schema map[string]any
	if err := json.Unmarshal(comuse.ResponseSchema(), &schema); err != nil {
		return map[string]any{"type": "object"}
	}
	return schema
}
