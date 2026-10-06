//go:build darwin && cgo

package darwin

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/sirerun/comuse/internal/backend"
	"github.com/sirerun/comuse/internal/jsonwire"
)

var requestSequence atomic.Uint64

func (owner *runtimeOwner) start(command ownerCommand, pending map[uint64]pendingRequest) {
	// The owner map, not caller goroutine lifetime, is the native in-flight
	// authority. Callers can cancel while Swift still owes a terminal callback.
	if len(pending) >= maximumInflight {
		command.reply <- callResult{err: backendError("rate_limited")}
		return
	}
	callbackID := callbackSequence.Add(1)
	if callbackID == 0 {
		callbackID = callbackSequence.Add(1)
	}
	nativeID, err := owner.lib.start(owner.runtimeID, command.request, callbackID, owner.completions)
	if err != nil {
		command.reply <- callResult{err: err}
		return
	}
	pending[callbackID] = pendingRequest{nativeID: nativeID, ctx: command.ctx, reply: command.reply, request: command.request}
}

func (native *nativeBackend) invoke(ctx context.Context, operation, windowRef, elementRef, stateID string,
	budget *backend.Budget, action *backend.Action, output any) error {
	if ctx == nil {
		return backendError("invalid_request")
	}
	if native.closed.Load() || native.closing.Load() {
		return backendError("session_closed")
	}
	if native.inflight.Add(1) > maximumInflight {
		native.inflight.Add(-1)
		return backendError("desktop_busy")
	}
	defer native.inflight.Add(-1)
	requestID := formatRequestID(requestSequence.Add(1))
	request := nativeRequest{SchemaVersion: abiVersion, RequestID: requestID, Operation: operation,
		WindowRef: windowRef, ElementRef: elementRef, StateID: stateID, Budget: budget, Action: action}
	data, err := json.Marshal(request)
	if err != nil || len(data) == 0 || len(data) > maxNativeRequest {
		return backendError("invalid_request")
	}
	reply := make(chan callResult, 1)
	command := ownerCommand{request: data, ctx: ctx, reply: reply}
	select {
	case native.owner.commands <- command:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case result := <-reply:
		if result.err != nil {
			return result.err
		}
		if output == nil {
			return nil
		}
		if err := json.Unmarshal(result.value, output); err != nil {
			return backendError("backend_unavailable")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (native *nativeBackend) requestClose(ctx context.Context) error {
	if native.closed.Load() {
		return nil
	}
	if ctx == nil {
		return backendError("invalid_request")
	}
	native.closing.Store(true)
	reply := make(chan callResult, 1)
	select {
	case native.owner.commands <- ownerCommand{ctx: ctx, reply: reply, close: true}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case result := <-reply:
		return result.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (native *nativeBackend) Doctor(ctx context.Context) (backend.Doctor, error) {
	var result backend.Doctor
	if err := native.invoke(ctx, "doctor", "", "", "", nil, nil, &result); err != nil {
		return backend.Doctor{}, err
	}
	result.Capabilities.Input = false
	result.Capabilities.QualifiedInput = false
	result.Capabilities.ScreenCapture = false
	return result, nil
}

func (native *nativeBackend) Windows(ctx context.Context, budget backend.Budget) ([]backend.Window, error) {
	var result []backend.Window
	if err := native.invoke(ctx, "windows", "", "", "", &budget, nil, &result); err != nil {
		return nil, err
	}
	for _, window := range result {
		if !validOpaque(window.Ref) || !scopeContains(native.boundScope, window.Process) {
			return nil, backendError("backend_unavailable")
		}
	}
	return result, nil
}

func (native *nativeBackend) Observe(ctx context.Context, windowRef string, budget backend.Budget) (backend.Snapshot, error) {
	if !validOpaque(windowRef) {
		return backend.Snapshot{}, backendError("invalid_request")
	}
	var result backend.Snapshot
	if err := native.invoke(ctx, "observe", windowRef, "", "", &budget, nil, &result); err != nil {
		return backend.Snapshot{}, err
	}
	if result.WindowRef != windowRef || !validOpaque(result.StateID) {
		return backend.Snapshot{}, backendError("backend_unavailable")
	}
	for i := range result.Elements {
		element := &result.Elements[i]
		if !validOpaque(element.Ref) || element.Order < 0 || element.Role == "" {
			return backend.Snapshot{}, backendError("backend_unavailable")
		}
		if element.Classification != "normal" {
			element.Value = nil
			element.Actions = nil
		}
		if element.Value != nil && (!native.allowValues || element.Classification != "normal" || len(*element.Value) > maximumTextBytes) {
			return backend.Snapshot{}, backendError("backend_unavailable")
		}
		if element.ParentRef != "" && !validOpaque(element.ParentRef) {
			return backend.Snapshot{}, backendError("backend_unavailable")
		}
	}
	return result, nil
}

func (native *nativeBackend) ReadElement(ctx context.Context, windowRef, elementRef, stateID string,
	budget backend.Budget) (backend.ElementContent, error) {
	if !validOpaque(windowRef) || !validOpaque(elementRef) || !validOpaque(stateID) {
		return backend.ElementContent{}, backendError("invalid_request")
	}
	var result backend.ElementContent
	if err := native.invoke(ctx, "read_element", windowRef, elementRef, stateID, &budget, nil, &result); err != nil {
		return backend.ElementContent{}, err
	}
	if result.WindowRef != windowRef || result.ElementRef != elementRef || result.StateID != stateID || len(result.Text) > maximumTextBytes {
		return backend.ElementContent{}, backendError("backend_unavailable")
	}
	return result, nil
}

func (native *nativeBackend) Execute(ctx context.Context, action backend.Action) (backend.ActionResult, error) {
	if action.ID == "" || !validOpaque(action.ID) || !validOpaque(action.WindowRef) ||
		!validOpaque(action.ElementRef) || !validOpaque(action.StateID) || len(action.Text) > maximumTextBytes {
		return backend.ActionResult{}, backendError("invalid_request")
	}
	// Native mutation remains source-only and compile-closed until separately qualified.
	return backend.ActionResult{}, backendError("unsupported")
}

func (native *nativeBackend) Close(ctx context.Context) error { return native.requestClose(ctx) }

func validateEnvelope(data []byte, expectedID string) (json.RawMessage, error) {
	if len(data) == 0 || len(data) > maximumResponse {
		return nil, backendError("backend_unavailable")
	}
	var envelope nativeEnvelope
	if err := jsonwire.Decode(strings.NewReader(string(data)), maximumResponse, &envelope); err != nil ||
		envelope.SchemaVersion != abiVersion || envelope.RequestID != expectedID {
		return nil, backendError("backend_unavailable")
	}
	switch envelope.Status {
	case "ok":
		if len(envelope.Result) == 0 || string(envelope.Result) == "null" || envelope.Error != nil {
			return nil, backendError("backend_unavailable")
		}
		return envelope.Result, nil
	case "error":
		if envelope.Error == nil || len(envelope.Result) != 0 {
			return nil, backendError("backend_unavailable")
		}
		return nil, safeNativeError(*envelope.Error)
	default:
		return nil, backendError("backend_unavailable")
	}
}

func safeNativeError(code string) error {
	switch code {
	case "invalid_request", "policy_refused", "approval_required", "element_stale", "state_expired",
		"permission_denied", "unsupported", "backend_unavailable", "desktop_busy", "rate_limited",
		"budget_exceeded", "cancelled", "session_closed", "unknown_outcome", "internal_error":
		return backendError(code)
	default:
		return backendError("internal_error")
	}
}

func backendError(code string) error {
	messages := map[string]string{
		"invalid_request": "The native request is invalid.", "policy_refused": "The target is outside the configured scope.",
		"approval_required": "The action requires host approval.", "element_stale": "The target identity is stale.",
		"state_expired": "The observed state is stale or expired.", "permission_denied": "Required macOS permission is unavailable.",
		"unsupported": "The requested operation is not supported.", "backend_unavailable": "The native backend is unavailable.",
		"desktop_busy": "The native desktop is busy.", "rate_limited": "The native request rate limit was reached.",
		"budget_exceeded": "The bounded native request limit was reached.", "cancelled": "The operation was cancelled.",
		"session_closed": "The native session is closed.", "unknown_outcome": "The operation outcome is unknown.",
		"internal_error": "The native backend failed.",
	}
	message, ok := messages[code]
	if !ok {
		code, message = "internal_error", messages["internal_error"]
	}
	return &backend.Error{Code: code, Message: message}
}

func formatRequestID(id uint64) string {
	if id == 0 {
		id = requestSequence.Add(1)
	}
	return strconv.FormatUint(id, 16)
}

func requestIDFromBytes(data []byte) string {
	var request struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return ""
	}
	return request.RequestID
}

var _ backend.Backend = (*nativeBackend)(nil)
