package writer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestQuotaHelperProcess(t *testing.T) {
	if os.Getenv("COMUSE_QUOTA_HELPER") != "1" {
		return
	}
	root := os.Getenv("COMUSE_QUOTA_ROOT")
	uid, _ := strconv.ParseUint(os.Getenv("COMUSE_QUOTA_UID"), 10, 32)
	commitment := sha256.Sum256([]byte(os.Getenv("COMUSE_QUOTA_ACTION")))
	store := newQuotaStoreAt(root, uint32(uid))
	now := time.UnixMilli(1_800_000_000_000).UTC()
	if err := store.reserveAt(context.Background(), commitment, now); err != nil {
		if errors.Is(err, ErrQuotaExhausted) {
			os.Exit(42)
		}
		t.Fatalf("helper reservation: %v", err)
	}
	os.Exit(0)
}

func runQuotaHelper(t *testing.T, root, action, journal string) error {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestQuotaHelperProcess$")
	command.Env = append(os.Environ(),
		"COMUSE_QUOTA_HELPER=1",
		"COMUSE_QUOTA_ROOT="+root,
		"COMUSE_QUOTA_UID="+strconv.Itoa(os.Getuid()),
		"COMUSE_QUOTA_ACTION="+action,
		"COMUSE_TEST_JOURNAL="+journal,
	)
	err := command.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 42 {
		return ErrQuotaExhausted
	}
	return err
}

func TestQuotaPersistsAcrossProcessesAndIgnoresJournalPath(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("writer quota requires a non-root owner")
	}
	root := filepath.Join(t.TempDir(), "canonical")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := newQuotaStoreAt(root, uint32(os.Getuid()))
	now := time.UnixMilli(1_800_000_000_000).UTC()
	for i := 0; i < MaxActionsPerMinute-1; i++ {
		commitment := sha256.Sum256([]byte(fmt.Sprintf("action-%d", i)))
		if err := store.reserveAt(context.Background(), commitment, now); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	if err := runQuotaHelper(t, root, "action-59", filepath.Join(t.TempDir(), "journal-a")); err != nil {
		t.Fatalf("different-process reservation at slot 60: %v", err)
	}
	if err := runQuotaHelper(t, root, "action-60", filepath.Join(t.TempDir(), "journal-b")); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("fresh journal path reset per-UID quota: %v", err)
	}
}

func TestQuotaExactReplayRollbackAndRollingWindow(t *testing.T) {
	root := filepath.Join(t.TempDir(), "canonical")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := newQuotaStoreAt(root, uint32(os.Getuid()))
	start := time.UnixMilli(1_800_000_000_000).UTC()
	commitments := make([][32]byte, MaxActionsPerMinute)
	for i := range commitments {
		commitments[i] = sha256.Sum256([]byte(fmt.Sprintf("attempt-%d", i)))
		if err := store.reserveAt(context.Background(), commitments[i], start); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	if err := store.reserveAt(context.Background(), commitments[0], start.Add(time.Second)); err != nil {
		t.Fatalf("exact replay consumed another quota slot: %v", err)
	}
	if err := store.reserveAt(context.Background(), sha256.Sum256([]byte("attempt-over")), start.Add(-10*time.Second)); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("clock rollback restored quota: %v", err)
	}
	if err := store.reserveAt(context.Background(), sha256.Sum256([]byte("attempt-after-window")), start.Add(61*time.Second)); err != nil {
		t.Fatalf("rolling window did not admit after expiry: %v", err)
	}
}

func TestQuotaCorruptionFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "canonical")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, fmt.Sprintf("quota-%d.json", os.Getuid()))
	if err := os.WriteFile(statePath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newQuotaStoreAt(root, uint32(os.Getuid()))
	if err := store.reserveAt(context.Background(), sha256.Sum256([]byte("must-fail")), time.Now()); err == nil {
		t.Fatal("corrupt durable quota was reset")
	}
}
