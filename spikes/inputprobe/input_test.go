package inputprobe

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sirerun/comuse/spikes/policyprobe"
)

type testWriter struct{ lease *testLease }

func (writer testWriter) Acquire(context.Context) (writerLease, error) { return writer.lease, nil }

type testLease struct{ releases, quarantines int }

func (lease *testLease) Release(context.Context) error           { lease.releases++; return nil }
func (lease *testLease) Quarantine(context.Context, error) error { lease.quarantines++; return nil }

type testJournal struct {
	records             map[string]JournalRecord
	begins, finishes    int
	beginErr, finishErr error
}

func (journal *testJournal) Lookup(_ context.Context, id string) (JournalRecord, bool, error) {
	record, ok := journal.records[id]
	return record, ok, nil
}
func (journal *testJournal) Begin(_ context.Context, record JournalRecord) error {
	journal.begins++
	if journal.beginErr != nil {
		return journal.beginErr
	}
	if journal.records == nil {
		journal.records = make(map[string]JournalRecord)
	}
	journal.records[record.ActionID] = record
	return nil
}
func (journal *testJournal) Finish(_ context.Context, record JournalRecord) error {
	journal.finishes++
	if journal.finishErr != nil {
		return journal.finishErr
	}
	journal.records[record.ActionID] = record
	return nil
}

type testBackend struct {
	calls    int
	response []byte
	err      error
}

func (backend *testBackend) Call(context.Context, []byte) ([]byte, error) {
	backend.calls++
	return backend.response, backend.err
}

func fixtureInput(t *testing.T) (*Executor, Request, *testJournal, *testLease, *testBackend, *policyprobe.HostAuthority) {
	t.Helper()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	gate, host, err := policyprobe.New(policyprobe.Clock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	scope := policyprobe.Scope{
		PrincipalID: "operator", SessionID: "session",
		Process:      policyprobe.ProcessIdentity{PID: 123, BundleID: "com.sirerun.comuse.fixture", LaunchGeneration: "process-ref"},
		FixtureNonce: "nonce-1", StateID: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		WindowRef: "window-ref", WindowTitle: "Comuse Fixture nonce-1", ElementRef: "element-ref", PolicyVersion: 1,
		ExpiresAt: now.Add(time.Minute),
	}
	action := policyprobe.Action{Kind: policyprobe.ActionReadValue, Target: policyprobe.TargetTextField, ElementRef: "element-ref"}
	request := Request{ActionID: "action-1", Scope: scope, Action: action}
	journal, lease, backend := &testJournal{}, &testLease{}, &testBackend{}
	executor, err := newExecutor(gate, testWriter{lease}, journal, backend, []byte("0123456789abcdef0123456789abcdef"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return executor, request, journal, lease, backend, host
}

func approved(t *testing.T, host *policyprobe.HostAuthority, request Request) policyprobe.Approval {
	t.Helper()
	challenge, err := host.Issue(request.Scope, request.Action, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := host.ApproveHost(challenge.ID)
	if err != nil {
		t.Fatal(err)
	}
	return approval
}

func TestDeniedApprovalNeverCallsBackend(t *testing.T) {
	executor, request, journal, lease, backend, _ := fixtureInput(t)
	_, err := executor.execute(context.Background(), request)
	if err == nil {
		t.Fatal("expected denial")
	}
	if backend.calls != 0 || journal.begins != 1 || lease.releases != 1 {
		t.Fatalf("calls=%d begins=%d releases=%d", backend.calls, journal.begins, lease.releases)
	}
}

func TestUnsupportedActionRejectedBeforeAdmissionAndBackend(t *testing.T) {
	executor, request, journal, lease, backend, _ := fixtureInput(t)
	request.Action.Kind = policyprobe.ActionKind(99) // Includes selected-text insertion until policy defines it.
	_, err := executor.execute(context.Background(), request)
	if err == nil {
		t.Fatal("expected unsupported action")
	}
	if backend.calls != 0 || journal.begins != 0 || lease.releases != 0 {
		t.Fatalf("calls=%d begins=%d releases=%d", backend.calls, journal.begins, lease.releases)
	}
}

func TestUnknownNativeOutcomeIsQuarantinedAndNeverRetried(t *testing.T) {
	executor, request, journal, lease, backend, host := fixtureInput(t)
	request.Approval = approved(t, host, request)
	backend.err = errors.New("native outcome uncertain")
	_, err := executor.execute(context.Background(), request)
	if err == nil {
		t.Fatal("expected unknown outcome error")
	}
	if backend.calls != 1 || lease.quarantines != 1 || journal.records[request.ActionID].Execution != "unknown" {
		t.Fatalf("calls=%d quarantine=%d record=%+v", backend.calls, lease.quarantines, journal.records[request.ActionID])
	}
	_, _ = executor.execute(context.Background(), request)
	if backend.calls != 1 {
		t.Fatalf("unknown action retried: calls=%d", backend.calls)
	}
}

func TestActionIDCommitmentMismatchNeverDispatches(t *testing.T) {
	executor, request, journal, lease, backend, host := fixtureInput(t)
	request.Approval = approved(t, host, request)
	request.Action.Text = "ignored for read action commitment binding"
	commitment, err := executor.commitment(request)
	if err != nil {
		t.Fatal(err)
	}
	journal.records = map[string]JournalRecord{request.ActionID: {ActionID: request.ActionID, Commitment: commitment}}
	request.Action.Text = "different bound input"
	_, err = executor.execute(context.Background(), request)
	if err == nil {
		t.Fatal("expected action id binding mismatch")
	}
	if backend.calls != 0 || journal.begins != 0 || lease.releases != 0 {
		t.Fatalf("calls=%d begins=%d releases=%d", backend.calls, journal.begins, lease.releases)
	}
}

func TestTerminalNativeResponseIsPersistedThenReplayedWithoutApproval(t *testing.T) {
	executor, request, journal, lease, backend, host := fixtureInput(t)
	request.Approval = approved(t, host, request)
	backend.response = []byte(`{"schema_version":"fixture.v0","ok":true,"request_id":"action-1","action_id":"action-1","action":"read_value","execution":"applied","verification":{"status":"verified"},"state_status":"available","cleanup":{"status":"not_required"}}`)
	response, err := executor.execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var envelope nativeEnvelope
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Execution != "applied" {
		t.Fatalf("response=%s err=%v", response, err)
	}
	request.Approval = policyprobe.Approval{}
	replayed, err := executor.execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if string(replayed) != string(response) || backend.calls != 1 || lease.releases != 1 || journal.finishes != 1 {
		t.Fatalf("replayed=%s calls=%d releases=%d finishes=%d", replayed, backend.calls, lease.releases, journal.finishes)
	}
}

func TestUncertainCleanupQuarantinesWriterAndDoesNotRetry(t *testing.T) {
	executor, request, journal, lease, backend, host := fixtureInput(t)
	request.Approval = approved(t, host, request)
	backend.response = []byte(`{"schema_version":"fixture.v0","ok":true,"request_id":"action-1","action_id":"action-1","action":"read_value","execution":"applied","verification":{"status":"verified"},"state_status":"available","cleanup":{"status":"unknown"}}`)
	response, err := executor.execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if lease.quarantines != 1 || lease.releases != 0 || journal.records[request.ActionID].Execution != "unknown" {
		t.Fatalf("quarantines=%d releases=%d record=%+v", lease.quarantines, lease.releases, journal.records[request.ActionID])
	}
	if _, err := executor.execute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if backend.calls != 1 {
		t.Fatalf("cleanup-uncertain action retried: calls=%d response=%s", backend.calls, response)
	}
}
