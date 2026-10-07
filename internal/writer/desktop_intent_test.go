package writer

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopIntentCrashClosePersistsAndBlocksDifferentJournal(t *testing.T) {
	root, identity := testDesktopRoot(t)
	binding := testDesktopBinding(1)
	lease, err := acquireDesktopAt(context.Background(), identity, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Begin(binding); err != nil {
		t.Fatal(err)
	}
	if err := lease.MarkDirty(); err != nil {
		t.Fatalf("MarkDirty with owned intent: %v", err)
	}
	if err := lease.MarkDirty(); err != nil {
		t.Fatalf("idempotent MarkDirty with owned intent: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireDesktopAt(context.Background(), identity, root); !errors.Is(err, ErrDirty) {
		t.Fatalf("acquire after crash-like close = %v, want ErrDirty", err)
	}
	called := false
	if err := reconcileDesktopAt(context.Background(), identity, root, testDesktopBinding(2), func(context.Context) error {
		called = true
		return nil
	}); !errors.Is(err, ErrDirty) {
		t.Fatalf("reconcile different journal = %v, want ErrDirty", err)
	}
	if called {
		t.Fatal("verifier called for a different journal binding")
	}
}

func TestDesktopIntentCompleteClearsExactOwnedMarker(t *testing.T) {
	root, identity := testDesktopRoot(t)
	lease, err := acquireDesktopAt(context.Background(), identity, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Begin(testDesktopBinding(3)); err != nil {
		t.Fatal(err)
	}
	if err := lease.Complete(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := acquireDesktopAt(context.Background(), identity, root)
	if err != nil {
		t.Fatalf("acquire after exact completion: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopIntentCompleteRejectsWrongTokenBindingAndMissingMarker(t *testing.T) {
	for _, scenario := range []string{"wrong-token", "wrong-binding", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			root, identity := testDesktopRoot(t)
			lease, err := acquireDesktopAt(context.Background(), identity, root)
			if err != nil {
				t.Fatal(err)
			}
			if err := lease.Begin(testDesktopBinding(4)); err != nil {
				t.Fatal(err)
			}
			path := desktopIntentPath(root, identity)
			switch scenario {
			case "wrong-token":
				lease.intent.Token = strings.Repeat("0", 64)
			case "wrong-binding":
				wrongBinding := testDesktopBinding(5)
				lease.intent.Binding = hex.EncodeToString(wrongBinding[:])
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := lease.Complete(); err == nil {
				t.Fatal("Complete accepted a non-owned or missing marker")
			}
			_ = lease.Close()
		})
	}
}

func TestDesktopIntentCompleteRejectsTamperedCorruptDuplicateAndOversize(t *testing.T) {
	for _, scenario := range []string{"tampered", "corrupt", "duplicate", "oversize", "unsafe-mode"} {
		t.Run(scenario, func(t *testing.T) {
			root, identity := testDesktopRoot(t)
			lease, err := acquireDesktopAt(context.Background(), identity, root)
			if err != nil {
				t.Fatal(err)
			}
			if err := lease.Begin(testDesktopBinding(6)); err != nil {
				t.Fatal(err)
			}
			path := desktopIntentPath(root, identity)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "tampered":
				data = []byte(strings.Replace(string(data), `"version":1`, `"version":2`, 1))
			case "corrupt":
				data = []byte("{")
			case "duplicate":
				data = []byte(strings.Replace(string(data), `"version":1`, `"version":1,"version":1`, 1))
			case "oversize":
				data = []byte(strings.Repeat("x", maxDesktopIntentBytes+1))
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if scenario == "unsafe-mode" {
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := lease.Complete(); err == nil {
				t.Fatal("Complete cleared an invalid marker")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("invalid marker was removed: %v", err)
			}
			_ = lease.Close()
		})
	}
}

func TestDesktopIntentRecoveryVerifierControlsClear(t *testing.T) {
	for _, verifierErr := range []error{errors.New("verification failed"), nil} {
		root, identity := testDesktopRoot(t)
		lease, err := acquireDesktopAt(context.Background(), identity, root)
		if err != nil {
			t.Fatal(err)
		}
		binding := testDesktopBinding(7)
		if err := lease.Begin(binding); err != nil {
			t.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
		called := false
		err = reconcileDesktopAt(context.Background(), identity, root, binding, func(context.Context) error {
			called = true
			return verifierErr
		})
		if err != verifierErr {
			t.Fatalf("reconcile error = %v, want %v", err, verifierErr)
		}
		if !called {
			t.Fatal("matching recovery did not invoke verifier")
		}
		_, statErr := os.Lstat(desktopIntentPath(root, identity))
		if verifierErr != nil && statErr != nil {
			t.Fatal("failed verifier cleared the intent marker")
		}
		if verifierErr == nil && !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("successful verifier left marker: %v", statErr)
		}
	}
}

func TestDesktopIntentRecoveryRejectsInvalidMarkersBeforeVerifier(t *testing.T) {
	for _, content := range [][]byte{[]byte("dirty-v1\n"), []byte("{"), []byte(strings.Repeat("x", maxDesktopIntentBytes+1))} {
		root, identity := testDesktopRoot(t)
		path := desktopIntentPath(root, identity)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		called := false
		if err := reconcileDesktopAt(context.Background(), identity, root, testDesktopBinding(8), func(context.Context) error {
			called = true
			return nil
		}); err == nil {
			t.Fatal("reconcile accepted invalid marker")
		}
		if called {
			t.Fatal("verifier called with invalid marker")
		}
	}
}

func TestDesktopJournalBindingUsesExactPrivateRootAndKey(t *testing.T) {
	root := filepath.Join(privateJournalParent(t), "journal")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	key := bytesOf(1, 32)
	first, err := DesktopJournalBinding(root, key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DesktopJournalBinding(root, bytesOf(2, 32))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different journal keys produced the same binding")
	}
	otherRoot := filepath.Join(privateJournalParent(t), "journal")
	if err := os.Mkdir(otherRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	third, err := DesktopJournalBinding(otherRoot, key)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("different journal roots produced the same binding")
	}
	if _, err := DesktopJournalBinding(root, []byte("short")); err == nil {
		t.Fatal("accepted short key")
	}
	if _, err := DesktopJournalBinding(root+"/../journal", key); err == nil {
		t.Fatal("accepted noncanonical root")
	}
	symlink := filepath.Join(t.TempDir(), "journal-link")
	if err := os.Symlink(root, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := DesktopJournalBinding(symlink, key); err == nil {
		t.Fatal("accepted symlink root")
	}
}

func testDesktopBinding(seed byte) [32]byte {
	var binding [32]byte
	for i := range binding {
		binding[i] = seed
	}
	return binding
}

func bytesOf(value byte, count int) []byte {
	data := make([]byte, count)
	for i := range data {
		data[i] = value
	}
	return data
}

func TestDesktopJournalBindingRejectsReplaceableAncestor(t *testing.T) {
	parent := filepath.Join(privateJournalParent(t), "replaceable")
	root := filepath.Join(parent, "journal")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := DesktopJournalBinding(root, bytes.Repeat([]byte{7}, 32)); err == nil {
		t.Fatal("accepted replaceable ancestor")
	}
}

func privateJournalParent(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	return parent
}
