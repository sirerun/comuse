package comuse

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateActionSourcePhaseOperations(t *testing.T) {
	base := Action{ID: "a1", WindowRef: "w1", ElementRef: "e1", StateID: "s1", Kind: ActionPress}
	if err := validateAction(base); err != nil {
		t.Fatalf("valid press: %v", err)
	}
	base.Text = "unexpected"
	if ErrorCode(validateAction(base)) != "invalid_request" {
		t.Fatal("press accepted text")
	}
	base.Kind, base.Text = ActionReplace, ""
	if err := validateAction(base); err != nil {
		t.Fatalf("empty replacement must be allowed: %v", err)
	}
	base.Kind = ActionInsert
	if ErrorCode(validateAction(base)) != "invalid_request" {
		t.Fatal("empty insert accepted")
	}
	base.Kind, base.Text = "launch_script", ""
	if ErrorCode(validateAction(base)) != "unsupported" {
		t.Fatal("unsupported operation was not rejected")
	}
	base.Kind, base.Text = ActionReplace, strings.Repeat("x", maxActionTextBytes+1)
	if ErrorCode(validateAction(base)) != "invalid_request" {
		t.Fatal("oversized replacement accepted")
	}
	base.Text, base.ElementRef = "ok", "élément"
	if ErrorCode(validateAction(base)) != "invalid_request" {
		t.Fatal("non-ASCII reference accepted")
	}
}

func TestApprovalBindingIsExactAndExpires(t *testing.T) {
	now := time.Now().UTC()
	request := ApprovalRequest{SessionID: "session", Action: Action{ID: "action", Kind: ActionPress}, Process: testProcess(), ObservedAt: now.Add(-time.Second), PolicyVersion: 1, ExpiresAt: now.Add(time.Minute)}
	approval := Approval(request)
	if err := validateApprovalBinding(request, approval, now); err != nil {
		t.Fatalf("valid approval rejected: %v", err)
	}
	changed := approval
	changed.Process.PID++
	if ErrorCode(validateApprovalBinding(request, changed, now)) != "policy_refused" {
		t.Fatal("approval for another process accepted")
	}
	changed = approval
	changed.ExpiresAt = now.Add(3 * time.Minute)
	if ErrorCode(validateApprovalBinding(request, changed, now)) != "policy_refused" {
		t.Fatal("approval expiry binding mismatch accepted")
	}
	if ErrorCode(validateApprovalBinding(request, approval, approval.ExpiresAt)) != "approval_required" {
		t.Fatal("expired approval accepted")
	}
}

func TestBackendErrorsAreMappedToStableSafeCodes(t *testing.T) {
	private := &Error{Code: "native_secret", Message: "raw native detail"}
	safe := sanitizedBackendError(private)
	if ErrorCode(safe) != "internal_error" || strings.Contains(safe.Error(), "native_secret") || strings.Contains(safe.Error(), "raw native detail") {
		t.Fatalf("unsafe backend error escaped: %v", safe)
	}
	known := &Error{Code: "permission_denied", Message: "TCC private detail"}
	safe = sanitizedBackendError(known)
	if ErrorCode(safe) != "permission_denied" || strings.Contains(safe.Error(), "TCC") {
		t.Fatalf("known backend code not safely retained: %v", safe)
	}
	if stableCallError(errors.New("private native failure")) == nil {
		t.Fatal("backend error mapping returned nil")
	}
}
