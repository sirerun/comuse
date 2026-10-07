package comuse

import (
	"context"
	"github.com/sirerun/comuse/internal/writer"
)

type desktopAuthority interface {
	Begin([32]byte) error
	Complete() error
	MarkDirty() error
	Close() error
}

func acquireDesktopAuthority(ctx context.Context) (desktopAuthority, error) {
	return writer.AcquireDesktop(ctx)
}
func reserveDesktopQuota(ctx context.Context, commitment [32]byte) error {
	store, err := writer.OpenQuotaStore()
	if err != nil {
		return err
	}
	return store.Reserve(ctx, commitment)
}

func (s *Session) journalQuarantined(lease *writer.Lease) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, held := range s.quarantined {
		if held == lease {
			return true
		}
	}
	return false
}
