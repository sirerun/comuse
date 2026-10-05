package inputprobe

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirerun/comuse/spikes/desktopprobe"
)

func TestDesktopJournalCreateOnlyReplayPersistsRedactedOutcome(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-state")
	key := bytes.Repeat([]byte{0x42}, 32)
	writer := &desktopWriterAdapter{root: root, journalKey: key, quarantined: make(map[*desktopLeaseAdapter]struct{})}
	journal := &desktopJournalAdapter{journalKey: key}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	baseLease, err := writer.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lease := baseLease.(*desktopLeaseAdapter)
	journalCtx := context.WithValue(ctx, writerLeaseContextKey{}, lease)
	commitment := bytes.Repeat([]byte{0x6a}, 32)
	record := JournalRecord{ActionID: "fixture-action", Commitment: [32]byte{}, Execution: "pending"}
	copy(record.Commitment[:], commitment)
	if _, found, err := journal.Lookup(journalCtx, record.ActionID); err != nil || found {
		t.Fatalf("initial lookup = found %v, err %v", found, err)
	}
	created, err := journal.Begin(journalCtx, record)
	if err != nil || !created {
		t.Fatalf("Begin = created %v, err %v", created, err)
	}
	terminal := record
	terminal.Action = "replace"
	terminal.Execution = "partial"
	terminal.Verification = "failed"
	terminal.StateStatus = "partial"
	terminal.Cleanup = "released"
	terminal.ErrorCode = "postcondition_failed"
	if err := journal.Finish(journalCtx, terminal); err != nil {
		t.Fatal(err)
	}
	if err := baseLease.Release(ctx); err != nil {
		t.Fatal(err)
	}
	ledger, err := os.ReadFile(filepath.Join(root, "replay-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ledger, []byte("synthetic_text_canary")) || bytes.Contains(ledger, []byte("original")) {
		t.Fatalf("ledger contains native response content: %s", ledger)
	}

	second, err := writer.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondLease := second.(*desktopLeaseAdapter)
	secondCtx := context.WithValue(ctx, writerLeaseContextKey{}, secondLease)
	if _, found, err := journal.Lookup(secondCtx, record.ActionID); err != nil || found {
		t.Fatalf("restart lookup before binding check = found %v, err %v", found, err)
	}
	created, err = journal.Begin(secondCtx, record)
	if err != nil || created {
		t.Fatalf("replay Begin = created %v, err %v", created, err)
	}
	prior, found, err := journal.Lookup(secondCtx, record.ActionID)
	if err != nil || !found || prior.Execution != "partial" || prior.ErrorCode != "postcondition_failed" || prior.Verification != "failed" {
		t.Fatalf("replay prior = %+v, found %v, err %v", prior, found, err)
	}
	if err := second.Release(ctx); err != nil {
		t.Fatal(err)
	}
	verifyLease, err := desktopprobe.Acquire(ctx, root)
	if err != nil {
		t.Fatalf("writer lock did not release after replay: %v", err)
	}
	_ = verifyLease.Close(ctx)
}

type drainTestBackend struct{ closed bool }

func (backend *drainTestBackend) CloseAndDrain(context.Context) error {
	backend.closed = true
	return nil
}

func TestQuarantinedDesktopLeaseWaitsForNativeDrain(t *testing.T) {
	root := filepath.Join(t.TempDir(), "quarantine-state")
	writer := &desktopWriterAdapter{root: root, journalKey: bytes.Repeat([]byte{0x23}, 32), quarantined: make(map[*desktopLeaseAdapter]struct{})}
	lease, err := writer.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Quarantine(context.Background(), os.ErrDeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := desktopprobe.Acquire(ctx, root); err == nil {
		t.Fatal("quarantined lease released writer before native drain")
	}
	backend := &drainTestBackend{}
	host := &desktopInputHost{writer: writer, backend: backend}
	if err := host.closeAfterNativeDrain(context.Background()); err != nil && !errors.Is(err, desktopprobe.ErrDirty) {
		t.Fatalf("close after drain = %v", err)
	}
	if !backend.closed {
		t.Fatal("native drain fence was not called")
	}
	reopened, err := desktopprobe.Acquire(context.Background(), root)
	if err != nil {
		t.Fatalf("writer lock not released after native drain: %v", err)
	}
	if _, _, err := reopened.Begin("after-quarantine", [32]byte{}); !errors.Is(err, desktopprobe.ErrDirty) {
		t.Fatalf("new action after quarantined close = %v, want ErrDirty", err)
	}
	_ = reopened.Close(context.Background())
}
