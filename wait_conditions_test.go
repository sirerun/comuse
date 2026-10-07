package comuse

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type semanticBooleanWaitBackend struct{ *fakeBackend }

func (b *semanticBooleanWaitBackend) Observe(ctx context.Context, windowRef string, budget Budget) (Observation, error) {
	observation, err := b.fakeBackend.Observe(ctx, windowRef, budget)
	if err != nil {
		return observation, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range observation.Elements {
		for _, source := range b.elements {
			if source.Ref == observation.Elements[i].Ref {
				observation.Elements[i].Checked = cloneBool(source.Checked)
				observation.Elements[i].Selected = cloneBool(source.Selected)
				break
			}
		}
	}
	return observation, nil
}

type cancellableWaitBackend struct {
	*fakeBackend
	mu            sync.Mutex
	blockNextRead bool
	readStarted   chan struct{}
}

func (b *cancellableWaitBackend) Observe(ctx context.Context, windowRef string, budget Budget) (Observation, error) {
	b.mu.Lock()
	block := b.blockNextRead
	b.blockNextRead = false
	started := b.readStarted
	b.mu.Unlock()
	if block {
		close(started)
		<-ctx.Done()
		return Observation{}, ctx.Err()
	}
	return (&semanticBooleanWaitBackend{fakeBackend: b.fakeBackend}).Observe(ctx, windowRef, budget)
}

func (b *cancellableWaitBackend) blockNextObservation() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blockNextRead = true
	b.readStarted = make(chan struct{})
	return b.readStarted
}

func TestWaitConditionUsesFreshStateAndReturnsCanonicalMetadata(t *testing.T) {
	enabled, checked := true, false
	base := &fakeBackend{process: testProcess(), nativeState: "wait-state", elements: []Element{{Ref: "normal-1", Role: "AXCheckBox", Classification: "normal", Enabled: &enabled, Checked: &checked}}}
	backend := &semanticBooleanWaitBackend{fakeBackend: base}
	session := newWaitTestSession(t, backend, testProcess())
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	prior, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	before := session.ledger.Snapshot(session.sessionID)

	result, err := session.WaitCondition(context.Background(), WaitParams{
		Condition: "element_enabled", WindowRef: "window-1", ElementRef: "normal-1", StateID: prior.StateID,
		Expected: boolPointer(true), TimeoutMS: 500,
	})
	if err != nil || !result.Satisfied || result.Reason != "satisfied" || result.Condition != "element_enabled" || result.FinalStateID == nil || *result.FinalStateID != prior.StateID || result.WindowRef == nil || *result.WindowRef != "window-1" {
		t.Fatalf("WaitCondition = (%+v, %v)", result, err)
	}
	after := session.ledger.Snapshot(session.sessionID)
	if after.Observations.State-before.Observations.State != 1 || after.Observations.A11y-before.Observations.A11y != 1 {
		t.Fatalf("wait counters state/a11y delta = %d/%d, want 1/1", after.Observations.State-before.Observations.State, after.Observations.A11y-before.Observations.A11y)
	}
	checkedResult, err := session.WaitCondition(context.Background(), WaitParams{
		Condition: "element_checked", WindowRef: "window-1", ElementRef: "normal-1", StateID: prior.StateID,
		Expected: boolPointer(false), TimeoutMS: 500,
	})
	if err != nil || !checkedResult.Satisfied || checkedResult.Reason != "satisfied" {
		t.Fatalf("WaitCondition checked=false = (%+v, %v)", checkedResult, err)
	}
	payload, err := projectResult(result)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := payload.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "observed_at") {
		t.Fatal("frozen wait DTO must not fabricate an observed_at field")
	}
	var metadata MetadataWaitResult
	if err := json.Unmarshal(encoded, &metadata); err != nil {
		t.Fatal(err)
	}
	if _, err := NewResultPayload(metadata); err != nil || !metadata.Satisfied || metadata.Condition != "element_enabled" || metadata.FinalStateID == nil || metadata.WindowRef == nil {
		t.Fatalf("wait metadata = (%+v, %v)", metadata, err)
	}
}

type changingBooleanWaitBackend struct {
	*fakeBackend
	mu2            sync.Mutex
	pollObserves   int
	firstPollReady chan struct{}
}

func (b *changingBooleanWaitBackend) Observe(ctx context.Context, windowRef string, budget Budget) (Observation, error) {
	observation, err := (&semanticBooleanWaitBackend{fakeBackend: b.fakeBackend}).Observe(ctx, windowRef, budget)
	if err != nil {
		return observation, err
	}
	b.mu2.Lock()
	b.pollObserves++
	if b.pollObserves == 2 {
		close(b.firstPollReady)
	}
	b.mu2.Unlock()
	return observation, nil
}

func TestWaitConditionObservesCheckedTransitionAcrossFreshStateIDs(t *testing.T) {
	checked := false
	base := &fakeBackend{process: testProcess(), nativeState: "wait-state", elements: []Element{{Ref: "normal-1", Role: "AXCheckBox", Classification: "normal", Checked: &checked}}}
	backend := &changingBooleanWaitBackend{fakeBackend: base, firstPollReady: make(chan struct{})}
	session := newWaitTestSession(t, backend, testProcess())
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct {
		result WaitResult
		err    error
	}, 1)
	go func() {
		result, waitErr := session.WaitCondition(context.Background(), WaitParams{
			Condition: "element_checked", WindowRef: "window-1", ElementRef: "normal-1", StateID: initial.StateID,
			Expected: boolPointer(true), TimeoutMS: 900,
		})
		done <- struct {
			result WaitResult
			err    error
		}{result: result, err: waitErr}
	}()
	select {
	case <-backend.firstPollReady:
	case <-time.After(time.Second):
		t.Fatal("wait did not perform its first fresh element read")
	}
	base.mu.Lock()
	base.elements[0].Checked = boolPointer(true)
	base.mu.Unlock()
	select {
	case outcome := <-done:
		if outcome.err != nil || !outcome.result.Satisfied || outcome.result.Reason != "satisfied" || outcome.result.FinalStateID == nil || *outcome.result.FinalStateID == initial.StateID {
			t.Fatalf("transition wait = (%+v, %v), initial state %s", outcome.result, outcome.err, initial.StateID)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not observe checked transition")
	}
}

func TestWaitConditionObservesEnabledTransitionAcrossFreshStateIDs(t *testing.T) {
	enabled := false
	base := &fakeBackend{process: testProcess(), nativeState: "wait-state", elements: []Element{{Ref: "normal-1", Role: "AXTextField", Classification: "normal", Enabled: &enabled}}}
	backend := &changingBooleanWaitBackend{fakeBackend: base, firstPollReady: make(chan struct{})}
	session := newWaitTestSession(t, backend, testProcess())
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct {
		result WaitResult
		err    error
	}, 1)
	go func() {
		result, waitErr := session.WaitCondition(context.Background(), WaitParams{
			Condition: "element_enabled", WindowRef: "window-1", ElementRef: "normal-1", StateID: initial.StateID,
			Expected: boolPointer(true), TimeoutMS: 900,
		})
		done <- struct {
			result WaitResult
			err    error
		}{result: result, err: waitErr}
	}()
	select {
	case <-backend.firstPollReady:
	case <-time.After(time.Second):
		t.Fatal("wait did not perform its first fresh element read")
	}
	base.mu.Lock()
	base.elements[0].Enabled = boolPointer(true)
	base.mu.Unlock()
	select {
	case outcome := <-done:
		if outcome.err != nil || !outcome.result.Satisfied || outcome.result.Reason != "satisfied" || outcome.result.FinalStateID == nil || *outcome.result.FinalStateID == initial.StateID {
			t.Fatalf("transition wait = (%+v, %v), initial state %s", outcome.result, outcome.err, initial.StateID)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not observe enabled transition")
	}
}

func TestWaitConditionDoesNotInventMissingBooleanAndPreservesSelected(t *testing.T) {
	checked, selected := false, true
	session := &Session{budget: testBudget(), sessionID: "wait-projection", now: time.Now}
	observation, _, err := session.normalizeObservation("window-1", Observation{
		WindowRef: "window-1", StateID: "native", Coverage: Coverage{Complete: true},
		Elements: []Element{
			{Ref: "known", Role: "AXToggle", Classification: "normal", Checked: &checked, Selected: &selected},
			{Ref: "unknown", Role: "AXToggle", Classification: "normal"},
			{Ref: "secure", Role: "AXTextField", Classification: "secure", Checked: &checked, Selected: &selected},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	known := findNormalElement(observation, "known")
	unknown := findNormalElement(observation, "unknown")
	if known == nil || known.Checked == nil || *known.Checked || known.Selected == nil || !*known.Selected {
		t.Fatalf("nullable booleans lost in projection: %+v", known)
	}
	if unknown == nil || unknown.Checked != nil || unknown.Selected != nil || findNormalElement(observation, "secure") != nil {
		t.Fatalf("missing/protected booleans were fabricated or exposed: %+v", observation.Elements)
	}
	copy := cloneObservation(observation)
	*copy.Elements[0].Checked = true
	if *observation.Elements[0].Checked {
		t.Fatal("clone shares the checked pointer with retained observation")
	}
}

func TestReadElementRequiresFreshNormalClassification(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "read-state", elements: testElements()}
	session := newTestSession(t, backend, false)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	prior, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ReadElement(context.Background(), "window-1", "secure-1", prior.StateID); ErrorCode(err) != "element_stale" {
		t.Fatalf("protected explicit read error = %v, want element_stale", err)
	}
	if backend.readState != "" {
		t.Fatalf("protected explicit read reached backend: %q", backend.readState)
	}
}

func TestWaitConditionRejectsExpiredElementIdentityBeforeFreshRead(t *testing.T) {
	enabled := true
	backend := &fakeBackend{process: testProcess(), nativeState: "wait-state", elements: []Element{{Ref: "normal-1", Role: "AXCheckBox", Classification: "normal", Enabled: &enabled}}}
	session := newTestSession(t, backend, false)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	prior, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	before := session.ledger.Snapshot(session.sessionID)
	session.now = func() time.Time { return time.Now().Add(snapshotTTL + time.Second) }
	_, err = session.WaitCondition(context.Background(), WaitParams{Condition: "element_exists", WindowRef: "window-1", ElementRef: "normal-1", StateID: prior.StateID, TimeoutMS: 100})
	if ErrorCode(err) != "state_expired" {
		t.Fatalf("expired element identity error = %v, want state_expired", err)
	}
	after := session.ledger.Snapshot(session.sessionID)
	if after.Observations.State != before.Observations.State || after.Observations.A11y != before.Observations.A11y {
		t.Fatalf("expired identity caused fresh reads: before=%+v after=%+v", before.Observations, after.Observations)
	}
}

func TestWaitConditionRejectsReusedRoleAndPermissionEpochChange(t *testing.T) {
	checked := false
	base := &fakeBackend{process: testProcess(), nativeState: "wait-state", elements: []Element{{Ref: "normal-1", Role: "AXCheckBox", Classification: "normal", Checked: &checked}}}
	backend := &semanticBooleanWaitBackend{fakeBackend: base}
	session := newWaitTestSession(t, backend, testProcess())
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	prior, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	base.mu.Lock()
	base.elements[0].Role = "AXTextField"
	base.mu.Unlock()
	_, err = session.WaitCondition(context.Background(), WaitParams{
		Condition: "element_checked", WindowRef: "window-1", ElementRef: "normal-1", StateID: prior.StateID,
		Expected: boolPointer(true), TimeoutMS: 100,
	})
	if ErrorCode(err) != "element_stale" {
		t.Fatalf("reused element role error = %v, want element_stale", err)
	}

	epochBackend := &permissionEpochWaitBackend{fakeBackend: &fakeBackend{process: testProcess(), nativeState: "wait-state"}}
	epochSession := newWaitTestSession(t, epochBackend, testProcess())
	t.Cleanup(func() { _ = epochSession.Close(context.Background()) })
	if _, err := epochSession.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	epochBackend.onNextWindows = epochSession.invalidateSemanticState
	result, err := epochSession.WaitCondition(context.Background(), WaitParams{Condition: "window_closed", WindowRef: "window-1", TimeoutMS: 100})
	if ErrorCode(err) != "permission_denied" || result.Satisfied {
		t.Fatalf("permission epoch change = (%+v, %v), want permission_denied", result, err)
	}
}

type permissionEpochWaitBackend struct {
	*fakeBackend
	onNextWindows func()
}

func (b *permissionEpochWaitBackend) Windows(ctx context.Context, budget Budget) ([]Window, error) {
	if b.onNextWindows != nil {
		change := b.onNextWindows
		b.onNextWindows = nil
		change()
	}
	return b.fakeBackend.Windows(ctx, budget)
}

func TestWindowClosedRejectsScopeExpiryDuringEnumeration(t *testing.T) {
	backend := &expireScopeWaitBackend{fakeBackend: &fakeBackend{process: testProcess(), nativeState: "wait-state"}}
	session := newWaitTestSession(t, backend, testProcess())
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	backend.expire = func() {
		session.mu.Lock()
		session.scope.ExpiresAt = time.Now().Add(-time.Second)
		session.mu.Unlock()
	}
	result, err := session.WaitCondition(context.Background(), WaitParams{Condition: "window_closed", WindowRef: "window-1", TimeoutMS: 100})
	if ErrorCode(err) != "state_expired" || result.Satisfied {
		t.Fatalf("scope expiry during enumeration = (%+v, %v), want state_expired", result, err)
	}
}

type expireScopeWaitBackend struct {
	*fakeBackend
	expire func()
}

func (b *expireScopeWaitBackend) Windows(ctx context.Context, budget Budget) ([]Window, error) {
	if b.expire != nil {
		change := b.expire
		b.expire = nil
		change()
		return []Window{}, nil
	}
	return b.fakeBackend.Windows(ctx, budget)
}

func TestWindowClosedRequiresSuccessfulFreshEnumeration(t *testing.T) {
	backend := &disappearingWindowWaitBackend{fakeBackend: &fakeBackend{process: testProcess(), nativeState: "wait-state"}}
	session := newWaitTestSession(t, backend, testProcess())
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	backend.disappeared = true
	result, err := session.WaitCondition(context.Background(), WaitParams{Condition: "window_closed", WindowRef: "window-1", TimeoutMS: 100})
	if err != nil || !result.Satisfied || result.Reason != "satisfied" || result.WindowRef == nil || *result.WindowRef != "window-1" {
		t.Fatalf("closed window wait = (%+v, %v)", result, err)
	}
}

type disappearingWindowWaitBackend struct {
	*fakeBackend
	disappeared bool
}

func (b *disappearingWindowWaitBackend) Windows(ctx context.Context, budget Budget) ([]Window, error) {
	if b.disappeared {
		return []Window{}, nil
	}
	return b.fakeBackend.Windows(ctx, budget)
}

func TestWaitConditionRejectsForeignProcessRefAndExpiredScope(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "wait-state"}
	session := newTestSession(t, backend, false)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	foreign := newTestSession(t, &fakeBackend{process: testProcess()}, false)
	t.Cleanup(func() {
		if err := foreign.Close(context.Background()); err != nil {
			t.Errorf("close foreign session: %v", err)
		}
	})
	foreignRef, err := foreign.ProcessRef(testProcess())
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.WaitCondition(context.Background(), WaitParams{Condition: "window_appears", ProcessRef: foreignRef, TimeoutMS: 100})
	if ErrorCode(err) != "policy_refused" {
		t.Fatalf("foreign process reference error = %v, want policy_refused", err)
	}
	if got := backend.doctorCalls; got != 0 {
		t.Fatalf("foreign ref caused native doctor call: %d", got)
	}
	localRef, err := session.ProcessRef(testProcess())
	if err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	session.scope.ExpiresAt = time.Now().Add(-time.Second)
	session.mu.Unlock()
	_, err = session.WaitCondition(context.Background(), WaitParams{Condition: "window_appears", ProcessRef: localRef, TimeoutMS: 100})
	if ErrorCode(err) != "state_expired" {
		t.Fatalf("expired process identity error = %v, want state_expired", err)
	}
}

func TestWaitConditionCancellationCountsAttemptedFreshRead(t *testing.T) {
	enabled := true
	base := &fakeBackend{process: testProcess(), nativeState: "wait-state", elements: []Element{{Ref: "normal-1", Role: "AXCheckBox", Classification: "normal", Enabled: &enabled}}}
	backend := &cancellableWaitBackend{fakeBackend: base}
	session := newWaitTestSession(t, backend, testProcess())
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	prior, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	before := session.ledger.Snapshot(session.sessionID)
	started := backend.blockNextObservation()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		result WaitResult
		err    error
	}, 1)
	go func() {
		result, waitErr := session.WaitCondition(ctx, WaitParams{Condition: "element_enabled", WindowRef: "window-1", ElementRef: "normal-1", StateID: prior.StateID, Expected: boolPointer(false), TimeoutMS: 900})
		done <- struct {
			result WaitResult
			err    error
		}{result: result, err: waitErr}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("wait did not reach its fresh observation")
	}
	cancel()
	select {
	case outcome := <-done:
		if ErrorCode(outcome.err) != "cancelled" || outcome.result.Reason != "cancelled" || outcome.result.Satisfied {
			t.Fatalf("cancelled wait = (%+v, %v)", outcome.result, outcome.err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not drain after cancellation")
	}
	after := session.ledger.Snapshot(session.sessionID)
	if after.Observations.State-before.Observations.State != 1 || after.Observations.A11y-before.Observations.A11y != 1 {
		t.Fatalf("cancel counters state/a11y delta = %d/%d, want 1/1", after.Observations.State-before.Observations.State, after.Observations.A11y-before.Observations.A11y)
	}
}

func TestWaitConditionTimeoutAndAmbiguousWindowAreTruthful(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "wait-state"}
	session := newTestSession(t, backend, false)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	processRef, err := session.ProcessRef(testProcess())
	if err != nil {
		t.Fatal(err)
	}
	appeared, err := session.WaitCondition(context.Background(), WaitParams{Condition: "window_appears", ProcessRef: processRef, Title: "Fixture window", TimeoutMS: 100})
	if err != nil || !appeared.Satisfied || appeared.Reason != "satisfied" || appeared.WindowRef == nil || *appeared.WindowRef != "window-1" {
		t.Fatalf("unique appearance = (%+v, %v)", appeared, err)
	}
	result, err := session.WaitCondition(context.Background(), WaitParams{Condition: "window_appears", ProcessRef: processRef, Title: "not present", TimeoutMS: 75, PollIntervalMS: 50})
	if ErrorCode(err) != "budget_exceeded" || result.Satisfied || result.Reason != "timeout" || result.WindowRef != nil || result.ElapsedMS == 0 {
		t.Fatalf("timeout = (%+v, %v)", result, err)
	}

	resourceBackend := &budgetErrorWaitBackend{fakeBackend: backend}
	resourceSession := newWaitTestSession(t, resourceBackend, testProcess())
	t.Cleanup(func() { _ = resourceSession.Close(context.Background()) })
	resourceRef, err := resourceSession.ProcessRef(testProcess())
	if err != nil {
		t.Fatal(err)
	}
	resource, err := resourceSession.WaitCondition(context.Background(), WaitParams{Condition: "window_appears", ProcessRef: resourceRef, TimeoutMS: 100})
	if ErrorCode(err) != "budget_exceeded" || resource.Reason != "unavailable" || resource.Satisfied {
		t.Fatalf("non-deadline budget refusal = (%+v, %v)", resource, err)
	}

	ambiguousBackend := &multiWindowWaitBackend{fakeBackend: backend}
	ambiguousSession := newWaitTestSession(t, ambiguousBackend, testProcess())
	t.Cleanup(func() { _ = ambiguousSession.Close(context.Background()) })
	ambiguousRef, err := ambiguousSession.ProcessRef(testProcess())
	if err != nil {
		t.Fatal(err)
	}
	ambiguous, err := ambiguousSession.WaitCondition(context.Background(), WaitParams{Condition: "window_appears", ProcessRef: ambiguousRef, TimeoutMS: 100})
	if ErrorCode(err) != "unsupported" || ambiguous.Satisfied || ambiguous.Reason != "ambiguous" || ambiguous.WindowRef != nil {
		t.Fatalf("ambiguous wait = (%+v, %v)", ambiguous, err)
	}
}

type budgetErrorWaitBackend struct{ *fakeBackend }

func (b *budgetErrorWaitBackend) Windows(context.Context, Budget) ([]Window, error) {
	return nil, coreError("budget_exceeded")
}

type multiWindowWaitBackend struct{ *fakeBackend }

func (b *multiWindowWaitBackend) Windows(ctx context.Context, budget Budget) ([]Window, error) {
	windows, err := b.fakeBackend.Windows(ctx, budget)
	if err != nil {
		return nil, err
	}
	window := windows[0]
	window.Ref = "window-2"
	return append(windows, window), nil
}

func newWaitTestSession(t *testing.T, backend Backend, process ProcessIdentity) *Session {
	t.Helper()
	session, err := newSyntheticSession(Config{Backend: backend, Scope: Scope{Processes: []ProcessIdentity{process}, ExpiresAt: time.Now().Add(time.Hour)}, Budget: testBudget()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func boolPointer(value bool) *bool { return &value }
