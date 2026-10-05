//go:build !darwin && !linux

package desktopprobe

import (
	"context"
	"os"
)

func writerLockAvailable() bool { return false }

func verifyCurrentOwner(os.FileInfo) error    { return ErrUnsupportedLock }
func openLockFile(string) (*os.File, error)   { return nil, ErrUnsupportedLock }
func openLedgerFile(string) (*os.File, error) { return nil, ErrUnsupportedLock }

func acquireFileLock(context.Context, *os.File) error { return ErrUnsupportedLock }
func releaseFileLock(*os.File) error                  { return nil }
