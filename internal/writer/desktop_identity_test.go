package writer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func TestDesktopLockHelperProcess(t *testing.T) {
	if os.Getenv("COMUSE_DESKTOP_HELPER") != "1" {
		return
	}
	uid, _ := strconv.ParseUint(os.Getenv("COMUSE_DESKTOP_UID"), 10, 32)
	identity := desktopIdentity{uid: uint32(uid), sessionID: 91}
	lease, err := acquireDesktopAt(context.Background(), identity, os.Getenv("COMUSE_DESKTOP_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { if err := lease.Close(); err != nil { t.Error(err) } }()
	if err := os.WriteFile(os.Getenv("COMUSE_DESKTOP_READY"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(os.Getenv("COMUSE_DESKTOP_RELEASE")); err == nil {
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(4)
}

func testDesktopRoot(t *testing.T) (string, desktopIdentity) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "canonical")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root, desktopIdentity{uid: uint32(os.Getuid()), sessionID: 91}
}

func TestDesktopLockSerializesSameVerifiedSession(t *testing.T) {
	root, identity := testDesktopRoot(t)
	first, err := acquireDesktopAt(context.Background(), identity, root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { if err := first.Close(); err != nil { t.Error(err) } }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := acquireDesktopAt(ctx, identity, root); err == nil {
		t.Fatal("same desktop session acquired a second writer lock")
	}
	otherSession := identity
	otherSession.sessionID++
	second, err := acquireDesktopAt(context.Background(), otherSession, root)
	if err != nil {
		t.Fatalf("distinct verified GUI session contended: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopLockExcludesAnotherProcessAcrossJournalDirectories(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("desktop lock requires a non-root owner")
	}
	root, identity := testDesktopRoot(t)
	ready := filepath.Join(root, "child-ready")
	release := filepath.Join(root, "child-release")
	command := exec.Command(os.Args[0], "-test.run=^TestDesktopLockHelperProcess$")
	command.Env = append(os.Environ(),
		"COMUSE_DESKTOP_HELPER=1",
		"COMUSE_DESKTOP_ROOT="+root,
		"COMUSE_DESKTOP_UID="+strconv.FormatUint(uint64(identity.uid), 10),
		"COMUSE_DESKTOP_READY="+ready,
		"COMUSE_DESKTOP_RELEASE="+release,
		"COMUSE_TEST_JOURNAL="+filepath.Join(t.TempDir(), "journal-a"),
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.WriteFile(release, []byte("release"), 0o600)
		_ = command.Wait()
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			_, err := acquireDesktopAt(ctx, identity, root)
			cancel()
			if err == nil {
				t.Fatal("different process and journal path bypassed desktop exclusion")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child process did not acquire canonical desktop lock")
}

func TestDesktopDirtyMarkerPersistsAcrossLeaseRestart(t *testing.T) {
	root, identity := testDesktopRoot(t)
	lease, err := acquireDesktopAt(context.Background(), identity, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.MarkDirty(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireDesktopAt(context.Background(), identity, root); err != ErrDirty {
		t.Fatalf("reopen error = %v, want ErrDirty", err)
	}
}

func TestDesktopIdentityFailsClosedOnUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("native identity resolution requires a qualified GUI fixture")
	}
	if _, err := resolveDesktopIdentity(); err == nil {
		t.Fatal("unsupported platform resolved a trusted GUI identity")
	}
}
