// Package backend defines the bounded native semantic runtime contract.
package backend

import (
	"context"
	"errors"
	"time"
)

// Error carries a stable code and a host-safe message, never native payloads.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func ErrorCode(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	var e *Error
	if errors.As(err, &e) {
		switch e.Code {
		case "invalid_request", "policy_refused", "approval_required", "element_stale", "state_expired", "permission_denied", "unsupported", "backend_unavailable", "desktop_busy", "rate_limited", "budget_exceeded", "cancelled", "session_closed", "unknown_outcome":
			return e.Code
		}
	}
	return "internal_error"
}

type ProcessIdentity struct {
	PID      int32  `json:"pid"`
	BundleID string `json:"bundle_id"`
	LaunchID string `json:"launch_id"`
}
type Scope struct {
	Processes []ProcessIdentity `json:"processes"`
	ExpiresAt time.Time         `json:"expires_at"`
}
type Budget struct {
	MaxDepth int           `json:"max_depth"`
	MaxNodes int           `json:"max_nodes"`
	MaxBytes int           `json:"max_bytes"`
	Timeout  time.Duration `json:"timeout"`
}
type Config struct {
	AllowValues bool   `json:"allow_values"`
	Scope       Scope  `json:"scope"`
	LibraryPath string `json:"library_path"`
}
type Capabilities struct {
	Accessibility  bool     `json:"accessibility"`
	Input          bool     `json:"input"`
	ScreenCapture  bool     `json:"screen_capture"`
	QualifiedInput bool     `json:"qualified_input"`
	Reasons        []string `json:"reasons,omitempty"`
}
type Doctor struct {
	Capabilities Capabilities      `json:"capabilities"`
	Permissions  map[string]string `json:"permissions"`
}
type Window struct {
	Ref     string          `json:"ref"`
	Process ProcessIdentity `json:"process"`
	Title   string          `json:"title"`
}
type Element struct {
	Ref            string   `json:"ref"`
	ParentRef      string   `json:"parent_ref,omitempty"`
	Order          int      `json:"order"`
	Role           string   `json:"role"`
	Label          string   `json:"label,omitempty"`
	Value          *string  `json:"value,omitempty"`
	Enabled        *bool    `json:"enabled,omitempty"`
	Actions        []string `json:"actions,omitempty"`
	Classification string   `json:"classification"`
}
type Coverage struct {
	Complete bool   `json:"complete"`
	Reason   string `json:"reason,omitempty"`
}
type Snapshot struct {
	WindowRef  string    `json:"window_ref"`
	StateID    string    `json:"state_id"`
	ObservedAt time.Time `json:"observed_at"`
	Elements   []Element `json:"elements"`
	Coverage   Coverage  `json:"coverage"`
}
type ElementContent struct {
	WindowRef  string `json:"window_ref"`
	ElementRef string `json:"element_ref"`
	StateID    string `json:"state_id"`
	Text       string `json:"text"`
}
type Action struct {
	ID         string `json:"id"`
	WindowRef  string `json:"window_ref"`
	ElementRef string `json:"element_ref"`
	StateID    string `json:"state_id"`
	Kind       string `json:"kind"`
	Text       string `json:"text,omitempty"`
}

// Closed result vocabulary follows RFC 0001 section 5.3.
type ExecutionStatus string
type VerificationStatus string
type StateStatus string
type CleanupStatus string

const (
	ExecutionNotApplied       ExecutionStatus    = "not_applied"
	ExecutionApplied          ExecutionStatus    = "applied"
	ExecutionPartiallyApplied ExecutionStatus    = "partially_applied"
	ExecutionUnknown          ExecutionStatus    = "unknown"
	VerificationVerified      VerificationStatus = "verified"
	VerificationFailed        VerificationStatus = "failed"
	VerificationUnavailable   VerificationStatus = "unavailable"
	StateAvailable            StateStatus        = "available"
	StateUnavailable          StateStatus        = "unavailable"
	CleanupComplete           CleanupStatus      = "complete"
	CleanupDirty              CleanupStatus      = "dirty"
	CleanupUnknown            CleanupStatus      = "unknown"
	ActionPress                                  = "press"
	ActionReplace                                = "replace"
	ActionInsert                                 = "insert"
)

type Verification struct {
	Status VerificationStatus `json:"status"`
	Reason string             `json:"reason,omitempty"`
}
type ActionResult struct {
	ActionID     string          `json:"action_id"`
	Execution    ExecutionStatus `json:"execution"`
	Verification Verification    `json:"verification"`
	StateStatus  StateStatus     `json:"state_status"`
	Cleanup      CleanupStatus   `json:"cleanup"`
}

// Backend must freshly validate scope and references at every native operation.
// Execute is reachable only after host admission and qualified input capability.
// Close errors retain runtime ownership so the owner can retry safe drain/close.
type Backend interface {
	Doctor(context.Context) (Doctor, error)
	Windows(context.Context, Budget) ([]Window, error)
	Observe(context.Context, string, Budget) (Snapshot, error)
	ReadElement(context.Context, string, string, string, Budget) (ElementContent, error)
	Execute(context.Context, Action) (ActionResult, error)
	Close(context.Context) error
}
