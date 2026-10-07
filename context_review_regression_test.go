package comuse

import (
	"context"
	"strings"
	"testing"
)

func newReviewedContextSession(t *testing.T) (*Session, *desktopContextBackend) {
	t.Helper()
	b := &desktopContextBackend{fakeBackend: &fakeBackend{process: testProcess(), nativeState: "reviewed-context", elements: testElements()}}
	b.setDesktop(testDesktopContext(testProcess()))
	s := newTestSession(t, b.fakeBackend, false)
	s.backend = b
	return s, b
}

func TestReviewedFocusedContextReferenceSupportsObserve(t *testing.T) {
	s, _ := newReviewedContextSession(t)
	report, err := s.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Observe(context.Background(), report.DesktopContext.FocusedWindow.Ref); err != nil {
		t.Fatalf("fresh focused reference is unusable: %v", err)
	}
}

func TestReviewedFirstWindowsObserveEstablishesContext(t *testing.T) {
	s, _ := newReviewedContextSession(t)
	windows, err := s.Windows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Observe(context.Background(), windows[0].Ref); err != nil {
		t.Fatalf("first enumeration/observation failed: %v", err)
	}
}

type reviewedClosureBackend struct {
	*fakeBackend
	closed      bool
	windowCalls uint64
}

func (b *reviewedClosureBackend) Windows(ctx context.Context, budget Budget) ([]Window, error) {
	b.windowCalls++
	if b.closed {
		return []Window{}, nil
	}
	return b.fakeBackend.Windows(ctx, budget)
}
func TestReviewedWindowClosedCountsEveryStateAttempt(t *testing.T) {
	b := &reviewedClosureBackend{fakeBackend: &fakeBackend{process: testProcess(), nativeState: "closure-accounting", elements: testElements()}}
	s := newTestSession(t, b.fakeBackend, false)
	s.backend = b
	if _, err := s.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.closed = true
	beforeDoctor, beforeWindows := b.doctorCalls, b.windowCalls
	envelope, err := s.Call(context.Background(), Request{Operation: OperationWait, Wait: &WaitParams{Condition: "window_closed", WindowRef: "window-1", TimeoutMS: 500}})
	if err != nil {
		t.Fatal(err)
	}
	actual := uint64(b.doctorCalls-beforeDoctor) + b.windowCalls - beforeWindows
	if envelope.Usage.Observations.State != actual {
		t.Fatalf("state attempts counted=%d actual=%d", envelope.Usage.Observations.State, actual)
	}
}
func TestReviewedFocusedWindowTitleLimitAtBackendBoundary(t *testing.T) {
	s, b := newReviewedContextSession(t)
	value := testDesktopContext(testProcess())
	value.FocusedWindow.Title = strings.Repeat("x", 4097)
	b.setDesktop(value)
	if _, err := s.Doctor(context.Background()); ErrorCode(err) != "backend_unavailable" {
		t.Fatalf("oversized trusted backend title: %v", err)
	}
}

type reviewedDelayedDoctorBackend struct {
	*fakeBackend
	started chan struct{}
	release chan struct{}
}

func (b *reviewedDelayedDoctorBackend) Doctor(ctx context.Context) (DoctorReport, error) {
	report, err := b.fakeBackend.Doctor(ctx)
	report.DesktopContext = testDesktopContext(testProcess())
	close(b.started)
	<-b.release
	return report, err
}
func TestReviewedFocusedReferenceCannotRestoreRevokedAuthority(t *testing.T) {
	b := &reviewedDelayedDoctorBackend{fakeBackend: &fakeBackend{process: testProcess()}, started: make(chan struct{}), release: make(chan struct{})}
	s := newTestSession(t, b.fakeBackend, false)
	s.backend = b
	done := make(chan error, 1)
	go func() { _, err := s.Doctor(context.Background()); done <- err }()
	<-b.started
	s.invalidateSemanticState()
	close(b.release)
	if err := <-done; ErrorCode(err) != "permission_denied" {
		t.Fatalf("late Doctor restored revoked authority: %v", err)
	}
	if _, ok := s.window("window-1"); ok {
		t.Fatal("late focused reference restored")
	}
}

func TestReviewedFocusedTitleExactBoundRemainsUsable(t *testing.T) {
	s, b := newReviewedContextSession(t)
	value := testDesktopContext(testProcess())
	value.FocusedWindow.Title = strings.Repeat("x", 4096)
	b.setDesktop(value)
	envelope, err := s.Call(context.Background(), Request{Operation: OperationDoctor})
	if err != nil || envelope.StateStatus != "available" {
		t.Fatalf("exact title bound rejected: %+v %v", envelope, err)
	}
}
