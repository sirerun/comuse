package comuse

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sirerun/comuse/internal/writer"
)

const (
	maxOpaqueRefBytes  = 128
	maxActionTextBytes = 8192
	maxApprovalAge     = 2 * time.Minute
)

var safeMessages = map[string]string{
	"invalid_request":     "The request is invalid.",
	"policy_refused":      "The request is not allowed by policy.",
	"approval_required":   "Host approval is required.",
	"element_stale":       "The element reference is stale.",
	"state_expired":       "The observed state has expired.",
	"permission_denied":   "Required permission was denied.",
	"unsupported":         "The operation is not supported.",
	"backend_unavailable": "The backend is unavailable.",
	"desktop_busy":        "The desktop writer is busy.",
	"rate_limited":        "The session action limit was reached.",
	"budget_exceeded":     "The request exceeds its configured budget.",
	"cancelled":           "The request was cancelled.",
	"session_closed":      "The session is closed.",
	"unknown_outcome":     "The action outcome is unknown.",
	"internal_error":      "The operation failed.",
}

func coreError(code string) error {
	message, ok := safeMessages[code]
	if !ok {
		code = "internal_error"
		message = safeMessages[code]
	}
	return &Error{Code: code, Message: message}
}

func opaqueASCII(value string) bool {
	if value == "" || len(value) > maxOpaqueRefBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func validateAction(action Action) error {
	if !opaqueASCII(action.ID) || !opaqueASCII(action.WindowRef) || !opaqueASCII(action.ElementRef) || !opaqueASCII(action.StateID) {
		return coreError("invalid_request")
	}
	if !utf8.ValidString(action.Text) || len(action.Text) > maxActionTextBytes {
		return coreError("invalid_request")
	}
	if action.Kind != ActionScroll && (action.Direction != "" || action.Amount != "") {
		return coreError("invalid_request")
	}
	switch action.Kind {
	case ActionPress, ActionPick, ActionFocus:
		if action.Text != "" {
			return coreError("invalid_request")
		}
	case ActionReplace:
		// An empty replacement deliberately clears the scoped field.
	case ActionInsert:
		if action.Text == "" {
			return coreError("invalid_request")
		}
	case ActionScroll:
		if action.Text != "" {
			return coreError("invalid_request")
		}
		switch action.Direction {
		case "up", "down", "left", "right":
		default:
			return coreError("invalid_request")
		}
		if action.Amount != "line" && action.Amount != "page" {
			return coreError("invalid_request")
		}
	default:
		return coreError("unsupported")
	}
	return nil
}

func validateApprovalBinding(request ApprovalRequest, approval Approval, now time.Time) error {
	if request.SessionID == "" || approval.SessionID != request.SessionID ||
		approval.Action != request.Action || approval.Process != request.Process ||
		!approval.ObservedAt.Equal(request.ObservedAt) || approval.PolicyVersion != request.PolicyVersion ||
		!approval.ExpiresAt.Equal(request.ExpiresAt) {
		return coreError("policy_refused")
	}
	if now.IsZero() || !approval.ExpiresAt.After(now) || approval.ExpiresAt.Sub(now) > maxApprovalAge {
		return coreError("approval_required")
	}
	return nil
}

func sanitizedBackendError(err error) error {
	if err == nil {
		return nil
	}
	var backendErr *Error
	if errors.As(err, &backendErr) {
		if _, ok := safeMessages[backendErr.Code]; ok {
			return coreError(backendErr.Code)
		}
		return coreError("internal_error")
	}
	return coreError("backend_unavailable")
}

func validateScope(scope Scope, now time.Time) error {
	if len(scope.Processes) == 0 || len(scope.Processes) > 256 || scope.ExpiresAt.IsZero() || !scope.ExpiresAt.After(now) {
		return coreError("invalid_request")
	}
	seen := make(map[ProcessIdentity]struct{}, len(scope.Processes))
	for _, process := range scope.Processes {
		if process.PID <= 0 || !safeBundleID(process.BundleID) || !opaqueASCII(process.LaunchID) {
			return coreError("invalid_request")
		}
		if _, exists := seen[process]; exists {
			return coreError("invalid_request")
		}
		seen[process] = struct{}{}
	}
	return nil
}

func safeBundleID(value string) bool {
	if value == "" || len(value) > 255 || strings.TrimSpace(value) != value {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func scopeContains(scope Scope, process ProcessIdentity) bool {
	for _, candidate := range scope.Processes {
		if candidate == process {
			return true
		}
	}
	return false
}

func validateBudget(budget Budget) error {
	if budget.MaxDepth < 1 || budget.MaxDepth > 128 || budget.MaxNodes < 1 || budget.MaxNodes > 10000 ||
		budget.MaxBytes < 1 || budget.MaxBytes > 4*1024*1024 || budget.Timeout <= 0 || budget.Timeout > 30*time.Second {
		return coreError("invalid_request")
	}
	return nil
}

func stableCallError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return coreError("cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return coreError("budget_exceeded")
	}
	return sanitizedBackendError(err)
}

func actionMetadata(action Action, result ActionResult, code string) writer.SafeActionMetadata {
	execution := string(writer.OutcomeUnknown)
	switch result.Execution {
	case ExecutionApplied:
		execution = string(writer.OutcomeApplied)
	case ExecutionNotApplied:
		execution = string(writer.OutcomeNotApplied)
	case ExecutionPartiallyApplied:
		execution = string(writer.OutcomePartial)
	}
	cleanup := "unknown"
	switch result.Cleanup {
	case CleanupComplete:
		cleanup = "released"
	case CleanupDirty:
		cleanup = "failed"
	}
	metadata := writer.SafeActionMetadata{
		Action:             action.Kind,
		Method:             result.Method,
		CompletedSteps:     strings.Join(result.CompletedSteps, ","),
		VerificationReason: safeVerificationReason(result.Verification.Reason),
		Execution:          execution,
		Verification:       string(result.Verification.Status),
		StateStatus:        string(result.StateStatus),
		Cleanup:            cleanup,
		ErrorCode:          code,
	}
	return metadata
}

func validResult(actionID string, result ActionResult) bool {
	if !safeDispatchMetadata(result) {
		return false
	}
	if result.ActionID != actionID || result.ActionID == "" {
		return false
	}
	switch result.Execution {
	case ExecutionNotApplied, ExecutionApplied, ExecutionPartiallyApplied, ExecutionUnknown:
	default:
		return false
	}
	switch result.Verification.Status {
	case VerificationVerified, VerificationFailed, VerificationUnavailable:
	default:
		return false
	}
	switch result.StateStatus {
	case StateAvailable, StateUnavailable:
	default:
		return false
	}
	switch result.Cleanup {
	case CleanupComplete, CleanupDirty, CleanupUnknown:
	default:
		return false
	}
	return true
}

func safeVerificationReason(reason string) string {
	switch reason {
	case "postcondition_met", "postcondition_failed", "state_changed", "target_missing", "state_unavailable", "verification_unavailable":
		return reason
	default:
		return ""
	}
}

func safeDispatchMetadata(r ActionResult) bool {
	switch r.Method {
	case "", "ax_press", "ax_pick", "ax_focus", "ax_set_value", "ax_scroll", "ax_focus_window", "cg_click", "cg_unicode", "cg_key", "cg_scroll", "cg_drag":
	default:
		return false
	}
	if len(r.CompletedSteps) > 128 {
		return false
	}
	for _, p := range r.CompletedSteps {
		switch p {
		case "focus", "press", "pick", "set_value", "unicode", "key_down", "key_up", "mouse_down", "mouse_up", "mouse_move", "scroll", "cleanup":
		default:
			return false
		}
	}
	return true
}
