package comuse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sirerun/comuse/internal/writer"
)

func newProtectedRecoveryJournal(t *testing.T) string {
	t.Helper()
	base := os.Getenv("COMUSE_PRIVATE_TEST_TMP")
	if base == "" {
		if runtime.GOOS == "darwin" {
			t.Skip("protected externally backed fixture root required")
		}
		base = "/tmp"
	}
	root, err := os.MkdirTemp(base, "comuse-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove recovery fixture: %v", err)
		}
	})
	return root
}

func TestReconcileDesktopRunsVerifierForCleanJournalAndPreservesOrder(t *testing.T) {
	root := newProtectedRecoveryJournal(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	expectedBinding, err := writer.DesktopJournalBinding(root, key)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	verifierCalled := false
	markerRemoved := false
	acquire := func(ctx context.Context, path string) (recoveryJournalLease, error) {
		events = append(events, "journal-acquire")
		return writer.Acquire(ctx, path)
	}
	bind := func(path string, secret []byte) ([32]byte, error) {
		events = append(events, "binding")
		return writer.DesktopJournalBinding(path, secret)
	}
	reconcile := func(ctx context.Context, binding [32]byte, callback func(context.Context) error) error {
		events = append(events, "canonical-lock")
		if binding != expectedBinding {
			t.Fatal("binding differs from the exact journal root and key")
		}
		// A second journal writer must remain excluded until callback recovery
		// has durably completed under the canonical lock.
		lockCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		if competing, err := writer.Acquire(lockCtx, root); err == nil {
			_ = competing.Close(ctx)
			t.Fatal("journal lock was not held while canonical exclusion was acquired")
		} else if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("competing journal acquisition error = %v, want deadline while exclusion is held", err)
		}
		if err := callback(ctx); err != nil {
			return err
		}
		markerRemoved = true
		return nil
	}
	err = reconcileDesktop(context.Background(), root, key, func(context.Context) error {
		events = append(events, "trusted-verifier")
		verifierCalled = true
		return nil
	}, acquire, bind, reconcile)
	if err != nil {
		t.Fatalf("reconcileDesktop: %v", err)
	}
	if !verifierCalled || !markerRemoved {
		t.Fatalf("verifier called=%v marker removed=%v", verifierCalled, markerRemoved)
	}
	want := []string{"journal-acquire", "binding", "canonical-lock", "trusted-verifier"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v, want %v", events, want)
	}
}

func TestReconcileDesktopClearsDirtyJournalBeforeMarkerRemoval(t *testing.T) {
	root := newProtectedRecoveryJournal(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	initial, err := writer.Acquire(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Quarantine("native_outcome_unknown"); err != nil {
		t.Fatal(err)
	}
	if err := initial.Close(ctx); !errors.Is(err, writer.ErrDirty) {
		t.Fatalf("close dirty fixture = %v, want ErrDirty", err)
	}
	key := make([]byte, 32)
	key[0] = 1
	markerRemoved := false
	err = reconcileDesktop(ctx, root, key, func(context.Context) error { return nil },
		func(ctx context.Context, path string) (recoveryJournalLease, error) { return writer.Acquire(ctx, path) },
		writer.DesktopJournalBinding,
		func(ctx context.Context, binding [32]byte, callback func(context.Context) error) error {
			if err := callback(ctx); err != nil {
				return err
			}
			// Reopening after the callback must not need reconciliation again.
			reopened, err := writer.Acquire(ctx, root)
			if err != nil {
				return err
			}
			called := false
			err = reopened.ReconcileDirty(ctx, "probe", func(context.Context, []writer.HeldStatusRecord) error {
				called = true
				return nil
			})
			closeErr := reopened.Close(ctx)
			if err != nil || closeErr != nil || called {
				return errors.Join(err, closeErr, errors.New("journal remained dirty after recovery"))
			}
			markerRemoved = true
			return nil
		})
	if err != nil {
		t.Fatalf("reconcile dirty journal: %v", err)
	}
	if !markerRemoved {
		t.Fatal("canonical marker was not removed after durable recovery")
	}
}

func TestReconcileDesktopRetainsMarkerOnWrongBindingOrVerifierFailure(t *testing.T) {
	tests := []struct {
		name         string
		verifyErr    error
		wrongBinding bool
	}{
		{name: "wrong binding", wrongBinding: true},
		{name: "verifier failure", verifyErr: errors.New("cleanup not proven")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := newProtectedRecoveryJournal(t)
			key := make([]byte, 32)
			key[0] = 1
			expectedBinding, err := writer.DesktopJournalBinding(root, key)
			if err != nil {
				t.Fatal(err)
			}
			verifierCalled := false
			markerRemoved := false
			err = reconcileDesktop(context.Background(), root, key, func(context.Context) error {
				verifierCalled = true
				return tc.verifyErr
			},
				func(ctx context.Context, path string) (recoveryJournalLease, error) { return writer.Acquire(ctx, path) },
				func(path string, secret []byte) ([32]byte, error) {
					binding, err := writer.DesktopJournalBinding(path, secret)
					if tc.wrongBinding {
						binding[0] ^= 0xff
					}
					return binding, err
				},
				func(ctx context.Context, binding [32]byte, callback func(context.Context) error) error {
					if binding != expectedBinding {
						return writer.ErrDirty
					}
					if err := callback(ctx); err != nil {
						return err
					}
					markerRemoved = true
					return nil
				})
			if err == nil {
				t.Fatal("recovery unexpectedly succeeded")
			}
			if verifierCalled == tc.wrongBinding {
				t.Fatalf("verifier called=%v with wrong binding=%v", verifierCalled, tc.wrongBinding)
			}
			if markerRemoved {
				t.Fatal("canonical marker was removed after failed recovery")
			}
		})
	}
}

func TestReconcileDesktopHonorsCancelledVerifierContext(t *testing.T) {
	root := newProtectedRecoveryJournal(t)
	key := make([]byte, 32)
	key[0] = 1
	verifierCalled := false
	markerRemoved := false
	err := reconcileDesktop(context.Background(), root, key, func(context.Context) error {
		verifierCalled = true
		return nil
	},
		func(ctx context.Context, path string) (recoveryJournalLease, error) { return writer.Acquire(ctx, path) },
		writer.DesktopJournalBinding,
		func(_ context.Context, _ [32]byte, callback func(context.Context) error) error {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := callback(ctx); err != nil {
				return err
			}
			markerRemoved = true
			return nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("reconcile error = %v, want context cancellation", err)
	}
	if verifierCalled || markerRemoved {
		t.Fatalf("verifier called=%v marker removed=%v after cancellation", verifierCalled, markerRemoved)
	}
}

func TestReconcileDesktopJournalFailureSkipsBindingAndCanonicalRecovery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	key := make([]byte, 32)
	bindCalled := false
	reconcileCalled := false
	err := reconcileDesktop(context.Background(), root, key, func(context.Context) error { return nil },
		func(context.Context, string) (recoveryJournalLease, error) {
			return nil, errors.New("journal unavailable")
		},
		func(string, []byte) ([32]byte, error) {
			bindCalled = true
			return [32]byte{}, nil
		},
		func(context.Context, [32]byte, func(context.Context) error) error {
			reconcileCalled = true
			return nil
		})
	if err == nil || bindCalled || reconcileCalled {
		t.Fatalf("error=%v binding called=%v canonical called=%v", err, bindCalled, reconcileCalled)
	}
}

type recoveryCloseErrorLease struct{ closeErr error }

func (l recoveryCloseErrorLease) ReconcileDirty(context.Context, string, writer.ReconcileFunc) error {
	return nil
}

func (l recoveryCloseErrorLease) Close(context.Context) error { return l.closeErr }

func TestReconcileDesktopPropagatesJournalCloseFailureAndRetainsMarker(t *testing.T) {
	closeErr := errors.New("journal unlock failed")
	markerRemoved := false
	err := reconcileDesktop(context.Background(), "journal", make([]byte, 32), func(context.Context) error { return nil },
		func(context.Context, string) (recoveryJournalLease, error) {
			return recoveryCloseErrorLease{closeErr: closeErr}, nil
		},
		func(string, []byte) ([32]byte, error) { return [32]byte{1}, nil },
		func(ctx context.Context, _ [32]byte, callback func(context.Context) error) error {
			if err := callback(ctx); err != nil {
				return err
			}
			markerRemoved = true
			return nil
		})
	if !errors.Is(err, closeErr) {
		t.Fatalf("reconcile error = %v, want journal close error", err)
	}
	if markerRemoved {
		t.Fatal("canonical marker was removed after journal close failure")
	}
}

func TestReconcileDesktopPublicBoundaryRejectsUnsafeJournalInputs(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if err := ReconcileDesktop(context.Background(), root, key, func(context.Context) error { return nil }); ErrorCode(err) != "invalid_request" {
		t.Fatalf("unsafe journal error code=%q err=%v", ErrorCode(err), err)
	}
	if err := ReconcileDesktop(context.Background(), root, key, nil); ErrorCode(err) != "invalid_request" {
		t.Fatalf("nil verifier error code=%q err=%v", ErrorCode(err), err)
	}
}

func TestRecoveryJournalInputRequiresExactBoundedHostKey(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{0, 31, 33, 8192} {
		if validRecoveryJournalInput(root, make([]byte, size)) {
			t.Fatalf("accepted key length%d", size)
		}
	}
	if !validRecoveryJournalInput(root, make([]byte, 32)) {
		t.Fatal("refused exact private host inputs")
	}
	if validRecoveryJournalInput(strings.Repeat("x", 4097), make([]byte, 32)) {
		t.Fatal("accepted oversized journal path")
	}
}
