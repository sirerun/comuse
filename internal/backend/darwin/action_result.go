package darwin

import (
	"math"
	"strings"

	"github.com/sirerun/comuse/internal/backend"
)

const nativeTextByteLimit = 8192

type nativeActionResult struct {
	ActionID       string                  `json:"action_id"`
	Execution      backend.ExecutionStatus `json:"execution"`
	Verification   backend.Verification    `json:"verification"`
	StateStatus    backend.StateStatus     `json:"state_status"`
	Cleanup        backend.CleanupStatus   `json:"cleanup"`
	Method         string                  `json:"method"`
	CompletedSteps []string                `json:"completed_steps"`
}

func decodeActionResult(wire nativeActionResult, action backend.Action) (backend.ActionResult, error) {
	expected := expectedNativeActionResult(action)
	expectedMethod, expectedStep := expected[0], expected[1]
	if wire.ActionID != action.ID || expectedMethod == "" || wire.Method != expectedMethod ||
		len(wire.CompletedSteps) != 1 || wire.CompletedSteps[0] != expectedStep ||
		wire.Execution != backend.ExecutionApplied || wire.Verification.Status != backend.VerificationUnavailable ||
		wire.Verification.Reason != "" ||
		(wire.StateStatus != backend.StateUnavailable && wire.StateStatus != backend.StateStatus("partial") && wire.StateStatus != backend.StateAvailable) ||
		wire.Cleanup != backend.CleanupComplete {
		return backend.ActionResult{}, &backend.Error{Code: "backend_unavailable", Message: "The native backend is unavailable."}
	}
	return backend.ActionResult{ActionID: wire.ActionID, Execution: wire.Execution,
		Verification: wire.Verification, StateStatus: wire.StateStatus, Cleanup: wire.Cleanup,
		Method: wire.Method, CompletedSteps: append([]string(nil), wire.CompletedSteps...)}, nil
}

func expectedNativeActionResult(action backend.Action) [2]string {
	switch action.Kind {
	case backend.ActionPress:
		return [2]string{"ax_press", "press"}
	case backend.ActionReplace:
		return [2]string{"ax_set_value", "set_value"}
	case backend.ActionInsert, backend.ActionTypeText:
		return [2]string{"cg_unicode", "unicode"}
	case backend.ActionPick:
		return [2]string{"ax_pick", "pick"}
	case backend.ActionFocus:
		return [2]string{"ax_focus", "focus"}
	case backend.ActionScroll:
		return [2]string{"ax_scroll", "scroll"}
	case backend.ActionClick:
		if action.ElementRef != "" {
			return [2]string{"ax_press", "click"}
		}
		return [2]string{"cg_click", "click"}
	case backend.ActionPressKey:
		return [2]string{"cg_key", "key"}
	case backend.ActionCoordinateScroll:
		return [2]string{"cg_scroll", "scroll"}
	case backend.ActionDrag:
		return [2]string{"cg_drag", "drag"}
	case backend.ActionFocusWindow:
		return [2]string{"ax_focus_window", "focus_window"}
	default:
		return [2]string{}
	}
}

// validateNativeAction mirrors the frozen common Action DTO limits at the
// Darwin boundary. It is intentionally private and cannot grant dispatch.
func validateNativeAction(action backend.Action) bool {
	if !validNativeOpaque(action.ID) || !validNativeOpaque(action.WindowRef) || len(action.Text) > nativeTextByteLimit ||
		!finite(action.X) || !finite(action.Y) || !finite(action.EndX) || !finite(action.EndY) {
		return false
	}
	semantic := action.ElementRef != "" || action.StateID != ""
	if semantic && (!validNativeOpaque(action.ElementRef) || !validNativeOpaque(action.StateID)) {
		return false
	}
	noPoint := action.X == 0 && action.Y == 0 && action.EndX == 0 && action.EndY == 0
	noPointer := action.Button == "" && action.Count == 0 && action.HoldMS == 0
	noKeys := action.Keys == "" && action.HoldMS == 0
	noScroll := action.Direction == "" && action.Amount == "" && action.DX == 0 && action.DY == 0
	noMotion := action.Steps == 0 && action.DurationMS == 0
	switch action.Kind {
	case backend.ActionPress:
		return semantic && action.Text == "" && noPoint && noPointer && action.DelayMS == 0 && noKeys && noScroll && noMotion
	case backend.ActionReplace:
		return semantic && noPoint && noPointer && action.DelayMS == 0 && noKeys && noScroll && noMotion
	case backend.ActionInsert:
		return semantic && action.Text != "" && noPoint && noPointer && action.DelayMS == 0 && noKeys && noScroll && noMotion
	case backend.ActionClick:
		if semantic {
			return action.Text == "" && noPoint && noPointer && action.DelayMS == 0 && noKeys && noScroll && noMotion
		}
		return action.X >= 0 && action.Y >= 0 && action.X <= 1_000_000 && action.Y <= 1_000_000 &&
			(action.Button == "left" || action.Button == "right" || action.Button == "middle") &&
			action.Count >= 1 && action.Count <= 2 && action.HoldMS >= 0 && action.HoldMS <= 1000 &&
			action.EndX == 0 && action.EndY == 0 && action.Text == "" && action.Direction == "" && action.Amount == "" &&
			action.DelayMS == 0 && action.Keys == "" && action.DX == 0 && action.DY == 0 && noMotion
	case backend.ActionPick, backend.ActionFocus:
		return semantic && action.Text == "" && noPoint && noPointer && action.DelayMS == 0 && noKeys && noScroll && noMotion
	case backend.ActionScroll:
		return semantic && oneOf(action.Direction, "up", "down", "left", "right") && oneOf(action.Amount, "line", "page") &&
			action.Text == "" && noPoint && noPointer && action.DelayMS == 0 && noKeys && action.DX == 0 && action.DY == 0 && noMotion
	case backend.ActionTypeText:
		return !semantic && action.Text != "" && action.DelayMS >= 0 && action.DelayMS <= 100 && noPoint && noPointer && noKeys && noScroll && noMotion
	case backend.ActionPressKey:
		return !semantic && validNativeKeys(action.Keys) && action.HoldMS >= 0 && action.HoldMS <= 1000 &&
			action.Text == "" && noPoint && action.Button == "" && action.Count == 0 && action.DelayMS == 0 && noScroll && noMotion
	case backend.ActionCoordinateScroll:
		return !semantic && action.X >= 0 && action.Y >= 0 && action.X <= 1_000_000 && action.Y <= 1_000_000 &&
			action.DX >= -10 && action.DX <= 10 && action.DY >= -10 && action.DY <= 10 && (action.DX != 0 || action.DY != 0) &&
			action.EndX == 0 && action.EndY == 0 && action.Text == "" && action.Direction == "" && action.Amount == "" &&
			action.Button == "" && action.Count == 0 && action.HoldMS == 0 && action.DelayMS == 0 && action.Keys == "" && noMotion
	case backend.ActionDrag:
		return !semantic && action.X >= 0 && action.Y >= 0 && action.EndX >= 0 && action.EndY >= 0 &&
			action.X <= 1_000_000 && action.Y <= 1_000_000 && action.EndX <= 1_000_000 && action.EndY <= 1_000_000 &&
			action.Steps >= 2 && action.Steps <= 64 && action.DurationMS >= 1 && action.DurationMS <= 2000 &&
			action.Text == "" && action.Direction == "" && action.Amount == "" && action.Button == "" && action.Count == 0 &&
			action.HoldMS == 0 && action.DelayMS == 0 && action.Keys == "" && action.DX == 0 && action.DY == 0
	case backend.ActionFocusWindow:
		return !semantic && action.Text == "" && noPoint && noPointer && action.DelayMS == 0 && noKeys && noScroll && noMotion
	default:
		return false
	}
}

func validNativeKeys(keys string) bool {
	parts := strings.Split(keys, " ")
	if len(parts) == 0 || len(parts) > 5 {
		return false
	}
	modifiers := map[string]bool{"ctrl": true, "alt": true, "shift": true, "meta": true}
	seen := make(map[string]bool, len(parts))
	nonModifier := 0
	for _, key := range parts {
		if key == "" || seen[key] {
			return false
		}
		seen[key] = true
		if modifiers[key] {
			continue
		}
		nonModifier++
		if len(key) == 1 && ((key[0] >= 'a' && key[0] <= 'z') || (key[0] >= '0' && key[0] <= '9')) {
			continue
		}
		if !oneOf(key, "enter", "escape", "tab", "space", "backspace", "delete", "up", "down", "left", "right", "home", "end", "page_up", "page_down") {
			return false
		}
	}
	return nonModifier == 1
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func validNativeOpaque(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if (b < '0' || b > '9') && (b < 'A' || b > 'Z') && (b < 'a' || b > 'z') && b != '-' && b != '.' && b != '_' {
			return false
		}
	}
	return true
}
