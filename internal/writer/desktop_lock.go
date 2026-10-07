package writer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type DesktopLease struct {
	identity    desktopIdentity
	root        string
	lock        *os.File
	closed      bool
	intent      *desktopIntent
	beginFailed bool
}

// AcquireDesktop reserves this verified GUI login session independently of
// the configurable action journal directory.
func AcquireDesktop(ctx context.Context) (*DesktopLease, error) {
	identity, err := resolveDesktopIdentity()
	if err != nil {
		return nil, ErrDesktopIdentityUnavailable
	}
	root, err := desktopRoot(identity)
	if err != nil {
		return nil, ErrDesktopIdentityUnavailable
	}
	if err := secureDesktopRoot(root, identity.uid); err != nil {
		return nil, fmt.Errorf("protect canonical desktop state: %w", err)
	}
	return acquireDesktopAt(ctx, identity, root)
}

func acquireDesktopAt(ctx context.Context, identity desktopIdentity, root string) (*DesktopLease, error) {
	return acquireDesktopAtMode(ctx, identity, root, false)
}

// acquireDesktopForRecoveryAt is intentionally private. Only trusted recovery
// code may bypass the marker check, and it must validate the marker before
// doing any recovery work.
func acquireDesktopForRecoveryAt(ctx context.Context, identity desktopIdentity, root string) (*DesktopLease, error) {
	return acquireDesktopAtMode(ctx, identity, root, true)
}

func acquireDesktopAtMode(ctx context.Context, identity desktopIdentity, root string, recovery bool) (*DesktopLease, error) {
	if ctx == nil {
		return nil, errors.New("desktop lock requires a context")
	}
	if identity.uid == 0 || identity.sessionID == 0 || !writerLockAvailable() {
		return nil, ErrDesktopIdentityUnavailable
	}
	if err := verifyPrivateStateRoot(root, identity.uid); err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%d-%d", identity.uid, identity.sessionID)
	lockPath := filepath.Join(root, "desktop-"+key+".lock")
	if err := verifyExistingRegular(lockPath, identity.uid); err != nil {
		return nil, err
	}
	file, err := openLockFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("open canonical desktop lock: %w", err)
	}
	info, err := file.Stat()
	if err == nil {
		err = verifyUIDOwner(info, identity.uid)
	}
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("canonical desktop lock must be a regular file")
	}
	if err == nil {
		err = file.Chmod(0o600)
	}
	if err == nil {
		err = acquireFileLock(ctx, file)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	for _, suffix := range []string{"dirty", "intent"} {
		if recovery && suffix == "intent" {
			continue
		}
		markerPath := filepath.Join(root, "desktop-"+key+"."+suffix)
		if err := verifyExistingRegular(markerPath, identity.uid); err != nil {
			_ = releaseFileLock(file)
			_ = file.Close()
			return nil, err
		}
		if _, err := os.Lstat(markerPath); err == nil {
			_ = releaseFileLock(file)
			_ = file.Close()
			return nil, ErrDirty
		} else if !errors.Is(err, os.ErrNotExist) {
			_ = releaseFileLock(file)
			_ = file.Close()
			return nil, fmt.Errorf("inspect canonical desktop %s marker: %w", suffix, err)
		}
	}
	return &DesktopLease{identity: identity, root: root, lock: file}, nil
}

func (l *DesktopLease) MarkDirty() error {
	if l == nil || l.closed || l.lock == nil {
		return ErrClosed
	}
	key := fmt.Sprintf("%d-%d", l.identity.uid, l.identity.sessionID)
	if l.intent != nil {
		path := desktopIntentPath(l.root, l.identity)
		marker, err := readDesktopIntent(path, l.identity.uid)
		if err != nil {
			return err
		}
		if marker != *l.intent {
			return ErrDirty
		}
		return nil
	}
	path := filepath.Join(l.root, "desktop-"+key+".dirty")
	if err := verifyExistingRegular(path, l.identity.uid); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return ErrDirty
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := atomicWrite(path, []byte("dirty-v1\n")); err != nil {
		return err
	}
	return syncDesktopDirectory(l.root)
}

func (l *DesktopLease) Close() error {
	if l == nil || l.closed {
		return nil
	}
	l.closed = true
	if l.lock == nil {
		return nil
	}
	return errors.Join(releaseFileLock(l.lock), l.lock.Close())
}

func verifyPrivateStateRoot(root string, uid uint32) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect canonical desktop state root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("canonical desktop state root must be a real directory")
	}
	if err := verifyUIDOwner(info, uid); err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("canonical desktop state root permissions must exclude group and other access")
	}
	return nil
}

func verifyExistingRegular(path string, uid uint32) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect canonical desktop state file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("canonical desktop state file must be a regular non-symlink")
	}
	if err := verifyUIDOwner(info, uid); err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("canonical desktop state file permissions must exclude group and other access")
	}
	return nil
}
