// Package backend defines the bounded native semantic runtime contract.
package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Error carries a stable code and a host-safe message, never native payloads.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func ErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "budget_exceeded"
	}
	var e *Error
	if errors.As(err, &e) {
		switch e.Code {
		case "invalid_request", "policy_refused", "approval_required", "element_stale", "state_expired", "permission_denied", "unsupported", "backend_unavailable", "desktop_busy", "rate_limited", "budget_exceeded", "cancelled", "session_closed", "unknown_outcome", "replay_result_expired":
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
	// ActionKinds is trusted backend qualification evidence, never JSON authority.
	ActionKinds    []string `json:"-"`
	Accessibility  bool     `json:"accessibility"`
	Input          bool     `json:"input"`
	ScreenCapture  bool     `json:"screen_capture"`
	QualifiedInput bool     `json:"qualified_input"`
	Reasons        []string `json:"reasons,omitempty"`
}
type Doctor struct {
	Capabilities   Capabilities      `json:"capabilities"`
	Permissions    map[string]string `json:"permissions"`
	DesktopContext *DesktopContext   `json:"desktop_context,omitempty"`
}
type Window struct {
	Ref     string          `json:"ref"`
	Process ProcessIdentity `json:"process"`
	Title   string          `json:"title"`
}

// DesktopContext is inspected native evidence. It is never request authority.
// A non-nil value represents a complete inspection; FocusedWindow=nil means
// inspection found no scoped focused window. The wire decoder requires all
// three members so a missing focus field cannot be mistaken for an inspected
// null result.
type DesktopContext struct {
	DisplayID         string  `json:"display_id"`
	DisplayGeneration uint64  `json:"display_generation"`
	FocusedWindow     *Window `json:"focused_window"`
}

func (c *DesktopContext) UnmarshalJSON(data []byte) error {
	if c == nil {
		return errors.New("nil desktop context")
	}
	var fields map[string]json.RawMessage
	if err := uniqueJSONObjectMembers(data); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != 3 {
		return errors.New("invalid desktop context fields")
	}
	for _, name := range []string{"display_id", "display_generation", "focused_window"} {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("missing desktop context field %q", name)
		}
	}
	var decoded struct {
		DisplayID         string  `json:"display_id"`
		DisplayGeneration uint64  `json:"display_generation"`
		FocusedWindow     *Window `json:"focused_window"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if !validContextOpaque(decoded.DisplayID) || decoded.DisplayGeneration == 0 || decoded.DisplayGeneration > 9007199254740991 {
		return errors.New("invalid desktop context identity")
	}
	if decoded.FocusedWindow != nil && (!validContextOpaque(decoded.FocusedWindow.Ref) ||
		decoded.FocusedWindow.Process.PID <= 0 || decoded.FocusedWindow.Process.BundleID == "" ||
		decoded.FocusedWindow.Process.LaunchID == "" || len(decoded.FocusedWindow.Title) > 4096) {
		return errors.New("invalid focused window evidence")
	}
	*c = DesktopContext{DisplayID: decoded.DisplayID, DisplayGeneration: decoded.DisplayGeneration, FocusedWindow: cloneWindowPointer(decoded.FocusedWindow)}
	return nil
}

func uniqueJSONObjectMembers(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("desktop context must be an object")
	}
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return errors.New("invalid desktop context object")
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("invalid desktop context key")
		}
		if _, exists := seen[name]; exists {
			return errors.New("duplicate desktop context field")
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return errors.New("invalid desktop context value")
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return errors.New("unterminated desktop context object")
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing desktop context data")
	}
	return nil
}

func validContextOpaque(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '!' || value[i] > '~' {
			return false
		}
	}
	return true
}

func cloneWindowPointer(window *Window) *Window {
	if window == nil {
		return nil
	}
	copy := *window
	return &copy
}

type Element struct {
	Ref            string   `json:"ref"`
	ParentRef      string   `json:"parent_ref,omitempty"`
	Order          int      `json:"order"`
	Role           string   `json:"role"`
	Label          string   `json:"label,omitempty"`
	Value          *string  `json:"value,omitempty"`
	Enabled        *bool    `json:"enabled,omitempty"`
	Focused        *bool    `json:"focused,omitempty"`
	Checked        *bool    `json:"checked,omitempty"`
	Selected       *bool    `json:"selected,omitempty"`
	Actions        []string `json:"actions,omitempty"`
	Classification string   `json:"classification"`
}
type Coverage struct {
	Complete bool   `json:"complete"`
	Reason   string `json:"reason,omitempty"`
}
type Snapshot struct {
	ScopeID        string          `json:"-"`
	ActionSequence uint64          `json:"-"`
	WindowRef      string          `json:"window_ref"`
	StateID        string          `json:"state_id"`
	ObservedAt     time.Time       `json:"observed_at"`
	Elements       []Element       `json:"elements"`
	Coverage       Coverage        `json:"coverage"`
	DesktopContext *DesktopContext `json:"desktop_context,omitempty"`
}
type ElementContent struct {
	ObservedAt time.Time `json:"-"`
	WindowRef  string    `json:"window_ref"`
	ElementRef string    `json:"element_ref"`
	StateID    string    `json:"state_id"`
	Text       string    `json:"text"`
}
type Action struct {
	ID         string  `json:"id"`
	WindowRef  string  `json:"window_ref"`
	ElementRef string  `json:"element_ref"`
	StateID    string  `json:"state_id"`
	Kind       string  `json:"kind"`
	Text       string  `json:"text,omitempty"`
	Direction  string  `json:"direction,omitempty"`
	Amount     string  `json:"amount,omitempty"`
	X          float64 `json:"x,omitempty"`
	Y          float64 `json:"y,omitempty"`
	EndX       float64 `json:"end_x,omitempty"`
	EndY       float64 `json:"end_y,omitempty"`
	Button     string  `json:"button,omitempty"`
	Count      int     `json:"count,omitempty"`
	HoldMS     int     `json:"hold_ms,omitempty"`
	DelayMS    int     `json:"delay_ms,omitempty"`
	Keys       string  `json:"keys,omitempty"`
	DX         int     `json:"dx,omitempty"`
	DY         int     `json:"dy,omitempty"`
	Steps      int     `json:"steps,omitempty"`
	DurationMS int     `json:"duration_ms,omitempty"`
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
	ActionScroll                                 = "scroll"
	ActionPick                                   = "pick"
	ActionFocus                                  = "focus"
	ActionClick                                  = "click"
	ActionTypeText                               = "type_text"
	ActionPressKey                               = "press_key"
	ActionCoordinateScroll                       = "coordinate_scroll"
	ActionDrag                                   = "drag"
	ActionFocusWindow                            = "focus_window"
)

type Verification struct {
	Status VerificationStatus `json:"status"`
	Reason string             `json:"reason,omitempty"`
}
type ActionResult struct {
	ActionID string `json:"action_id"`
	// Dispatch metadata is projected separately into the canonical envelope.
	Method         string          `json:"-"`
	CompletedSteps []string        `json:"-"`
	Execution      ExecutionStatus `json:"execution"`
	Verification   Verification    `json:"verification"`
	StateStatus    StateStatus     `json:"state_status"`
	Cleanup        CleanupStatus   `json:"cleanup"`
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
