//go:build darwin && cgo

package darwin

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/sirerun/comuse/internal/backend"
)

type fakeNativeTransport struct {
	closeFailures int
	starts        int
	closed        bool
}

func (f *fakeNativeTransport) isProcessMain() bool                 { return true }
func (f *fakeNativeTransport) open([]byte) (uint64, []byte, error) { return 1, []byte(`{}`), nil }
func (f *fakeNativeTransport) pump(uint64, uint32) error           { return nil }
func (f *fakeNativeTransport) closeRuntime(uint64) error {
	if f.closeFailures > 0 {
		f.closeFailures--
		return errors.New("injected close failure")
	}
	return nil
}
func (f *fakeNativeTransport) start(uint64, []byte, uint64, chan nativeCompletion) (uint64, error) {
	f.starts++
	return uint64(f.starts), nil
}
func (f *fakeNativeTransport) cancel(uint64, uint64) error { return nil }
func (f *fakeNativeTransport) close()                      { f.closed = true }

func TestValidateBoundScopeUsesWireExpiryPrecisionWithoutExtension(t *testing.T) {
	requestedExpiry := time.Unix(1_800_000_000, 123_456_789)
	requested := backend.Scope{
		Processes: []backend.ProcessIdentity{{PID: 42, BundleID: "com.example.app"}},
		ExpiresAt: requestedExpiry,
	}
	resolved := backend.Scope{
		Processes: []backend.ProcessIdentity{{PID: 42, BundleID: "com.example.app", LaunchID: "launch-1"}},
		ExpiresAt: time.UnixMilli(requestedExpiry.UnixMilli()),
	}
	if err := validateBoundScope(requested, resolved); err != nil {
		t.Fatalf("millisecond-rounded expiry rejected: %v", err)
	}
	resolved.ExpiresAt = resolved.ExpiresAt.Add(time.Millisecond)
	if err := validateBoundScope(requested, resolved); err == nil {
		t.Fatal("expiry extension accepted")
	}
}

func TestOwnerCloseFailureRetainsAndMainThreadRetryCloses(t *testing.T) {
	if hasRetainedOwner() {
		t.Fatal("test starts with a retained native owner")
	}
	runtime.LockOSThread()
	transport := &fakeNativeTransport{closeFailures: ownerCloseAttempts}
	owner := &runtimeOwner{
		lib: transport, runtimeID: 9, pending: make(map[uint64]pendingRequest),
		commands: make(chan ownerCommand), completions: make(chan nativeCompletion, 64),
		closing: true, workerEnded: true,
	}
	owner.drainDeadline = nowPlusOwnerDrain()
	if err := owner.loop(context.Background()); err == nil {
		t.Fatal("expected bounded close failure")
	}
	if !hasRetainedOwner() || transport.closed {
		t.Fatal("failed close must retain the owner and loaded library")
	}
	if err := RetryPendingClose(context.Background()); err != nil {
		runtime.UnlockOSThread()
		t.Fatalf("retry close: %v", err)
	}
	if hasRetainedOwner() || !transport.closed {
		t.Fatal("successful retry must clear retained ownership and unload")
	}
}

func TestOwnerRejectsStartsAtNativePendingLimit(t *testing.T) {
	transport := &fakeNativeTransport{}
	owner := &runtimeOwner{lib: transport, runtimeID: 1, completions: make(chan nativeCompletion, 64)}
	pending := make(map[uint64]pendingRequest, maximumInflight)
	for i := 0; i < maximumInflight; i++ {
		pending[uint64(i+1)] = pendingRequest{}
	}
	reply := make(chan callResult, 1)
	owner.start(ownerCommand{request: []byte(`{}`), ctx: context.Background(), reply: reply}, pending)
	if got := <-reply; got.err == nil || backend.ErrorCode(got.err) != "rate_limited" {
		t.Fatalf("start at limit: got %v, want rate_limited", got.err)
	}
	if transport.starts != 0 {
		t.Fatal("native start ran despite pending limit")
	}
}

func TestOwnerReturnsAfterContextDrainDeadlineWhenWorkerDoesNotFinish(t *testing.T) {
	transport := &fakeNativeTransport{}
	owner := &runtimeOwner{
		lib: transport, runtimeID: 1, pending: make(map[uint64]pendingRequest),
		commands: make(chan ownerCommand), completions: make(chan nativeCompletion, 64),
		closing: true, closed: true, workerDone: make(chan error),
		drainDeadline: time.Now().Add(-time.Millisecond),
	}
	if err := owner.loop(context.Background()); err == nil || backend.ErrorCode(err) != "backend_unavailable" {
		t.Fatalf("loop with unfinished worker: got %v, want backend_unavailable", err)
	}
}

func nowPlusOwnerDrain() time.Time { return time.Now().Add(ownerDrainTimeout) }

func TestNativeErrorEnvelopeUsesStableStringCode(t *testing.T) {
	data := []byte(`{"schema_version":1,"request_id":"r1","status":"error","error":"permission_denied"}`)
	if _, err := validateEnvelope(data, "r1"); err == nil || backend.ErrorCode(err) != "permission_denied" {
		t.Fatalf("native error: got %v, want permission_denied", err)
	}
	malformed := []byte(`{"schema_version":1,"request_id":"r1","status":"error","result":{},"error":"permission_denied"}`)
	if _, err := validateEnvelope(malformed, "r1"); err == nil || backend.ErrorCode(err) != "backend_unavailable" {
		t.Fatalf("nonexclusive native error: got %v, want backend_unavailable", err)
	}
}

func TestExecuteRemainsClosedWithoutNativeDispatch(t *testing.T) {
	native := &nativeBackend{}
	result, err := native.Execute(context.Background(), backend.Action{
		ID: "a1", WindowRef: "w1", ElementRef: "e1", StateID: "s1", Kind: backend.ActionPress,
	})
	if err == nil || backend.ErrorCode(err) != "unsupported" {
		t.Fatalf("Execute: got %v, want unsupported", err)
	}
	if result.ActionID != "" {
		t.Fatalf("closed Execute returned fabricated result: %#v", result)
	}
}

func TestExecuteRejectsInvalidTypedActionBeforeClosedBackendResponse(t *testing.T) {
	native := &nativeBackend{}
	_, err := native.Execute(context.Background(), backend.Action{
		ID: "a1", WindowRef: "w1", ElementRef: "e1", StateID: "s1", Kind: backend.ActionInsert,
	})
	if err == nil || backend.ErrorCode(err) != "invalid_request" {
		t.Fatalf("empty insert: got %v, want invalid_request", err)
	}
}
