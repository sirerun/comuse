package writer

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrDesktopIdentityUnavailable = errors.New("trusted desktop identity is unavailable")

type desktopIdentity struct {
	uid       uint32
	sessionID uint32
}

// desktopRoot resolves a fixed application-owned path using the account
// database entry for the verified UID. It never consults HOME or WriterDirectory.
func desktopRoot(identity desktopIdentity) (string, error) {
	if identity.uid == 0 || identity.sessionID == 0 {
		return "", ErrDesktopIdentityUnavailable
	}
	account, err := user.LookupId(strconv.FormatUint(uint64(identity.uid), 10))
	if err != nil || account.Uid != strconv.FormatUint(uint64(identity.uid), 10) || account.HomeDir == "" || !filepath.IsAbs(account.HomeDir) {
		return "", ErrDesktopIdentityUnavailable
	}
	if strings.ContainsRune(account.HomeDir, 0) {
		return "", ErrDesktopIdentityUnavailable
	}
	return filepath.Join(account.HomeDir, "Library", "Application Support", "Comuse", "DesktopState"), nil
}

func secureDesktopRoot(root string, uid uint32) error {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("canonical desktop root is invalid")
	}
	account, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	if err != nil || account.Uid != strconv.FormatUint(uint64(uid), 10) || !filepath.IsAbs(account.HomeDir) {
		return ErrDesktopIdentityUnavailable
	}
	homeParts := strings.Split(strings.TrimPrefix(filepath.Clean(account.HomeDir), string(filepath.Separator)), string(filepath.Separator))
	parts := strings.Split(strings.TrimPrefix(root, string(filepath.Separator)), string(filepath.Separator))
	if len(parts) <= len(homeParts) || filepath.Join(string(filepath.Separator), filepath.Join(parts[:len(homeParts)]...)) != filepath.Clean(account.HomeDir) {
		return errors.New("canonical desktop state must be inside the verified user home")
	}
	current := string(filepath.Separator)
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return errors.New("canonical desktop root contains an invalid component")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			mode := os.FileMode(0o700)
			if index < len(homeParts) { // User home directory is created by the account service.
				return ErrDesktopIdentityUnavailable
			}
			if err := os.Mkdir(current, mode); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("create canonical desktop state directory: %w", err)
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return fmt.Errorf("inspect canonical desktop state directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("canonical desktop state path must contain real directories")
		}
		if index >= len(homeParts)-1 {
			if err := verifyUIDOwner(info, uid); err != nil {
				return err
			}
			if info.Mode().Perm()&0o022 != 0 {
				return errors.New("canonical desktop state ancestor is writable by another account")
			}
		}
		if index == len(parts)-1 && info.Mode().Perm()&0o077 != 0 {
			return errors.New("canonical desktop state root permissions must exclude group and other access")
		}
	}
	return nil
}
