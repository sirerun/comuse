package darwin

import "github.com/sirerun/comuse/internal/backend"

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
	expected := map[string][2]string{
		backend.ActionPress:   {"ax_press", "press"},
		backend.ActionReplace: {"ax_set_value", "set_value"},
		backend.ActionInsert:  {"cg_unicode", "unicode"},
	}[action.Kind]
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
