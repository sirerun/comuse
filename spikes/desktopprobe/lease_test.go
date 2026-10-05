//go:build darwin || linux

package desktopprobe

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func testLease(t *testing.T) (*Lease, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := Acquire(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := lease.Close(ctx); err != nil {
			t.Errorf("closing test lease: %v", err)
		}
	})
	return lease, root
}

func TestBeginFinishReplayAndBinding(t *testing.T) {
	lease, root := testLease(t)
	var binding [32]byte
	binding[0] = 1
	ticket, prior, err := lease.Begin("action-1", binding)
	if err != nil || ticket == nil || prior != nil {
		t.Fatalf("Begin = (%v, %v, %v)", ticket, prior, err)
	}
	if err := lease.Finish(ticket, OutcomeApplied); err != nil {
		t.Fatal(err)
	}
	if err := lease.Finish(ticket, OutcomeApplied); !errors.Is(err, ErrTicketUsed) {
		t.Fatalf("second Finish error = %v, want ErrTicketUsed", err)
	}
	if ticket, prior, err = lease.Begin("action-1", binding); err != nil || ticket != nil || prior == nil || prior.Outcome != OutcomeApplied {
		t.Fatalf("replay Begin = (%v, %v, %v), want prior applied", ticket, prior, err)
	}
	binding[0] = 2
	if _, _, err := lease.Begin("action-1", binding); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("changed binding error = %v, want ErrBindingMismatch", err)
	}
	_ = lease.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reopened, err := Acquire(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	binding[0] = 1
	if ticket, prior, err := reopened.Begin("action-1", binding); err != nil || ticket != nil || prior == nil || prior.Outcome != OutcomeApplied {
		t.Fatalf("restart replay = (%v, %v, %v), want prior applied", ticket, prior, err)
	}
}

func TestInflightBecomesUnknownAfterRestart(t *testing.T) {
	lease, root := testLease(t)
	var binding [32]byte
	binding[0] = 7
	if ticket, _, err := lease.Begin("crash-action", binding); err != nil || ticket == nil {
		t.Fatalf("Begin = (%v, %v)", ticket, err)
	}
	if retry, prior, err := lease.Begin("crash-action", binding); err != nil || retry != nil || prior == nil || prior.Outcome != OutcomeUnknown {
		t.Fatalf("in-flight retry = (%v, %v, %v), want prior unknown", retry, prior, err)
	}
	// Simulate process exit without the normal Close path.
	_ = releaseFileLock(lease.lockFile)
	_ = lease.lockFile.Close()
	lease.lockFile = nil
	lease.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	restarted, err := Acquire(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close(context.Background())
	ticket, prior, err := restarted.Begin("crash-action", binding)
	if err != nil || ticket != nil || prior == nil || prior.Outcome != OutcomeUnknown {
		t.Fatalf("restart Begin = (%v, %v, %v), want prior unknown", ticket, prior, err)
	}
}

func TestHeldCleanupFailureDirtiesAndRequiresVerifier(t *testing.T) {
	lease, _ := testLease(t)
	held, err := lease.RegisterHeldInput("key-down", func(context.Context) error {
		return errors.New("synthetic cleanup failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := held.Release(context.Background()); err == nil {
		t.Fatal("failed cleanup returned nil")
	}
	var binding [32]byte
	if _, _, err := lease.Begin("next-action", binding); !errors.Is(err, ErrDirty) {
		t.Fatalf("Begin after failed release = %v, want ErrDirty", err)
	}
	verified := false
	err = lease.ReconcileDirty(context.Background(), "host verified no held keys", func(_ context.Context, held []HeldStatusRecord) error {
		verified = true
		if len(held) != 1 || held[0].ID != "key-down" || held[0].Status != HeldDirty {
			t.Fatalf("verifier records = %#v", held)
		}
		return nil
	})
	if err != nil || !verified {
		t.Fatalf("ReconcileDirty = %v, verifier called=%v", err, verified)
	}
	if ticket, _, err := lease.Begin("next-action", binding); err != nil || ticket == nil {
		t.Fatalf("Begin after verified reconciliation = (%v, %v)", ticket, err)
	}
}

func TestRestartWithUnresolvedHeldInputStaysDirtyUntilVerified(t *testing.T) {
	lease, root := testLease(t)
	if _, err := lease.RegisterHeldInput("mouse-held", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	_ = releaseFileLock(lease.lockFile)
	_ = lease.lockFile.Close()
	lease.lockFile = nil
	lease.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	restarted, err := Acquire(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close(context.Background())
	var binding [32]byte
	if _, _, err := restarted.Begin("blocked-action", binding); !errors.Is(err, ErrDirty) {
		t.Fatalf("Begin after unresolved held marker = %v, want ErrDirty", err)
	}
	err = restarted.ReconcileDirty(context.Background(), "trusted host inspected current input state", func(_ context.Context, rows []HeldStatusRecord) error {
		if len(rows) != 1 || rows[0].Status != HeldUnknown {
			t.Fatalf("reconciliation rows = %#v", rows)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCloseTimeoutKeepsWriterLockUntilCleanupReturns(t *testing.T) {
	lease, root := testLease(t)
	release := make(chan struct{})
	if _, err := lease.RegisterHeldInput("blocked-cleanup", func(context.Context) error {
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	errCh := make(chan error, 1)
	go func() { errCh <- lease.Close(closeCtx) }()
	err := <-errCh
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want deadline", err)
	}
	contenderCtx, contenderCancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	_, acquireErr := Acquire(contenderCtx, root)
	contenderCancel()
	if !errors.Is(acquireErr, context.DeadlineExceeded) {
		t.Fatalf("Acquire while cleanup is still running = %v, want deadline", acquireErr)
	}
	close(release)
	retryCtx, retryCancel := context.WithTimeout(context.Background(), time.Second)
	defer retryCancel()
	if err := lease.Close(retryCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retry Close after expired cleanup = %v, want persisted cleanup failure", err)
	}
	newLease, err := Acquire(retryCtx, root)
	if err != nil {
		t.Fatalf("Acquire after safe Close: %v", err)
	}
	if err := newLease.Close(retryCtx); err != nil {
		t.Fatal(err)
	}
}

func TestCloseTimeoutKeepsWriterLockUntilCleanupReturns(t *testing.T) {
	lease, root := testLease(t)
	release := make(chan struct{})
	if _, err := lease.RegisterHeldInput("blocked-cleanup", func(context.Context) error {
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	errCh := make(chan error, 1)
	go func() { errCh <- lease.Close(closeCtx) }()
	err := <-errCh
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want deadline", err)
	}
	contenderCtx, contenderCancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	_, acquireErr := Acquire(contenderCtx, root)
	contenderCancel()
	if !errors.Is(acquireErr, context.DeadlineExceeded) {
		t.Fatalf("Acquire while cleanup is still running = %v, want deadline", acquireErr)
	}
	close(release)
	retryCtx, retryCancel := context.WithTimeout(context.Background(), time.Second)
	defer retryCancel()
	if err := lease.Close(retryCtx); err != nil {
		t.Fatalf("retry Close after cleanup returned: %v", err)
	}
	newLease, err := Acquire(retryCtx, root)
	if err != nil {
		t.Fatalf("Acquire after safe Close: %v", err)
	}
	if err := newLease.Close(retryCtx); err != nil {
		t.Fatal(err)
	}
}

func TestBindingCommitmentUsesCallerKey(t *testing.T) {
	key := make([]byte, 32)
	key[0] = 1
	first, err := BindingCommitment(key, []byte("opaque binding"))
	if err != nil {
		t.Fatal(err)
	}
	key[0] = 2
	second, err := BindingCommitment(key, []byte("opaque binding"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("commitment did not depend on the caller key")
	}
}

func TestExpiredReplayMovesToPermanentTombstone(t *testing.T) {
	lease, _ := testLease(t)
	var binding [32]byte
	binding[0] = 9
	encoded := base64.RawURLEncoding.EncodeToString(binding[:])
	lease.mu.Lock()
	lease.state.Actions["old-action"] = actionRecord{Binding: encoded, Outcome: OutcomeApplied, Started: time.Now().Add(-2 * terminalTTL), Updated: time.Now().Add(-2 * terminalTTL)}
	if err := lease.pruneExpired(time.Now()); err != nil {
		lease.mu.Unlock()
		t.Fatal(err)
	}
	if err := lease.saveLocked(); err != nil {
		lease.mu.Unlock()
		t.Fatal(err)
	}
	lease.mu.Unlock()
	ticket, prior, err := lease.Begin("old-action", binding)
	if err != nil || ticket != nil || prior == nil || prior.Outcome != OutcomeApplied {
		t.Fatalf("expired action replay = (%v, %v, %v), want permanent prior outcome", ticket, prior, err)
	}
	binding[0] = 10
	if _, _, err := lease.Begin("old-action", binding); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("changed binding against tombstone = %v, want ErrBindingMismatch", err)
	}
}

func TestTombstoneCapacityFailsClosed(t *testing.T) {
	lease, _ := testLease(t)
	lease.mu.Lock()
	for i := 0; i < maxTombstones; i++ {
		lease.state.Tombstones[fmt.Sprintf("retired-%d", i)] = actionTombstone{Binding: "commitment", Outcome: OutcomeUnknown}
	}
	lease.state.Actions["expired"] = actionRecord{Binding: "commitment", Outcome: OutcomeApplied, Updated: time.Now().Add(-2 * terminalTTL)}
	err := lease.pruneExpired(time.Now())
	if err == nil {
		lease.mu.Unlock()
		t.Fatal("expired record was pruned after tombstone capacity was reached")
	}
	if _, exists := lease.state.Actions["expired"]; !exists {
		lease.mu.Unlock()
		t.Fatal("capacity failure discarded replay record")
	}
	lease.state.Tombstones = make(map[string]actionTombstone)
	delete(lease.state.Actions, "expired")
	lease.mu.Unlock()
}

func TestBindingCommitmentNeverPersistsOpaqueActionBinding(t *testing.T) {
	lease, root := testLease(t)
	key := make([]byte, 32)
	key[0] = 3
	opaque := []byte("private synthetic action payload")
	commitment, err := BindingCommitment(key, opaque)
	if err != nil {
		t.Fatal(err)
	}
	ticket, _, err := lease.Begin("safe-id", commitment)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Finish(ticket, OutcomeNotApplied); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "replay-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, opaque) {
		t.Fatal("replay ledger contains opaque action binding text")
	}
}

func TestWriterLockAcrossTwoChildProcesses(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ready := filepath.Join(t.TempDir(), "ready")
	helper := func(role string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWriterLockProcessHelper$")
		cmd.Env = append(os.Environ(), "DESKTOPPROBE_CHILD_ROLE="+role, "DESKTOPPROBE_CHILD_ROOT="+root, "DESKTOPPROBE_CHILD_READY="+ready)
		return cmd
	}
	first := helper("holder")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = first.Process.Kill()
			_ = first.Wait()
			t.Fatal("holder child did not signal lock acquisition")
		}
		time.Sleep(10 * time.Millisecond)
	}
	second := helper("contender")
	if output, err := second.CombinedOutput(); err != nil {
		_ = first.Process.Kill()
		_ = first.Wait()
		t.Fatalf("contender child failed: %v: %s", err, output)
	}
	if err := first.Wait(); err != nil {
		t.Fatalf("holder child failed: %v", err)
	}
}

func TestWriterLockProcessHelper(t *testing.T) {
	role := os.Getenv("DESKTOPPROBE_CHILD_ROLE")
	if role == "" {
		return
	}
	root := os.Getenv("DESKTOPPROBE_CHILD_ROOT")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	switch role {
	case "holder":
		lease, err := Acquire(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("DESKTOPPROBE_CHILD_READY"), []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		if err := lease.Close(ctx); err != nil {
			t.Fatal(err)
		}
	case "contender":
		short, stop := context.WithTimeout(context.Background(), 120*time.Millisecond)
		defer stop()
		lease, err := Acquire(short, root)
		if err == nil {
			_ = lease.Close(context.Background())
			t.Fatal("contender acquired the writer lock while holder was active")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("contender error = %v, want context deadline", err)
		}
	default:
		t.Fatalf("unknown child role %q", role)
	}
}
