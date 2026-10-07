package comuse

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sirerun/comuse/internal/writer"
)

type rawTestBackend struct {
	*fakeBackend
	actionKinds  []string
	windowsCalls int
	windowsErr   error
}

func (b *rawTestBackend) Doctor(context.Context) (DoctorReport, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.doctorCalls++
	return DoctorReport{Capabilities: Capabilities{
		Accessibility: true, Input: true, QualifiedInput: true,
		ActionKinds: append([]string(nil), b.actionKinds...),
	}, Permissions: map[string]string{"accessibility": "granted"}}, nil
}

func (b *rawTestBackend) Windows(ctx context.Context, budget Budget) ([]Window, error) {
	b.mu.Lock()
	b.windowsCalls++
	err := b.windowsErr
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return b.fakeBackend.Windows(ctx, budget)
}

func newRawTestSession(t *testing.T, kinds []string, approval ApprovalProvider) (*Session, *rawTestBackend) {
	t.Helper()
	base := &fakeBackend{process: testProcess(), input: true, qualified: true}
	backend := &rawTestBackend{fakeBackend: base, actionKinds: kinds}
	if approval == nil {
		approval = fixedApproval{}
	}
	session, err := newSyntheticSession(Config{
		Backend: backend, Scope: Scope{Processes: []ProcessIdentity{base.process}, ExpiresAt: time.Now().Add(time.Hour)},
		Budget: testBudget(), ApprovalProvider: approval, WriterDirectory: filepath.Join(t.TempDir(), "journal"),
		WriterKey: []byte(strings.Repeat("r", 32)), MaxActions: 64,
	})
	if err != nil {
		t.Fatalf("new raw session: %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	return session, backend
}

func allRawKinds() []string {
	return []string{developerActionClick, developerActionTypeText, developerActionPressKey, developerActionCoordinateScroll, developerActionDrag, developerActionFocusWindow}
}

func TestDeveloperActionsCallRoutesAndComparableDTO(t *testing.T) {
	session, backend := newRawTestSession(t, allRawKinds(), nil)
	requests := []struct {
		request Request
		kind    string
	}{
		{Request{Operation: OperationClick, Click: &ClickParams{WindowTarget: WindowTarget{"call-click", "window-1"}, Point: Point{X: 2, Y: 3}, Button: "right", Count: 2, HoldMS: 20}}, developerActionClick},
		{Request{Operation: OperationTypeText, TypeText: &TypeTextParams{WindowTarget: WindowTarget{"call-text", "window-1"}, Text: "héllo 🧭", DelayMS: 4}}, developerActionTypeText},
		{Request{Operation: OperationPressKey, PressKey: &PressKeyParams{WindowTarget: WindowTarget{"call-key", "window-1"}, Keys: []string{"shift", "ctrl", "a"}, HoldMS: 25}}, developerActionPressKey},
		{Request{Operation: OperationScroll, Scroll: &ScrollParams{WindowTarget: WindowTarget{"call-scroll", "window-1"}, Point: Point{X: 7, Y: 8}, DX: -3, DY: 6}}, developerActionCoordinateScroll},
		{Request{Operation: OperationDrag, Drag: &DragParams{WindowTarget: WindowTarget{"call-drag", "window-1"}, Start: Point{X: 1, Y: 2}, End: Point{X: 9, Y: 10}, Steps: 4, DurationMS: 100}}, developerActionDrag},
		{Request{Operation: OperationFocusWindow, FocusWindow: &WindowTarget{ActionID: "call-focus", WindowRef: "window-1"}}, developerActionFocusWindow},
	}
	for _, tc := range requests {
		t.Run(string(tc.kind), func(t *testing.T) {
			envelope, err := session.Call(context.Background(), tc.request)
			if err != nil || envelope.Status != "ok" || envelope.ActionID == nil || envelope.Execution != string(ExecutionApplied) {
				t.Fatalf("Call: envelope=%+v err=%v", envelope, err)
			}
		})
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.executed) != len(requests) {
		t.Fatalf("Execute count=%d want=%d", len(backend.executed), len(requests))
	}
	got := map[string]Action{}
	for _, action := range backend.executed {
		got[action.ID] = action
	}
	if got["call-click"].Kind != developerActionClick || got["call-click"].X != 2 || got["call-click"].Y != 3 || got["call-click"].Button != "right" || got["call-click"].Count != 2 || got["call-click"].HoldMS != 20 {
		t.Fatalf("click DTO=%+v", got["call-click"])
	}
	if got["call-text"].Text != "héllo 🧭" || got["call-text"].DelayMS != 4 {
		t.Fatalf("type DTO=%+v", got["call-text"])
	}
	if got["call-key"].Keys != "ctrl shift a" || got["call-key"].HoldMS != 25 {
		t.Fatalf("key DTO=%+v", got["call-key"])
	}
	if got["call-scroll"].Kind != developerActionCoordinateScroll || got["call-scroll"].DX != -3 || got["call-scroll"].DY != 6 {
		t.Fatalf("scroll DTO=%+v", got["call-scroll"])
	}
	if got["call-drag"].EndX != 9 || got["call-drag"].EndY != 10 || got["call-drag"].Steps != 4 || got["call-drag"].DurationMS != 100 {
		t.Fatalf("drag DTO=%+v", got["call-drag"])
	}
	if got["call-focus"].Kind != developerActionFocusWindow || got["call-focus"].WindowRef != "window-1" || got["call-focus"].StateID != "" {
		t.Fatalf("focus DTO=%+v", got["call-focus"])
	}
}

func TestDeveloperActionMethodsUseTypedRequests(t *testing.T) {
	session, backend := newRawTestSession(t, allRawKinds(), nil)
	methods := []struct {
		name string
		call func() (ActionResult, error)
	}{
		{"Click", func() (ActionResult, error) {
			return session.Click(context.Background(), ClickParams{WindowTarget: WindowTarget{"api-click", "window-1"}, Point: Point{}, Button: "left", Count: 1})
		}},
		{"TypeText", func() (ActionResult, error) {
			return session.TypeText(context.Background(), TypeTextParams{WindowTarget: WindowTarget{"api-text", "window-1"}, Text: "unicode λ"})
		}},
		{"PressKey", func() (ActionResult, error) {
			return session.PressKey(context.Background(), PressKeyParams{WindowTarget: WindowTarget{"api-key", "window-1"}, Keys: []string{"enter"}})
		}},
		{"Scroll", func() (ActionResult, error) {
			return session.Scroll(context.Background(), ScrollParams{WindowTarget: WindowTarget{"api-scroll", "window-1"}, Point: Point{}, DY: 1})
		}},
		{"Drag", func() (ActionResult, error) {
			return session.Drag(context.Background(), DragParams{WindowTarget: WindowTarget{"api-drag", "window-1"}, Start: Point{}, End: Point{X: 2, Y: 2}, Steps: 2, DurationMS: 1})
		}},
		{"FocusWindow", func() (ActionResult, error) {
			return session.FocusWindow(context.Background(), WindowTarget{"api-focus", "window-1"})
		}},
	}
	for _, tc := range methods {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.call()
			if err != nil || result.Execution != ExecutionApplied || result.Cleanup != CleanupComplete {
				t.Fatalf("typed method result=%+v err=%v", result, err)
			}
		})
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.executed) != len(methods) {
		t.Fatalf("Execute count=%d want=%d", len(backend.executed), len(methods))
	}
}

func TestDeveloperActionBoundsMixedInputsAndClosedQualification(t *testing.T) {
	session, backend := newRawTestSession(t, []string{"click", "future_action"}, nil)
	invalid := []func() (ActionResult, error){
		func() (ActionResult, error) {
			return session.Click(context.Background(), ClickParams{WindowTarget: WindowTarget{"bad-point", "window-1"}, Point: Point{X: -1}, Button: "left", Count: 1})
		},
		func() (ActionResult, error) {
			return session.TypeText(context.Background(), TypeTextParams{WindowTarget: WindowTarget{"bad-utf8", "window-1"}, Text: string([]byte{0xff})})
		},
		func() (ActionResult, error) {
			return session.PressKey(context.Background(), PressKeyParams{WindowTarget: WindowTarget{"bad-keys", "window-1"}, Keys: []string{"ctrl", "launch"}})
		},
		func() (ActionResult, error) {
			return session.Scroll(context.Background(), ScrollParams{WindowTarget: WindowTarget{"bad-scroll", "window-1"}, DX: 11})
		},
		func() (ActionResult, error) {
			return session.Drag(context.Background(), DragParams{WindowTarget: WindowTarget{"bad-drag", "window-1"}, Steps: 1, DurationMS: 1})
		},
	}
	for _, call := range invalid {
		if _, err := call(); ErrorCode(err) != "invalid_request" {
			t.Fatalf("invalid typed request err=%v", err)
		}
	}
	valid := ClickParams{WindowTarget: WindowTarget{"unqualified", "window-1"}, Point: Point{}, Button: "left", Count: 1}
	if _, err := session.PressKey(context.Background(), PressKeyParams{WindowTarget: valid.WindowTarget, Keys: []string{"a"}}); ErrorCode(err) != "unsupported" {
		t.Fatalf("open/unknown capability list accepted: %v", err)
	}
	mixed := Request{Operation: OperationClick, Click: &valid, TypeText: &TypeTextParams{}}
	if _, err := session.Call(context.Background(), mixed); ErrorCode(err) != "invalid_request" {
		t.Fatalf("mixed raw/semantic request err=%v", err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.executed) != 0 {
		t.Fatalf("invalid or unqualified calls reached Execute: %d", len(backend.executed))
	}
}

func TestDeveloperActionAdmissionReplayApprovalCancelAndUnknown(t *testing.T) {
	t.Run("quota refusal replays before fresh window read", func(t *testing.T) {
		session, backend := newRawTestSession(t, allRawKinds(), nil)
		quotaCalls := 0
		session.reserveQuota = func(context.Context, [32]byte) error { quotaCalls++; return writer.ErrQuotaExhausted }
		p := ClickParams{WindowTarget: WindowTarget{"quota-raw", "window-1"}, Point: Point{}, Button: "left", Count: 1}
		first, err := session.Click(context.Background(), p)
		if ErrorCode(err) != "rate_limited" || first.Execution != ExecutionNotApplied {
			t.Fatalf("first=%+v err=%v", first, err)
		}
		backend.windowsErr = errors.New("fresh read must not run on replay")
		before := backend.windowsCalls
		second, replayErr := session.Click(context.Background(), p)
		if ErrorCode(replayErr) != "rate_limited" || second.Execution != first.Execution || backend.windowsCalls != before || quotaCalls != 1 {
			t.Fatalf("replay=%+v err=%v windows=%d/%d quota=%d", second, replayErr, backend.windowsCalls, before, quotaCalls)
		}
	})
	t.Run("approval refusal never executes", func(t *testing.T) {
		session, backend := newRawTestSession(t, allRawKinds(), fixedApproval{err: coreError("approval_required")})
		_, err := session.FocusWindow(context.Background(), WindowTarget{"no-approval", "window-1"})
		if ErrorCode(err) != "approval_required" {
			t.Fatalf("approval error=%v", err)
		}
		backend.mu.Lock()
		defer backend.mu.Unlock()
		if len(backend.executed) != 0 {
			t.Fatal("approval refusal reached Execute")
		}
	})
	t.Run("cancel before admission never executes", func(t *testing.T) {
		session, backend := newRawTestSession(t, allRawKinds(), nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := session.TypeText(ctx, TypeTextParams{WindowTarget: WindowTarget{"cancelled", "window-1"}, Text: "safe"})
		if ErrorCode(err) != "cancelled" {
			t.Fatalf("cancel error=%v", err)
		}
		backend.mu.Lock()
		defer backend.mu.Unlock()
		if len(backend.executed) != 0 {
			t.Fatal("cancelled call reached Execute")
		}
	})
	t.Run("uncertain native result remains replayed", func(t *testing.T) {
		session, backend := newRawTestSession(t, allRawKinds(), nil)
		backend.executeHook = func() (ActionResult, error) {
			return ActionResult{ActionID: "uncertain-raw", Execution: ExecutionUnknown, Verification: Verification{Status: VerificationUnavailable}, Cleanup: CleanupUnknown}, nil
		}
		p := DragParams{WindowTarget: WindowTarget{"uncertain-raw", "window-1"}, Start: Point{}, End: Point{X: 2, Y: 2}, Steps: 2, DurationMS: 10}
		result, err := session.Drag(context.Background(), p)
		if ErrorCode(err) != "unknown_outcome" || result.Execution != ExecutionUnknown {
			t.Fatalf("uncertain result=%+v err=%v", result, err)
		}
		before := backend.windowsCalls
		retry, retryErr := session.Drag(context.Background(), p)
		if ErrorCode(retryErr) != "budget_exceeded" || backend.windowsCalls != before || len(backend.executed) != 1 {
			t.Fatalf("uncertain retry=%+v err=%v windows=%d/%d executions=%d", retry, retryErr, backend.windowsCalls, before, len(backend.executed))
		}
	})
}

func TestCallRawActionCountersCountAdmissionOnceAndExcludeReplay(t *testing.T) {
	session, backend := newRawTestSession(t, allRawKinds(), nil)
	session.reserveQuota = func(context.Context, [32]byte) error { return writer.ErrQuotaExhausted }
	request := Request{Operation: OperationClick, Click: &ClickParams{
		WindowTarget: WindowTarget{ActionID: "counter-raw", WindowRef: "window-1"},
		Point:        Point{}, Button: "left", Count: 1,
	}}
	first, err := session.Call(context.Background(), request)
	if ErrorCode(err) != "rate_limited" || first.Usage.Actions != 1 {
		t.Fatalf("admission envelope=%+v err=%v", first, err)
	}
	before := backend.windowsCalls
	replay, replayErr := session.Call(context.Background(), request)
	if ErrorCode(replayErr) != "rate_limited" || replay.Usage.Actions != 0 || backend.windowsCalls != before {
		t.Fatalf("replay envelope=%+v err=%v window calls=%d/%d", replay, replayErr, backend.windowsCalls, before)
	}
}

func TestCallRawActionCancellationAfterAdmissionIsCountedWithoutExecute(t *testing.T) {
	started := make(chan struct{})
	approval := blockingApproval{started: started, release: make(chan struct{})}
	session, backend := newRawTestSession(t, allRawKinds(), approval)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan struct {
		envelope ResultEnvelope
		err      error
	}, 1)
	go func() {
		envelope, err := session.Call(ctx, Request{Operation: OperationTypeText, TypeText: &TypeTextParams{
			WindowTarget: WindowTarget{ActionID: "cancel-admitted", WindowRef: "window-1"}, Text: "bounded",
		}})
		result <- struct {
			envelope ResultEnvelope
			err      error
		}{envelope, err}
	}()
	<-started
	cancel()
	call := <-result
	if ErrorCode(call.err) != "cancelled" || call.envelope.Usage.Actions != 1 {
		t.Fatalf("cancelled admission envelope=%+v err=%v", call.envelope, call.err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.executed) != 0 {
		t.Fatal("cancelled admitted call reached Execute")
	}
}

func TestDeveloperActionFreshScopeAndNoRawModelAdvertisement(t *testing.T) {
	session, backend := newRawTestSession(t, allRawKinds(), nil)
	if _, err := session.Click(context.Background(), ClickParams{WindowTarget: WindowTarget{"out-of-scope", "outside"}, Point: Point{}, Button: "left", Count: 1}); ErrorCode(err) != "element_stale" {
		t.Fatalf("out-of-scope window err=%v", err)
	}
	operations, err := session.SemanticOperations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(operations, OperationClick) || slices.Contains(operations, OperationTypeText) || slices.Contains(operations, OperationPressKey) || slices.Contains(operations, OperationScroll) || slices.Contains(operations, OperationDrag) || slices.Contains(operations, OperationFocusWindow) {
		t.Fatalf("developer operations were advertised as semantic: %v", operations)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.executed) != 0 {
		t.Fatal("out-of-scope target reached Execute")
	}
}

type deadlineRawTestBackend struct {
	*rawTestBackend
	remaining time.Duration
}

func (b *deadlineRawTestBackend) Execute(ctx context.Context, action Action) (ActionResult, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return ActionResult{}, coreError("backend_unavailable")
	}
	b.remaining = time.Until(deadline)
	return b.rawTestBackend.Execute(ctx, action)
}
func TestDeveloperAdmissionDeadlineIsBoundedBeforeBackend(t *testing.T) {
	s, base := newRawTestSession(t, allRawKinds(), nil)
	b := &deadlineRawTestBackend{rawTestBackend: base}
	s.backend = b
	s.budget.Timeout = 30 * time.Second
	_, err := s.Click(context.Background(), ClickParams{WindowTarget: WindowTarget{"deadline-click", "window-1"}, Point: Point{X: 1, Y: 1}, Button: "left", Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	if b.remaining <= 0 || b.remaining > 10*time.Second {
		t.Fatalf("native context deadline=%v", b.remaining)
	}
}
