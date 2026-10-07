package comuse

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/sirerun/comuse/internal/backend"
)

type desktopContextBackend struct {
	*fakeBackend
	mu      sync.Mutex
	desktop *DesktopContext
}

func (b *desktopContextBackend) setDesktop(value *DesktopContext) {
	b.mu.Lock()
	b.desktop = cloneDesktopContext(value)
	b.mu.Unlock()
}

func (b *desktopContextBackend) Doctor(ctx context.Context) (DoctorReport, error) {
	report, err := b.fakeBackend.Doctor(ctx)
	if err != nil {
		return report, err
	}
	b.mu.Lock()
	report.DesktopContext = cloneDesktopContext(b.desktop)
	b.mu.Unlock()
	return report, nil
}

func (b *desktopContextBackend) Observe(ctx context.Context, windowRef string, budget Budget) (Observation, error) {
	result, err := b.fakeBackend.Observe(ctx, windowRef, budget)
	if err != nil {
		return result, err
	}
	result.ScopeID = "caller-controlled-scope"
	b.mu.Lock()
	result.DesktopContext = cloneDesktopContext(b.desktop)
	b.mu.Unlock()
	return result, nil
}

func testDesktopContext(process ProcessIdentity) *DesktopContext {
	return &DesktopContext{DisplayID: "display-primary", DisplayGeneration: 1,
		FocusedWindow: &Window{Ref: "window-1", Process: process, Title: "Fixture window"}}
}

func TestDoctorAndSnapshotProjectOnlyInspectedCompactContext(t *testing.T) {
	process := testProcess()
	backend := &desktopContextBackend{fakeBackend: &fakeBackend{process: process, nativeState: "native-a", elements: testElements()}}
	backend.setDesktop(testDesktopContext(process))
	session := newTestSession(t, backend.fakeBackend, false)
	// Rebind the test session to the context-capable backend without changing
	// its immutable scope or any backend call accounting.
	session.backend = backend
	envelope, err := session.Call(context.Background(), Request{Operation: OperationDoctor})
	if err != nil || envelope.StateStatus != "available" {
		t.Fatalf("Doctor compact context: status=%s state=%s err=%v", envelope.Status, envelope.StateStatus, err)
	}
	var state CompactState
	if err := json.Unmarshal(envelope.State.encoded, &state); err != nil {
		t.Fatal(err)
	}
	if state.DisplayID != "display-primary" || state.DisplayGeneration != 1 || state.FocusedWindow == nil || state.FocusedWindow.Ref != "window-1" {
		t.Fatalf("compact state = %+v", state)
	}
	var doctor MetadataDoctor
	if err := json.Unmarshal(envelope.Result.encoded, &doctor); err != nil {
		t.Fatal(err)
	}
	encodedDoctor, _ := json.Marshal(doctor)
	if strings.Contains(string(encodedDoctor), "desktop_context") || strings.Contains(string(encodedDoctor), "display-primary") {
		t.Fatalf("legacy Doctor leaked trusted context: %s", encodedDoctor)
	}
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DesktopContext == nil || snapshot.DesktopContext.FocusedWindow == nil {
		t.Fatal("snapshot lost detached context evidence")
	}
	if snapshot.ScopeID == "caller-controlled-scope" {
		t.Fatal("backend supplied ScopeID became semantic authority")
	}
	*snapshot.DesktopContext.FocusedWindow = Window{Ref: "changed"}
	stored, ok := session.findSnapshot("window-1", snapshot.StateID)
	if !ok || stored.public.DesktopContext.FocusedWindow.Ref != "window-1" {
		t.Fatal("snapshot context clone aliased returned value")
	}
	noFocus := testDesktopContext(process)
	noFocus.FocusedWindow = nil
	backend.setDesktop(noFocus)
	noFocusEnvelope, err := session.Call(context.Background(), Request{Operation: OperationDoctor})
	if err != nil || noFocusEnvelope.StateStatus != "available" || string(noFocusEnvelope.State.encoded) == "null" {
		t.Fatalf("inspected no-focus context was lost: %+v err=%v", noFocusEnvelope, err)
	}
	var noFocusState MetadataCompactState
	if err := json.Unmarshal(noFocusEnvelope.State.encoded, &noFocusState); err != nil || noFocusState.FocusedWindow != nil {
		t.Fatalf("null focus was not preserved: state=%+v err=%v", noFocusState, err)
	}
}

func TestDisplayGenerationRotatesScopeAndPurgesAuthorityWithoutPermissionFailure(t *testing.T) {
	process := testProcess()
	backend := &desktopContextBackend{fakeBackend: &fakeBackend{process: process, nativeState: "native-a", elements: testElements()}}
	backend.setDesktop(testDesktopContext(process))
	session := newTestSession(t, backend.fakeBackend, false)
	session.backend = backend
	if _, err := session.Doctor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	firstFull, err := projectFullObservation(session, first)
	if err != nil {
		t.Fatal(err)
	}
	permissionEpoch := session.currentPermissionEpoch()
	secondContext := testDesktopContext(process)
	secondContext.DisplayGeneration = 2
	secondContext.FocusedWindow = nil
	backend.setDesktop(secondContext)
	report, err := session.Doctor(context.Background())
	if err != nil || report.DesktopContext.DisplayGeneration != 2 {
		t.Fatalf("changed-context Doctor: report=%+v err=%v", report, err)
	}
	if got := session.currentPermissionEpoch(); got != permissionEpoch {
		t.Fatalf("display change fabricated permission epoch: %d -> %d", permissionEpoch, got)
	}
	if len(session.windows) != 0 || len(session.snapshots) != 0 || session.snapshotBytes != 0 {
		t.Fatalf("display change retained authority: windows=%d snapshots=%d bytes=%d", len(session.windows), len(session.snapshots), session.snapshotBytes)
	}
	if _, err := session.Observe(context.Background(), "window-1"); ErrorCode(err) != "element_stale" {
		t.Fatalf("old window binding survived display rotation: %v", err)
	}
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	secondFull, err := projectFullObservation(session, second)
	if err != nil {
		t.Fatal(err)
	}
	if firstFull.ScopeID == secondFull.ScopeID {
		t.Fatal("semantic scope did not bind display generation")
	}
}

func TestLateObservationFromPriorDisplayEpochFailsClosed(t *testing.T) {
	process := testProcess()
	started := make(chan struct{})
	release := make(chan struct{})
	fake := &fakeBackend{process: process, nativeState: "native-a", elements: testElements(),
		observeStarted: started, observeRelease: release}
	backend := &desktopContextBackend{fakeBackend: fake}
	backend.setDesktop(testDesktopContext(process))
	session := newTestSession(t, fake, false)
	session.backend = backend
	if _, err := session.Doctor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := session.Observe(context.Background(), "window-1")
		result <- err
	}()
	<-started
	changed := testDesktopContext(process)
	changed.DisplayGeneration = 2
	changed.FocusedWindow = nil
	backend.setDesktop(changed)
	if _, err := session.Doctor(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; ErrorCode(err) != "state_expired" {
		t.Fatalf("late old-epoch observation returned %v", err)
	}
	if len(session.snapshots) != 0 || len(session.windows) != 0 {
		t.Fatal("late old-epoch observation restored purged authority")
	}
}

func TestInvalidOrForeignDesktopContextIsRejectedAndMissingContextUnavailable(t *testing.T) {
	process := testProcess()
	foreign := testDesktopContext(process)
	foreign.FocusedWindow.Process.PID++
	backend := &desktopContextBackend{fakeBackend: &fakeBackend{process: process}}
	backend.setDesktop(foreign)
	session := newTestSession(t, backend.fakeBackend, false)
	session.backend = backend
	if _, err := session.Doctor(context.Background()); ErrorCode(err) != "backend_unavailable" {
		t.Fatalf("foreign context accepted: %v", err)
	}
	backend.setDesktop(nil)
	envelope, err := session.Call(context.Background(), Request{Operation: OperationDoctor})
	if err != nil || envelope.StateStatus != "unavailable" || string(envelope.State.encoded) != "null" {
		t.Fatalf("missing context fabricated compact state: %+v err=%v", envelope, err)
	}
	for _, raw := range []string{
		`{"display_id":"d","display_generation":1}`,
		`{"display_id":"d","display_generation":0,"focused_window":null}`,
		`{"display_id":"d","display_generation":1,"focused_window":null,"extra":true}`,
		`{"display_id":"d","display_id":"other","display_generation":1,"focused_window":null}`,
	} {
		var context DesktopContext
		if err := json.Unmarshal([]byte(raw), &context); err == nil {
			t.Fatalf("malformed context accepted: %s", raw)
		}
	}
	var decoded DoctorReport
	if err := json.Unmarshal([]byte(`{"capabilities":{},"permissions":{},"desktop_context":{"display_id":"d","display_generation":1,"focused_window":null}}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.DesktopContext == nil {
		t.Fatal("valid wire context was not decoded")
	}
}

func TestBackendDesktopContextCloneDoesNotAlias(t *testing.T) {
	ctx := testDesktopContext(testProcess())
	clone := cloneDesktopContext(ctx)
	clone.FocusedWindow.Title = "mutated"
	if ctx.FocusedWindow.Title != "Fixture window" {
		t.Fatal("desktop context clone aliases focused window")
	}
	var decoded backend.DesktopContext
	if err := json.Unmarshal([]byte(`{"display_id":"d","display_generation":9007199254740991,"focused_window":null}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.DisplayGeneration != maxDesktopGeneration {
		t.Fatalf("generation decoded as %d", decoded.DisplayGeneration)
	}
	if err := json.Unmarshal([]byte(`{"display_id":"d","display_generation":9007199254740992,"focused_window":null}`), &decoded); err == nil {
		t.Fatal("unsafe JSON integer display generation was accepted")
	}
}

func TestDesktopContextWireShapeUsesNullableFocusedWindow(t *testing.T) {
	value := DesktopContext{DisplayID: "display-1", DisplayGeneration: 3}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"display_id":"display-1","display_generation":3,"focused_window":null}` {
		t.Fatalf("wire context = %s", encoded)
	}
}
