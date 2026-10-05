package inputprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirerun/comuse/spikes/bridgeclient"
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
	replayed := replaySummary(prior)
	if bytes.Contains(replayed, []byte("synthetic_text_canary")) || bytes.Contains(replayed, []byte("original")) {
		t.Fatalf("durable replay contains native value text: %s", replayed)
	}
	var envelope map[string]any
	if err := json.Unmarshal(replayed, &envelope); err != nil || envelope["execution"] != "partial" || envelope["ok"] != false {
		t.Fatalf("durable partial replay is not truthful: %s (%v)", replayed, err)
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

func (*drainTestBackend) Inspect(context.Context, NativeTargetRequest) (NativeClassification, error) {
	return NativeClassification{}, nil
}

func (*drainTestBackend) inputCall(context.Context, bridgeclient.HostInputRequest) ([]byte, error) {
	return nil, nil
}

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

type finishFailureJournal struct{ delegate *desktopJournalAdapter }

func (journal *finishFailureJournal) Lookup(ctx context.Context, id string) (JournalRecord, bool, error) {
	return journal.delegate.Lookup(ctx, id)
}
func (journal *finishFailureJournal) Begin(ctx context.Context, record JournalRecord) (bool, error) {
	return journal.delegate.Begin(ctx, record)
}
func (*finishFailureJournal) Finish(context.Context, JournalRecord) error {
	return errors.New("injected terminal persistence failure")
}

func TestDirtyPersistsWhenTerminalJournalFinishFails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "finish-failure-state")
	key := bytes.Repeat([]byte{0x51}, 32)
	writer := &desktopWriterAdapter{root: root, journalKey: key, quarantined: make(map[*desktopLeaseAdapter]struct{})}
	base, err := writer.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease := base.(*desktopLeaseAdapter)
	journal := &desktopJournalAdapter{journalKey: key}
	ctx := context.WithValue(context.Background(), writerLeaseContextKey{}, lease)
	record := JournalRecord{ActionID: "finish-failure", Execution: "pending"}
	if created, err := journal.Begin(ctx, record); err != nil || !created {
		t.Fatalf("Begin = %v, %v", created, err)
	}
	executor, _, _, _, _, _ := fixtureInput(t)
	executor.journal = &finishFailureJournal{delegate: journal}
	response := []byte(`{"schema_version":"fixture.v0","request_id":"finish-failure","action_id":"finish-failure","action":"replace","execution":"unknown","verification":{"status":"unavailable"},"state_status":"unavailable","cleanup":{"status":"unknown"},"error":"dispatch_unknown","result":null}`)
	if err := executor.finishUnknown(ctx, lease, record, response); err == nil {
		t.Fatal("Finish failure was ignored")
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := desktopprobe.Acquire(waitCtx, root); err == nil {
		t.Fatal("writer lock released before native drain/close")
	}
	closeErr := lease.lease.Close(context.Background())
	if !errors.Is(closeErr, desktopprobe.ErrDirty) {
		t.Fatalf("close after unknown result = %v, want durable ErrDirty", closeErr)
	}
	restarted, err := desktopprobe.Acquire(context.Background(), root)
	if err != nil {
		t.Fatalf("restart acquire = %v", err)
	}
	defer func() { _ = restarted.Close(context.Background()) }()
	if _, _, err := restarted.Begin("later-action", [32]byte{}); !errors.Is(err, desktopprobe.ErrDirty) {
		t.Fatalf("new action after unresolved terminal write = %v, want ErrDirty", err)
	}
}
