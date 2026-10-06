package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorCodesNeverEchoUnknownPayloads(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{&Error{Code: "permission_denied", Message: "sensitive native content"}, "permission_denied"},
		{fmt.Errorf("wrapped: %w", &Error{Code: "element_stale", Message: "sensitive content"}), "element_stale"},
		{&Error{Code: "private text from native", Message: "private value"}, "internal_error"},
		{errors.New("private raw error"), "internal_error"},
		{context.Canceled, "cancelled"}, {context.DeadlineExceeded, "budget_exceeded"},
	} {
		if got := ErrorCode(tc.err); got != tc.code {
			t.Fatalf("got %s want %s", got, tc.code)
		}
	}
}
func TestResultVocabularyFollowsRFC(t *testing.T) {
	r := ActionResult{ActionID: "a1", Execution: ExecutionPartiallyApplied, Verification: Verification{Status: VerificationUnavailable}, StateStatus: StateUnavailable, Cleanup: CleanupDirty}
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	for _, part := range []string{`"execution":"partially_applied"`, `"verification":{"status":"unavailable"}`, `"state_status":"unavailable"`, `"cleanup":"dirty"`} {
		if !strings.Contains(string(b), part) {
			t.Fatalf("missing %s: %s", part, b)
		}
	}
}
