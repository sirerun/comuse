package comuse

import (
	"context"
	"testing"
)

func scrollFixture(t *testing.T) (*Session, *fakeBackend, Observation) {
	t.Helper()
	enabled := true
	b := &fakeBackend{process: testProcess(), nativeState: "native-scroll", input: true, qualified: true, elements: []Element{{Ref: "scroll-1", Role: "AXScrollArea", Classification: "normal", Enabled: &enabled, Actions: []string{ActionScroll}}}}
	s := newTestSession(t, b, true)
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	if _, err := s.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	o, err := s.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	return s, b, o
}
func TestSemanticScrollBoundsBeforeNative(t *testing.T) {
	for _, tc := range []struct{ direction, amount string }{{"up", "line"}, {"down", "page"}, {"left", "line"}, {"right", "page"}} {
		a := Action{ID: "a", WindowRef: "w", ElementRef: "e", StateID: "s", Kind: ActionScroll, Direction: tc.direction, Amount: tc.amount}
		if err := validateAction(a); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ direction, amount, text string }{{"", "line", ""}, {"diagonal", "line", ""}, {"up", "", ""}, {"up", "two", ""}, {"up", "page", "hidden"}} {
		s, b, o := scrollFixture(t)
		a := Action{ID: "a", WindowRef: o.WindowRef, ElementRef: "scroll-1", StateID: o.StateID, Kind: ActionScroll, Direction: tc.direction, Amount: tc.amount, Text: tc.text}
		_, err := s.Do(context.Background(), a)
		if ErrorCode(err) != "invalid_request" {
			t.Fatalf("%+v: %v", tc, err)
		}
		if len(b.executed) != 0 {
			t.Fatal("invalid scroll dispatched")
		}
	}
	a := Action{ID: "a", WindowRef: "w", ElementRef: "e", StateID: "s", Kind: ActionPress, Direction: "up", Amount: "line"}
	if ErrorCode(validateAction(a)) != "invalid_request" {
		t.Fatal("mixed press scroll accepted")
	}
}
func TestSemanticScrollOneUnitAndReplayBinding(t *testing.T) {
	s, b, o := scrollFixture(t)
	call := func(direction string) (ActionResult, error) {
		return s.ScrollElement(context.Background(), "scroll-a", o.WindowRef, "scroll-1", o.StateID, direction, "page")
	}
	r, err := call("down")
	if err != nil || r.Execution != ExecutionApplied {
		t.Fatalf("scroll: %+v %v", r, err)
	}
	if len(b.executed) != 1 || b.executed[0].Direction != "down" || b.executed[0].Amount != "page" {
		t.Fatal("wrong unit or dispatch count")
	}
	if _, err = call("down"); err != nil {
		t.Fatal("exact replay:", err)
	}
	if len(b.executed) != 1 {
		t.Fatal("replay redispatched")
	}
	if _, err = call("up"); ErrorCode(err) != "policy_refused" {
		t.Fatal("changed direction replay accepted:", err)
	}
	if len(b.executed) != 1 {
		t.Fatal("mismatch redispatched")
	}
}
func TestSemanticScrollFreshEligibilityAndCapability(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*fakeBackend)
	}{
		{"stale", "element_stale", func(b *fakeBackend) { b.elements[0].Label = "changed" }},
		{"protected", "element_stale", func(b *fakeBackend) { b.elements[0].Classification = "secure" }},
		{"disabled", "element_stale", func(b *fakeBackend) { v := false; b.elements[0].Enabled = &v }},
		{"partial", "element_stale", func(b *fakeBackend) { b.partial = true }},
		{"unqualified", "policy_refused", func(b *fakeBackend) { b.qualified = false }},
		{"permission", "permission_denied", func(b *fakeBackend) { b.accessibilityDenied = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, b, o := scrollFixture(t)
			tc.change(b)
			_, err := s.ScrollElement(context.Background(), "a", o.WindowRef, "scroll-1", o.StateID, "up", "line")
			if ErrorCode(err) != tc.code {
				t.Fatalf("want %s got %v", tc.code, err)
			}
			if len(b.executed) != 0 {
				t.Fatal("ineligible scroll dispatched")
			}
		})
	}
}

func TestDispatchMetadataPreservedOnDurableReplay(t *testing.T) {
	s, b, o := scrollFixture(t)
	b.executeHook = func() (ActionResult, error) {
		return ActionResult{ActionID: "metadata-a", Execution: ExecutionApplied, Method: "ax_scroll", CompletedSteps: []string{"focus", "scroll", "cleanup"}, Verification: Verification{Status: VerificationVerified, Reason: "postcondition_met"}, StateStatus: StateAvailable, Cleanup: CleanupComplete}, nil
	}
	for i := 0; i < 2; i++ {
		r, err := s.ScrollElement(context.Background(), "metadata-a", o.WindowRef, "scroll-1", o.StateID, "up", "line")
		if err != nil {
			t.Fatal(err)
		}
		if r.Method != "ax_scroll" || len(r.CompletedSteps) != 3 || r.CompletedSteps[1] != "scroll" || r.Verification.Reason != "postcondition_met" {
			t.Fatalf("metadata dropped on call%d: %+v", i, r)
		}
		r.CompletedSteps[1] = "caller mutation"
	}
	if len(b.executed) != 1 {
		t.Fatal("metadata replay dispatched again")
	}
}
func TestDispatchMetadataClosedVocabulary(t *testing.T) {
	for _, r := range []ActionResult{{Method: "secret backend detail"}, {Method: "ax_scroll", CompletedSteps: []string{"requested text"}}, {CompletedSteps: make([]string, 129)}} {
		if safeDispatchMetadata(r) {
			t.Fatal("unsafe metadata admitted")
		}
	}
	if !safeDispatchMetadata(ActionResult{Method: "ax_press", CompletedSteps: []string{"focus", "press", "cleanup"}}) {
		t.Fatal("safe metadata refused")
	}
}
