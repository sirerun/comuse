//go:build linux

package writer

import (
	"errors"
	"os"
	"syscall"
)

func resolveDesktopIdentity() (desktopIdentity, error) {
	return desktopIdentity{}, ErrDesktopIdentityUnavailable
}

func verifyUIDOwner(info os.FileInfo, uid uint32) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint32(stat.Uid) != uid {
		return errors.New("canonical desktop state is not owned by the verified user")
	}
	return nil
}
