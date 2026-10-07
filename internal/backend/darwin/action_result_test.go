package darwin

import (
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
				Cleanup: backend.CleanupComplete, Method: test.method, CompletedSteps: []string{test.step}}
			result, err := decodeActionResult(wire, action)
			if err != nil || result.ActionID != action.ID || result.Method != test.method ||
				len(result.CompletedSteps) != 1 || result.CompletedSteps[0] != test.step {
				t.Fatalf("decodeActionResult: result=%#v err=%v", result, err)
			}
			result.CompletedSteps[0] = "mutated"
			if wire.CompletedSteps[0] != test.step {
				t.Fatalf("result retained native backing slice: %#v", wire.CompletedSteps)
			}
		})
	}
}

func TestDecodeNativeActionResultRejectsMethodAndOutcomeDrift(t *testing.T) {
	action := backend.Action{ID: "a1", WindowRef: "w1", ElementRef: "e1", StateID: "s1", Kind: backend.ActionInsert, Text: "x"}
	valid := nativeActionResult{ActionID: "a1", Execution: backend.ExecutionApplied,
		Verification: backend.Verification{Status: backend.VerificationUnavailable}, StateStatus: backend.StateUnavailable,
		Cleanup: backend.CleanupComplete, Method: "cg_unicode", CompletedSteps: []string{"unicode"}}
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
