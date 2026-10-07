package comuse

import (
	"context"
	"errors"
	"github.com/sirerun/comuse/internal/writer"
	"slices"
	"testing"
	"time"
)

type syntheticDesktopAuthority struct{}

func (*syntheticDesktopAuthority) Begin([32]byte) error { return nil }
func (*syntheticDesktopAuthority) Complete() error      { return nil }
func (*syntheticDesktopAuthority) MarkDirty() error     { return nil }
func (*syntheticDesktopAuthority) Close() error         { return nil }

func newSyntheticSession(config Config) (*Session, error) {
	s, err := NewSession(config)
	if err == nil && s.mutationEnabled {
		s.acquireDesktop = func(context.Context) (desktopAuthority, error) { return &syntheticDesktopAuthority{}, nil }
		s.reserveQuota = func(context.Context, [32]byte) error { return nil }
		s.journalBinding = func(string, []byte) ([32]byte, error) { return [32]byte{1}, nil }
	}
	return s, err
}

type recordedDesktopAuthority struct {
	events      *[]string
	completeErr error
}

func (g *recordedDesktopAuthority) Begin([32]byte) error {
	*g.events = append(*g.events, "intent")
	return nil
}
func (g *recordedDesktopAuthority) Complete() error {
	*g.events = append(*g.events, "complete")
	return g.completeErr
}
func (g *recordedDesktopAuthority) MarkDirty() error {
	*g.events = append(*g.events, "dirty")
	return nil
}
func (g *recordedDesktopAuthority) Close() error { *g.events = append(*g.events, "close"); return nil }

func TestCanonicalQuotaRefusalSettlesIntentAndReplaysWithoutNativeRead(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-state", elements: testElements(), input: true, qualified: true}
	session := newTestSession(t, backend, true)
	_, err := session.Windows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	observed, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	events := []string{}
	quotaAttempts := 0
	session.acquireDesktop = func(context.Context) (desktopAuthority, error) {
		events = append(events, "acquire")
		return &recordedDesktopAuthority{events: &events}, nil
	}
	session.reserveQuota = func(context.Context, [32]byte) error {
		events = append(events, "quota")
		quotaAttempts++
		return writer.ErrQuotaExhausted
	}
	action := Action{ID: "quota-refused", WindowRef: "window-1", ElementRef: "normal-1", StateID: observed.StateID, Kind: ActionPress}
	result, err := session.Do(context.Background(), action)
	if result.Execution != ExecutionNotApplied || err == nil || ErrorCode(err) != "rate_limited" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !slices.Equal(events, []string{"acquire", "intent", "quota", "complete", "close"}) {
		t.Fatalf("ordering=%v", events)
	}
	session.now = func() time.Time { return time.Now().Add(snapshotTTL + time.Second) }
	backend.observeErr = errors.New("private diagnostic must never be read during replay")
	replay, replayErr := session.Do(context.Background(), action)
	if replay.Execution != result.Execution || replayErr == nil || ErrorCode(replayErr) != "rate_limited" || quotaAttempts != 1 || len(backend.executed) != 0 {
		t.Fatalf("replay=%+v err=%v attempts=%d", replay, replayErr, quotaAttempts)
	}
}

func TestCanonicalCleanupFailureRetainsLockUntilBackendDrain(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-state", elements: testElements(), input: true, qualified: true}
	session := newTestSession(t, backend, true)
	_, _ = session.Windows(context.Background())
	observed, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	events := []string{}
	guard := &recordedDesktopAuthority{events: &events, completeErr: errors.New("private persistence failure")}
	session.acquireDesktop = func(context.Context) (desktopAuthority, error) { return guard, nil }
	result, err := session.Do(context.Background(), Action{ID: "complete-failed", WindowRef: "window-1", ElementRef: "normal-1", StateID: observed.StateID, Kind: ActionPress})
	if result.Execution != ExecutionApplied || result.Cleanup != CleanupComplete || err == nil || ErrorCode(err) != "backend_unavailable" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if slices.Contains(events, "close") {
		t.Fatal("canonical lock released before backend drain")
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if backend.closeCalls != 1 || !slices.Contains(events, "close") {
		t.Fatalf("drain ordering backend=%d events=%v", backend.closeCalls, events)
	}
}
