package comuse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirerun/comuse/internal/writer"
)

type fakeBackend struct {
	mu          sync.Mutex
	process     ProcessIdentity
	nativeState string
	elements    []Element
	input       bool
	qualified   bool
	partial     bool
	readState   string
	executed    []Action
	closeErrors []error
	executeHook func() (ActionResult, error)
	doctorCalls int
	closeCalls  int
}

func (f *fakeBackend) Doctor(context.Context) (DoctorReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doctorCalls++
	return DoctorReport{Capabilities: Capabilities{Accessibility: true, Input: f.input, QualifiedInput: f.qualified}, Permissions: map[string]string{"accessibility": "granted", "private-native-key": "secret"}}, nil
}

func (f *fakeBackend) Windows(context.Context, Budget) ([]Window, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []Window{{Ref: "window-1", Process: f.process, Title: "Fixture window"}, {Ref: "outside", Process: ProcessIdentity{PID: 991, BundleID: "elsewhere", LaunchID: "launch-x"}, Title: "outside"}}, nil
}

func (f *fakeBackend) Observe(_ context.Context, windowRef string, _ Budget) (Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	coverage := Coverage{Complete: !f.partial}
	if f.partial {
		coverage.Reason = "private native coverage detail"
	}
	return Observation{WindowRef: windowRef, StateID: f.nativeState, ObservedAt: time.Now(), Elements: cloneElements(f.elements), Coverage: coverage}, nil
}

func (f *fakeBackend) ReadElement(_ context.Context, windowRef, elementRef, stateID string, _ Budget) (ElementContent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readState = stateID
	return ElementContent{WindowRef: windowRef, ElementRef: elementRef, StateID: stateID, Text: "explicit value"}, nil
}

func (f *fakeBackend) Execute(_ context.Context, action Action) (ActionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executed = append(f.executed, action)
	if f.executeHook != nil {
		return f.executeHook()
	}
	return ActionResult{ActionID: action.ID, Execution: ExecutionApplied, Verification: Verification{Status: VerificationVerified, Reason: "postcondition_met"}, StateStatus: StateAvailable, Cleanup: CleanupComplete}, nil
}

func (f *fakeBackend) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	if len(f.closeErrors) == 0 {
		return nil
	}
	err := f.closeErrors[0]
	f.closeErrors = f.closeErrors[1:]
	return err
}

func cloneElements(elements []Element) []Element {
	copyElements := make([]Element, len(elements))
	for i, element := range elements {
		copyElements[i] = element
		copyElements[i].Value = cloneString(element.Value)
		copyElements[i].Enabled = cloneBool(element.Enabled)
		copyElements[i].Actions = append([]string(nil), element.Actions...)
	}
	return copyElements
}

type fixedApproval struct{ err error }

func (approval fixedApproval) Approve(_ context.Context, request ApprovalRequest) (Approval, error) {
	if approval.err != nil {
		return Approval{}, approval.err
	}
	return Approval{SessionID: request.SessionID, Action: request.Action, Process: request.Process, ObservedAt: request.ObservedAt, PolicyVersion: request.PolicyVersion, ExpiresAt: request.ExpiresAt}, nil
}

func testProcess() ProcessIdentity {
	return ProcessIdentity{PID: 41, BundleID: "com.example.fixture", LaunchID: "start-41"}
}

func testBudget() Budget {
	return Budget{MaxDepth: 16, MaxNodes: 256, MaxBytes: 64 * 1024, Timeout: time.Second}
}

func testElements() []Element {
	enabled := true
	normalValue := "ordinary private value"
	secureValue := "secure hidden value"
	return []Element{
		{Ref: "normal-1", Role: "AXTextField", Label: "Name", Value: &normalValue, Enabled: &enabled, Actions: []string{ActionReplace, ActionPress, ActionReplace}, Classification: "normal"},
		{Ref: "secure-1", Role: "AXTextField", Label: "Password", Value: &secureValue, Enabled: &enabled, Actions: []string{ActionReplace}, Classification: "secure"},
		{Ref: "child-secret", ParentRef: "secure-1", Role: "AXStaticText", Label: "hidden child", Classification: "normal"},
		{Ref: "unknown-1", Role: "AXTextField", Label: "unknown", Classification: "future-class"},
	}
}

func newTestSession(t *testing.T, backend *fakeBackend, mutation bool) *Session {
	t.Helper()
	config := Config{Backend: backend, Scope: Scope{Processes: []ProcessIdentity{backend.process}, ExpiresAt: time.Now().Add(time.Hour)}, Budget: testBudget()}
	if mutation {
		config.ApprovalProvider = fixedApproval{}
		config.WriterDirectory = filepath.Join(t.TempDir(), "state")
		config.WriterKey = []byte(strings.Repeat("k", 32))
		config.MaxActions = 2
	}
	session, err := NewSession(config)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func TestNewSessionReadonlyCopiesScopeAndCallsNoBackend(t *testing.T) {
	process := testProcess()
	backend := &fakeBackend{process: process, nativeState: "native-a", elements: testElements()}
	config := Config{Backend: backend, Scope: Scope{Processes: []ProcessIdentity{process}, ExpiresAt: time.Now().Add(time.Hour)}, Budget: testBudget()}
	session, err := NewSession(config)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	config.Scope.Processes[0].PID = 999
	if backend.doctorCalls != 0 {
		t.Fatalf("constructor made native calls: %d", backend.doctorCalls)
	}
	windows, err := session.Windows(context.Background())
	if err != nil {
		t.Fatalf("Windows: %v", err)
	}
	if len(windows) != 1 || windows[0].Process != process {
		t.Fatalf("scope was not copied/enforced: %#v", windows)
	}
	if _, err := session.Do(context.Background(), Action{ID: "action-1", WindowRef: "window-1", ElementRef: "normal-1", StateID: "state", Kind: ActionPress}); ErrorCode(err) != "approval_required" {
		t.Fatalf("readonly session allowed action: %v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestObservationRedactsBeforeHashAndRetainsPrivateNativeBinding(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-a", elements: testElements()}
	session := newTestSession(t, backend, false)
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Elements) != 1 || first.Elements[0].Ref != "normal-1" || first.Elements[0].Value != nil {
		t.Fatalf("protected/unrequested values escaped projection: %#v", first.Elements)
	}
	if strings.Contains(string(mustJSON(t, first)), "native-a") || strings.Contains(string(mustJSON(t, first)), "secure hidden value") {
		t.Fatal("native state or protected value escaped public observation")
	}
	first.Elements[0].Label = "caller mutation"
	backend.mu.Lock()
	backend.nativeState = "native-b"
	backend.mu.Unlock()
	second, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.StateID != second.StateID {
		t.Fatalf("hidden native state changed public hash: %q != %q", first.StateID, second.StateID)
	}
	if second.Elements[0].Label != "Name" {
		t.Fatal("caller mutation changed cached observation")
	}
	content, err := session.ReadElement(context.Background(), "window-1", "normal-1", second.StateID)
	if err != nil {
		t.Fatal(err)
	}
	if content.StateID != second.StateID || backend.readState != "native-b" {
		t.Fatalf("read did not use private native binding and public ID: content=%#v native=%q", content, backend.readState)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestActionApprovalDurableReplayAndNoTextPersistence(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-action", elements: testElements(), input: true, qualified: true}
	session := newTestSession(t, backend, true)
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	action := Action{ID: "action-once", WindowRef: "window-1", ElementRef: "normal-1", StateID: state.StateID, Kind: ActionReplace, Text: "private action text"}
	first, err := session.Do(context.Background(), action)
	if err != nil || first.Execution != ExecutionApplied {
		t.Fatalf("Do: result=%#v err=%v", first, err)
	}
	second, err := session.Do(context.Background(), action)
	if err != nil || second.Execution != ExecutionApplied {
		t.Fatalf("replay: result=%#v err=%v", second, err)
	}
	if len(backend.executed) != 1 || backend.executed[0].StateID != "native-action" {
		t.Fatalf("native action was redispatched or had public state ID: %#v", backend.executed)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(session.writerDirectory, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "private action text") {
			t.Errorf("journal persisted action text in %s", filepath.Base(path))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownActionRetainsInflightWhenJournalWritesFail(t *testing.T) {
	process := testProcess()
	root := filepath.Join(t.TempDir(), "state")
	backend := &fakeBackend{process: process, nativeState: "native-action", elements: testElements(), input: true, qualified: true}
	backend.executeHook = func() (ActionResult, error) {
		if err := os.Chmod(root, 0500); err != nil {
			return ActionResult{}, err
		}
		return ActionResult{}, errors.New("private native failure")
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0700) })
	config := Config{Backend: backend, Scope: Scope{Processes: []ProcessIdentity{process}, ExpiresAt: time.Now().Add(time.Hour)}, Budget: testBudget(), ApprovalProvider: fixedApproval{}, WriterDirectory: root, WriterKey: []byte(strings.Repeat("k", 32)), MaxActions: 2}
	session, err := NewSession(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	action := Action{ID: "action-uncertain", WindowRef: "window-1", ElementRef: "normal-1", StateID: state.StateID, Kind: ActionPress}
	result, err := session.Do(context.Background(), action)
	if ErrorCode(err) != "unknown_outcome" || result.Execution != ExecutionUnknown {
		t.Fatalf("uncertain action = (%+v, %v), want unknown outcome", result, err)
	}
	if err := session.Close(context.Background()); err == nil {
		t.Fatal("close with unwritable journal succeeded")
	}
	if got := len(backend.executed); got != 1 {
		t.Fatalf("native action dispatched %d times", got)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("retry Close after restoring journal permissions: %v", err)
	}
	lease, err := writer.Acquire(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close(context.Background()) }()
	var binding [32]byte
	if _, _, err := lease.Begin("action-after-unknown", binding); !errors.Is(err, writer.ErrDirty) {
		t.Fatalf("new action after unknown outcome = %v, want ErrDirty", err)
	}
}

func TestApprovalCancellationAndDeadlineCodesReplayExactly(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "cancelled", err: context.Canceled, code: "cancelled"},
		{name: "deadline", err: context.DeadlineExceeded, code: "budget_exceeded"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			process := testProcess()
			backend := &fakeBackend{process: process, nativeState: "native-action", elements: testElements(), input: true, qualified: true}
			config := Config{Backend: backend, Scope: Scope{Processes: []ProcessIdentity{process}, ExpiresAt: time.Now().Add(time.Hour)}, Budget: testBudget(), ApprovalProvider: fixedApproval{err: tc.err}, WriterDirectory: filepath.Join(t.TempDir(), "state"), WriterKey: []byte(strings.Repeat("k", 32)), MaxActions: 2}
			session, err := NewSession(config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Windows(context.Background()); err != nil {
				t.Fatal(err)
			}
			state, err := session.Observe(context.Background(), "window-1")
			if err != nil {
				t.Fatal(err)
			}
			action := Action{ID: "approval-replay", WindowRef: "window-1", ElementRef: "normal-1", StateID: state.StateID, Kind: ActionPress}
			first, firstErr := session.Do(context.Background(), action)
			if ErrorCode(firstErr) != tc.code || first.Execution != ExecutionNotApplied {
				t.Fatalf("first Do = (%+v, %v), want not applied with %s", first, firstErr, tc.code)
			}
			second, secondErr := session.Do(context.Background(), action)
			if ErrorCode(secondErr) != tc.code || second.Execution != ExecutionNotApplied {
				t.Fatalf("replay Do = (%+v, %v), want same durable %s outcome", second, secondErr, tc.code)
			}
			if len(backend.executed) != 0 {
				t.Fatalf("approval failure dispatched native action: %#v", backend.executed)
			}
			if err := session.Close(context.Background()); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

func TestCloseRetainsOwnershipAfterBackendCloseFailure(t *testing.T) {
	secret := "native private close detail"
	backend := &fakeBackend{process: testProcess(), closeErrors: []error{errors.New(secret)}}
	session := newTestSession(t, backend, false)
	err := session.Close(context.Background())
	if ErrorCode(err) != "backend_unavailable" || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe close error: %v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("retry Close: %v", err)
	}
	if backend.closeCalls != 2 {
		t.Fatalf("Close calls=%d want retry", backend.closeCalls)
	}
}

func TestExpiredSnapshotAndCancellationFailClosed(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-a", elements: testElements()}
	session := newTestSession(t, backend, false)
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	session.now = func() time.Time { return time.Now().Add(snapshotTTL + time.Second) }
	if _, err := session.ReadElement(context.Background(), "window-1", "normal-1", state.StateID); ErrorCode(err) != "state_expired" {
		t.Fatalf("expired read error=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.Do(ctx, Action{ID: "action-cancel", WindowRef: "window-1", ElementRef: "normal-1", StateID: state.StateID, Kind: ActionPress}); ErrorCode(err) != "approval_required" {
		t.Fatalf("readonly cancellation path unexpectedly admitted: %v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotGenerationAndStorageRetentionLimits(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-0", elements: testElements()}
	session := newTestSession(t, backend, false)
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	states := make([]string, 0, maxSnapshotGenerations+1)
	for i := 0; i < maxSnapshotGenerations+1; i++ {
		backend.mu.Lock()
		backend.nativeState = fmt.Sprintf("native-%d", i)
		backend.elements[0].Order = i
		backend.mu.Unlock()
		state, err := session.Observe(context.Background(), "window-1")
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, state.StateID)
	}
	if _, err := session.ReadElement(context.Background(), "window-1", "normal-1", states[0]); ErrorCode(err) != "state_expired" {
		t.Fatalf("oldest generation retained: %v", err)
	}
	session.now = func() time.Time { return time.Now().Add(snapshotTTL + time.Second) }
	if _, err := session.ReadElement(context.Background(), "window-1", "normal-1", states[len(states)-1]); ErrorCode(err) != "state_expired" {
		t.Fatalf("expired generation retained: %v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAllowValuesIsExplicitAndStillRedactsSecureElements(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-value", elements: testElements()}
	config := Config{Backend: backend, Scope: Scope{Processes: []ProcessIdentity{backend.process}, ExpiresAt: time.Now().Add(time.Hour)}, Budget: testBudget(), AllowValues: true}
	session, err := NewSession(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Elements) != 1 || state.Elements[0].Value == nil || *state.Elements[0].Value != "ordinary private value" {
		t.Fatalf("explicit normal value opt-in missing: %#v", state.Elements)
	}
	if strings.Contains(string(mustJSON(t, state)), "secure hidden value") {
		t.Fatal("secure value escaped explicit value mode")
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPartialSnapshotPreservesCoverageForReadsAndBlocksWrites(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-partial", elements: testElements(), partial: true, input: true, qualified: true}
	session := newTestSession(t, backend, true)
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Coverage.Complete || state.Coverage.Reason != "partial" || strings.Contains(state.Coverage.Reason, "private") {
		t.Fatalf("partial coverage was not safely preserved: %#v", state.Coverage)
	}
	if _, err := session.ReadElement(context.Background(), "window-1", "normal-1", state.StateID); err != nil {
		t.Fatalf("freshly verified observed normal target should remain readable: %v", err)
	}
	action := Action{ID: "partial-block", WindowRef: "window-1", ElementRef: "normal-1", StateID: state.StateID, Kind: ActionPress}
	if _, err := session.Do(context.Background(), action); ErrorCode(err) != "policy_refused" {
		t.Fatalf("write admitted against partial snapshot: %v", err)
	}
	if len(backend.executed) != 0 {
		t.Fatal("partial snapshot dispatched a write")
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
