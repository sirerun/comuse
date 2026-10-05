package policyprobe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

func newTestGate(t *testing.T) (*Gate, *HostAuthority, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	gate, host, err := New(clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return gate, host, clock
}

func validScope(now time.Time) Scope {
	return Scope{
		PrincipalID: "principal-fixture-test",
		SessionID:   "session-fixture-test",
		Process: ProcessIdentity{
			PID: 4312, BundleID: "com.sirerun.comuse.fixture", LaunchGeneration: "launch-7",
		},
		FixtureNonce:  "run-test-7",
		WindowRef:     "window-ref-7",
		WindowTitle:   "Comuse Fixture run-test-7",
		ElementRef:    "element-ref-counter",
		PolicyVersion: PolicyVersion,
		ExpiresAt:     now.Add(time.Minute),
	}
}

func pressAction() Action {
	return Action{Kind: ActionPress, Target: TargetButton, ElementRef: "element-ref-counter"}
}

func approve(t *testing.T, host *HostAuthority, scope Scope, action Action) Approval {
	t.Helper()
	challenge, err := host.Issue(scope, action, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := host.ApproveHost(challenge.ID)
	if err != nil {
		t.Fatal(err)
	}
	return approval
}

func TestDefaultDenyRequiresHostIssuedSingleUseApproval(t *testing.T) {
	gate, host, clock := newTestGate(t)
	scope := validScope(clock.Now())
	if got := new(Gate).Admit(scope, pressAction(), Approval{}); got.Code == DecisionAllowed {
		t.Fatal("zero-value gate allowed an action")
	}
	if got := gate.Admit(scope, pressAction(), Approval{}); got.Code != DecisionDeniedMissingApproval {
		t.Fatalf("zero-value approval decision = %q, want missing approval", got.Code)
	}

	approval := approve(t, host, scope, pressAction())
	if got := gate.Admit(scope, pressAction(), approval); got.Code != DecisionAllowed {
		t.Fatalf("approved decision = %q, want allowed", got.Code)
	}
	if got := gate.Admit(scope, pressAction(), approval); got.Code != DecisionDeniedReplay {
		t.Fatalf("replay decision = %q, want replay", got.Code)
	}
}

func TestApprovalBindsProcessLaunchWindowElementAndActionPayload(t *testing.T) {
	gate, host, clock := newTestGate(t)
	scope := validScope(clock.Now())
	action := Action{Kind: ActionReplaceText, Target: TargetTextField, ElementRef: scope.ElementRef, Text: "synthetic approved text"}
	approval := approve(t, host, scope, action)

	wrong := scope
	wrong.Process.LaunchGeneration = "launch-8"
	if got := gate.Admit(wrong, action, approval); got.Code != DecisionDeniedBindingMismatch {
		t.Fatalf("wrong launch generation decision = %q", got.Code)
	}
	changed := action
	changed.Text = "different text"
	if got := gate.Admit(scope, changed, approval); got.Code != DecisionDeniedBindingMismatch {
		t.Fatalf("changed payload decision = %q", got.Code)
	}
	changedExpiry := scope
	changedExpiry.ExpiresAt = scope.ExpiresAt.Add(-time.Second)
	if got := gate.Admit(changedExpiry, action, approval); got.Code != DecisionDeniedBindingMismatch {
		t.Fatalf("changed scope expiry decision = %q", got.Code)
	}

	// A title is descriptive only. The opaque window reference remains bound.
	titleOnly := scope
	titleOnly.WindowTitle = "Human-readable title refresh"
	if got := gate.Admit(titleOnly, action, approval); got.Code != DecisionAllowed {
		t.Fatalf("title-only change decision = %q, want allowed", got.Code)
	}
}

func TestWrongWindowAndTargetAreDenied(t *testing.T) {
	gate, host, clock := newTestGate(t)
	scope := validScope(clock.Now())
	action := pressAction()
	approval := approve(t, host, scope, action)

	wrongWindow := scope
	wrongWindow.WindowRef = "another-window"
	if got := gate.Admit(wrongWindow, action, approval); got.Code != DecisionDeniedBindingMismatch {
		t.Fatalf("wrong window decision = %q", got.Code)
	}
	wrongTarget := action
	wrongTarget.ElementRef = "another-element"
	if got := gate.Admit(scope, wrongTarget, approval); got.Code != DecisionDeniedBindingMismatch {
		t.Fatalf("wrong target decision = %q", got.Code)
	}
	if got := gate.Admit(scope, action, approval); got.Code != DecisionAllowed {
		t.Fatalf("original binding decision = %q, want allowed", got.Code)
	}
}

func TestExpiredScopeAndPolicyVersionDenyBeforeActionAdmission(t *testing.T) {
	gate, _, clock := newTestGate(t)
	scope := validScope(clock.Now())
	expired := scope
	expired.ExpiresAt = clock.Now().Add(-time.Nanosecond)
	if got := gate.Admit(expired, Action{Kind: ActionReplaceText, Target: TargetSecureField, ElementRef: scope.ElementRef, Text: "sensitive-canary"}, Approval{}); got.Code != DecisionDeniedExpired {
		t.Fatalf("expired-scope decision = %q", got.Code)
	}
	wrongVersion := scope
	wrongVersion.PolicyVersion++
	if got := gate.Admit(wrongVersion, Action{Kind: ActionReplaceText, Target: TargetTextField, ElementRef: scope.ElementRef, Text: "sensitive-canary"}, Approval{}); got.Code != DecisionDeniedPolicyVersion {
		t.Fatalf("wrong-policy-version decision = %q", got.Code)
	}
}

func TestSecureTargetDeniedBeforePayloadDigest(t *testing.T) {
	_, host, clock := newTestGate(t)
	scope := validScope(clock.Now())
	action := Action{Kind: ActionReadValue, Target: TargetSecureField, ElementRef: scope.ElementRef, Text: "never hash this secret"}
	if _, err := host.Issue(scope, action, 30*time.Second); !errors.Is(err, ErrProtectedTarget) {
		t.Fatalf("secure target issue error = %v, want protected target", err)
	}
}

func TestIssueRequiresValidScopeActionAndBoundedTTL(t *testing.T) {
	_, host, clock := newTestGate(t)
	scope := validScope(clock.Now())
	if _, err := host.Issue(scope, pressAction(), maxChallengeTTL+time.Second); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("overlong TTL error = %v", err)
	}
	if _, err := host.Issue(scope, Action{Kind: ActionKind(255), Target: TargetButton, ElementRef: scope.ElementRef}, 30*time.Second); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("unknown action error = %v", err)
	}
	wrongTarget := pressAction()
	wrongTarget.ElementRef = "not-the-scoped-element"
	if _, err := host.Issue(scope, wrongTarget, 30*time.Second); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("wrong issue target error = %v", err)
	}
}

func TestActiveChallengeCapacityRejectsWithoutEviction(t *testing.T) {
	_, host, clock := newTestGate(t)
	scope := validScope(clock.Now())
	for range maxChallenges {
		if _, err := host.Issue(scope, pressAction(), 30*time.Second); err != nil {
			t.Fatalf("issue within capacity: %v", err)
		}
	}
	if _, err := host.Issue(scope, pressAction(), 30*time.Second); !errors.Is(err, ErrCapacity) {
		t.Fatalf("over-capacity issue error = %v, want capacity", err)
	}
}

func TestApprovalExpiryAndPerCapabilityRevocation(t *testing.T) {
	gate, host, clock := newTestGate(t)
	scope := validScope(clock.Now())
	approval := approve(t, host, scope, pressAction())
	if !host.Revoke(approval) || host.Revoke(approval) {
		t.Fatal("approval revoke was not single-shot")
	}
	if got := gate.Admit(scope, pressAction(), approval); got.Code != DecisionDeniedRevoked {
		t.Fatalf("revoked decision = %q", got.Code)
	}

	second := approve(t, host, scope, pressAction())
	clock.Advance(31 * time.Second)
	if got := gate.Admit(scope, pressAction(), second); got.Code != DecisionDeniedExpired {
		t.Fatalf("expired capability decision = %q", got.Code)
	}
}

func TestRevokeScopeAndSessionInvalidatePendingAndApprovedGrants(t *testing.T) {
	gate, host, clock := newTestGate(t)
	scope := validScope(clock.Now())
	pending, err := host.Issue(scope, pressAction(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	approved := approve(t, host, scope, pressAction())
	if got := host.RevokeScope(scope); got != 2 {
		t.Fatalf("scope revoke removed %d grants, want 2", got)
	}
	if _, err := host.ApproveHost(pending.ID); !errors.Is(err, ErrChallengeNotFound) {
		t.Fatalf("revoked challenge approval error = %v", err)
	}
	if got := gate.Admit(scope, pressAction(), approved); got.Code != DecisionDeniedRevoked {
		t.Fatalf("scope-revoked approval decision = %q", got.Code)
	}

	otherScope := scope
	otherScope.ElementRef = "other-element"
	otherAction := Action{Kind: ActionPress, Target: TargetButton, ElementRef: otherScope.ElementRef}
	other := approve(t, host, otherScope, otherAction)
	if got := host.RevokeSession(scope.PrincipalID, scope.SessionID); got != 1 {
		t.Fatalf("session revoke removed %d grants, want 1", got)
	}
	if got := gate.Admit(otherScope, otherAction, other); got.Code != DecisionDeniedRevoked {
		t.Fatalf("session-revoked approval decision = %q", got.Code)
	}
}

func TestConcurrentAdmissionConsumesApprovalExactlyOnce(t *testing.T) {
	gate, host, clock := newTestGate(t)
	scope := validScope(clock.Now())
	action := pressAction()
	approval := approve(t, host, scope, action)

	const contenders = 48
	results := make(chan DecisionCode, contenders)
	var workers sync.WaitGroup
	workers.Add(contenders)
	for range contenders {
		go func() {
			defer workers.Done()
			results <- gate.Admit(scope, action, approval).Code
		}()
	}
	workers.Wait()
	close(results)
	allowed, replayed := 0, 0
	for result := range results {
		switch result {
		case DecisionAllowed:
			allowed++
		case DecisionDeniedReplay:
			replayed++
		default:
			t.Errorf("unexpected admission result: %q", result)
		}
	}
	if allowed != 1 || replayed != contenders-1 {
		t.Fatalf("concurrent outcomes: allowed=%d replayed=%d", allowed, replayed)
	}
}

func TestReadValueNeedsExplicitHostApproval(t *testing.T) {
	gate, host, clock := newTestGate(t)
	scope := validScope(clock.Now())
	action := Action{Kind: ActionReadValue, Target: TargetTextField, ElementRef: scope.ElementRef}
	if got := gate.Admit(scope, action, Approval{}); got.Code != DecisionDeniedMissingApproval {
		t.Fatalf("unapproved value read = %q", got.Code)
	}
	approval := approve(t, host, scope, action)
	if got := gate.Admit(scope, action, approval); got.Code != DecisionAllowed {
		t.Fatalf("approved synthetic value read = %q", got.Code)
	}
}

func TestDecisionChallengeAndAuditAreRedacted(t *testing.T) {
	gate, host, clock := newTestGate(t)
	scope := validScope(clock.Now())
	canary := "synthetic-sensitive-canary-value"
	action := Action{Kind: ActionReplaceText, Target: TargetTextField, ElementRef: scope.ElementRef, Text: canary}
	challenge, err := host.Issue(scope, action, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := host.ApproveHost(challenge.ID)
	if err != nil {
		t.Fatal(err)
	}
	decision := gate.Admit(scope, action, approval)
	if decision.Code != DecisionAllowed {
		t.Fatalf("decision = %q", decision.Code)
	}

	challengeJSON, err := json.Marshal(challenge)
	if err != nil {
		t.Fatal(err)
	}
	decisionJSON, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	auditJSON, err := json.Marshal(gate.AuditSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{challengeJSON, decisionJSON, auditJSON} {
		if strings.Contains(string(data), canary) {
			t.Fatal("raw action content appeared in challenge, decision, or audit")
		}
		digest := sha256.Sum256([]byte(canary))
		if strings.Contains(string(data), hex.EncodeToString(digest[:])) {
			t.Fatal("bare payload digest appeared in challenge, decision, or audit")
		}
	}
	if strings.Contains(string(auditJSON), "element-ref-counter") {
		t.Fatal("audit exposed the native target reference")
	}
}
