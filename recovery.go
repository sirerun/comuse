package comuse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/sirerun/comuse/internal/writer"
)

const desktopRecoveryTimeout = 30 * time.Second

type recoveryJournalLease interface {
	ReconcileDirty(context.Context, string, writer.ReconcileFunc) error
	Close(context.Context) error
}

type recoveryJournalAcquire func(context.Context, string) (recoveryJournalLease, error)
type recoveryBindingFunc func(string, []byte) ([32]byte, error)
type recoveryDesktopFunc func(context.Context, [32]byte, func(context.Context) error) error

// ReconcileDesktop recovers trusted desktop and journal state after the host
// has drained and closed all earlier backends and held inputs. verify must
// confirm that cleanup; it is required, runs even if the journal is not dirty,
// and must honor ctx. The journal directory and key are protected host inputs
// and must never come from a caller-controlled request.
func ReconcileDesktop(ctx context.Context, journalDirectory string, journalKey []byte, verify func(context.Context) error) error {
	if ctx == nil || verify == nil || !validRecoveryJournalInput(journalDirectory, journalKey) {
		return coreError("invalid_request")
	}
	bounded, cancel := context.WithTimeout(ctx, desktopRecoveryTimeout)
	defer cancel()

	err := reconcileDesktop(bounded, journalDirectory, journalKey, verify, func(ctx context.Context, root string) (recoveryJournalLease, error) {
		return writer.Acquire(ctx, root)
	}, writer.DesktopJournalBinding, writer.ReconcileDesktop)
	return safeRecoveryError(err)
}

func validRecoveryJournalInput(root string, key []byte) bool {
	if len(key) < 32 || root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return false
	}
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return false
	}
	return true
}

func reconcileDesktop(ctx context.Context, journalDirectory string, journalKey []byte, verify func(context.Context) error, acquire recoveryJournalAcquire, bind recoveryBindingFunc, reconcile recoveryDesktopFunc) (returnedErr error) {
	if ctx == nil || verify == nil || acquire == nil || bind == nil || reconcile == nil {
		return errors.New("desktop recovery requires context, journal, binding, and cleanup functions")
	}
	lease, err := acquire(ctx, journalDirectory)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			returnedErr = errors.Join(returnedErr, lease.Close(ctx))
		}
	}()

	binding, err := bind(journalDirectory, journalKey)
	if err != nil {
		return err
	}
	var journalCloseErr error
	closeJournal := func() error {
		if closed {
			return journalCloseErr
		}
		closed = true
		journalCloseErr = lease.Close(ctx)
		return journalCloseErr
	}
	return reconcile(ctx, binding, func(verifyCtx context.Context) error {
		if verifyCtx == nil {
			return errors.New("desktop recovery verifier requires a context")
		}
		if err := verifyCtx.Err(); err != nil {
			return err
		}
		if err := verify(verifyCtx); err != nil {
			return err
		}
		if err := verifyCtx.Err(); err != nil {
			return err
		}
		if err := lease.ReconcileDirty(verifyCtx, "trusted desktop cleanup verified", func(context.Context, []writer.HeldStatusRecord) error {
			return nil
		}); err != nil {
			return err
		}
		return closeJournal()
	})
}

func safeRecoveryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return coreError("cancelled")
	}
	if errors.Is(err, writer.ErrDesktopIdentityUnavailable) || errors.Is(err, writer.ErrUnsupportedLock) {
		return coreError("backend_unavailable")
	}
	if errors.Is(err, writer.ErrDirty) {
		return coreError("unknown_outcome")
	}
	return coreError("internal_error")
}
