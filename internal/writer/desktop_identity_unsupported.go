//go:build !darwin && !linux

package writer

import (
	"errors"
	"os"
)

func resolveDesktopIdentity() (desktopIdentity, error) {
	return desktopIdentity{}, ErrDesktopIdentityUnavailable
}

func verifyUIDOwner(info os.FileInfo, uid uint32) error {
	return errors.New("canonical desktop identity is unsupported on this platform")
}
