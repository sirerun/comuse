package darwin

import (
	"math"
	"testing"

	"github.com/sirerun/comuse/internal/backend"
)

func TestDecodeNativeActionResultMapsOnlyExactMethodsAndSteps(t *testing.T) {
	cases := []struct {
		kind, method, step string
	}{
		{backend.ActionPress, "ax_press", "press"},
		{backend.ActionReplace, "ax_set_value", "set_value"},
		{backend.ActionInsert, "cg_unicode", "unicode"},
	}
	for _, test := range cases {
		t.Run(test.kind, func(t *testing.T) {
			action := backend.Action{ID: "a1", WindowRef: "w1", ElementRef: "e1", StateID: "s1", Kind: test.kind, Text: "x"}
			wire := nativeActionResult{ActionID: action.ID, Execution: backend.ExecutionApplied,
				Verification: backend.Verification{Status: backend.VerificationUnavailable}, StateStatus: backend.StateUnavailable,
				Cleanup: backend.CleanupComplete, Method: test.method, CompletedSteps: expectedNativeActionResult(action).steps}
			result, err := decodeActionResult(wire, action)
			if err != nil || result.ActionID != action.ID || result.Method != test.method ||
				len(result.CompletedSteps) != len(expectedNativeActionResult(action).steps) {
				t.Fatalf("decodeActionResult: result=%#v err=%v", result, err)
			}
			result.CompletedSteps[0] = "mutated"
			if wire.CompletedSteps[0] != expectedNativeActionResult(action).steps[0] {
				t.Fatalf("result retained native backing slice: %#v", wire.CompletedSteps)
			}
		})
	}
}

func TestNativeInventoryValidationAndMethodMapping(t *testing.T) {
	cases := []struct {
		name   string
		action backend.Action
		method string
	}{
		{"semantic_click_as_press", backend.Action{ID: "a", WindowRef: "w", ElementRef: "e", StateID: "s", Kind: backend.ActionPress}, "ax_press"},
		{"pick", backend.Action{ID: "a", WindowRef: "w", ElementRef: "e", StateID: "s", Kind: backend.ActionPick}, "ax_pick"},
		{"focus", backend.Action{ID: "a", WindowRef: "w", ElementRef: "e", StateID: "s", Kind: backend.ActionFocus}, "ax_focus"},
		{"semantic_scroll", backend.Action{ID: "a", WindowRef: "w", ElementRef: "e", StateID: "s", Kind: backend.ActionScroll, Direction: "down", Amount: "line"}, "cg_scroll"},
		{"developer_click", backend.Action{ID: "a", WindowRef: "w", Kind: backend.ActionClick, X: 1, Y: 1, Button: "left", Count: 1}, "cg_click"},
		{"type_text", backend.Action{ID: "a", WindowRef: "w", Kind: backend.ActionTypeText, Text: "hello"}, "cg_unicode"},
		{"press_key", backend.Action{ID: "a", WindowRef: "w", Kind: backend.ActionPressKey, Keys: "ctrl a"}, "cg_key"},
		{"coordinate_scroll", backend.Action{ID: "a", WindowRef: "w", Kind: backend.ActionCoordinateScroll, X: 1, Y: 2, DX: 1}, "cg_scroll"},
		{"drag", backend.Action{ID: "a", WindowRef: "w", Kind: backend.ActionDrag, X: 1, Y: 2, EndX: 3, EndY: 4, Steps: 2, DurationMS: 1}, "cg_drag"},
		{"focus_window", backend.Action{ID: "a", WindowRef: "w", Kind: backend.ActionFocusWindow}, "ax_focus_window"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !validateNativeAction(tc.action) {
				t.Fatal("valid frozen action rejected")
			}
			expected := expectedNativeActionResult(tc.action)
			if expected.method != tc.method {
				t.Fatalf("method = %q, want %q", expected.method, tc.method)
			}
			wire := nativeActionResult{ActionID: tc.action.ID, Execution: backend.ExecutionApplied,
				Verification: backend.Verification{Status: backend.VerificationUnavailable}, StateStatus: backend.StateUnavailable,
				Cleanup: backend.CleanupComplete, Method: expected.method, CompletedSteps: expected.steps}
			if _, err := decodeActionResult(wire, tc.action); err != nil {
				t.Fatalf("decode method result: %v", err)
			}
		})
	}
}

func TestNativeInventoryRejectsOutOfContractActions(t *testing.T) {
	base := backend.Action{ID: "a", WindowRef: "w", Kind: backend.ActionPressKey, Keys: "ctrl a"}
	cases := []struct {
		name   string
		mutate func(*backend.Action)
	}{
		{"duplicate_key", func(a *backend.Action) { a.Keys = "ctrl ctrl a" }},
		{"two_non_modifiers", func(a *backend.Action) { a.Keys = "a b" }},
		{"unknown_key", func(a *backend.Action) { a.Keys = "ctrl fn" }},
		{"nan_point", func(a *backend.Action) { a.Kind = backend.ActionClick; a.Keys = ""; a.X = math.NaN() }},
		{"semantic_missing_state", func(a *backend.Action) { a.Kind = backend.ActionPick; a.ElementRef = "e" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action := base
			tc.mutate(&action)
			if validateNativeAction(action) {
				t.Fatal("invalid action accepted")
			}
		})
	}
}

func TestDecodeNativeActionResultRejectsMethodAndOutcomeDrift(t *testing.T) {
	action := backend.Action{ID: "a1", WindowRef: "w1", ElementRef: "e1", StateID: "s1", Kind: backend.ActionInsert, Text: "x"}
	valid := nativeActionResult{ActionID: "a1", Execution: backend.ExecutionApplied,
		Verification: backend.Verification{Status: backend.VerificationUnavailable}, StateStatus: backend.StateUnavailable,
		Cleanup: backend.CleanupComplete, Method: "cg_unicode", CompletedSteps: []string{"key_down", "key_up"}}
	cases := map[string]func(*nativeActionResult){
		"wrong_method":            func(r *nativeActionResult) { r.Method = "ax_set_value" },
		"wrong_steps":             func(r *nativeActionResult) { r.CompletedSteps = []string{"unicode", "set_value"} },
		"wrong_action":            func(r *nativeActionResult) { r.ActionID = "a2" },
		"uncertain_cleanup":       func(r *nativeActionResult) { r.Cleanup = backend.CleanupUnknown },
		"unexpected_verification": func(r *nativeActionResult) { r.Verification.Reason = "private detail" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			wire := valid
			wire.CompletedSteps = append([]string(nil), valid.CompletedSteps...)
			change(&wire)
			if _, err := decodeActionResult(wire, action); err == nil || backend.ErrorCode(err) != "backend_unavailable" {
				t.Fatalf("decode accepted invalid native result: %v", err)
			}
		})
	}
}

func TestDecodeNativeActionResultPreservesPartialAndUnknownOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		action backend.Action
		wire   nativeActionResult
	}{
		{
			name:   "partial_key_cleanup_complete",
			action: backend.Action{ID: "a1", WindowRef: "w1", Kind: backend.ActionTypeText, Text: "ab"},
			wire: nativeActionResult{ActionID: "a1", Execution: backend.ExecutionPartiallyApplied,
				Verification: backend.Verification{Status: backend.VerificationUnavailable}, StateStatus: backend.StateUnavailable,
				Cleanup: backend.CleanupComplete, Method: "cg_unicode", CompletedSteps: []string{"key_down", "cleanup"}},
		},
		{
			name:   "unknown_coordinate_release_uncertain",
			action: backend.Action{ID: "a1", WindowRef: "w1", Kind: backend.ActionClick, Button: "left", Count: 1},
			wire: nativeActionResult{ActionID: "a1", Execution: backend.ExecutionUnknown,
				Verification: backend.Verification{Status: backend.VerificationUnavailable}, StateStatus: backend.StateUnavailable,
				Cleanup: backend.CleanupUnknown, Method: "cg_click", CompletedSteps: []string{"mouse_down"}},
		},
		{
			name:   "pre_dispatch_cancel",
			action: backend.Action{ID: "a1", WindowRef: "w1", Kind: backend.ActionPressKey, Keys: "a"},
			wire: nativeActionResult{ActionID: "a1", Execution: backend.ExecutionNotApplied,
				Verification: backend.Verification{Status: backend.VerificationUnavailable}, StateStatus: backend.StateUnavailable,
				Cleanup: backend.CleanupComplete, Method: "cg_key"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeActionResult(tc.wire, tc.action)
			if err != nil {
				t.Fatalf("decodeActionResult: %v", err)
			}
			if got.Execution != tc.wire.Execution || got.Cleanup != tc.wire.Cleanup || len(got.CompletedSteps) != len(tc.wire.CompletedSteps) {
				t.Fatalf("lost native outcome: got=%#v wire=%#v", got, tc.wire)
			}
		})
	}
}
