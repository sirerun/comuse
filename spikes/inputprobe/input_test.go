package inputprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sirerun/comuse/spikes/bridgeclient"
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
func (journal *testJournal) Begin(_ context.Context, record JournalRecord) (bool, error) {
	journal.begins++
	if journal.beginErr != nil {
		return false, journal.beginErr
	}
	if journal.records == nil {
		journal.records = make(map[string]JournalRecord)
	}
	if _, exists := journal.records[record.ActionID]; exists {
		return false, nil
	}
	journal.records[record.ActionID] = record
	return true, nil
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
	calls          int
	inspects       int
	response       []byte
	err            error
	classification *NativeClassification
}

func (backend *testBackend) Inspect(_ context.Context, request NativeTargetRequest) (NativeClassification, error) {
	backend.inspects++
	if backend.classification != nil {
		return *backend.classification, nil
	}
	classification := NativeClassification{Complete: true, ProcessStartRef: request.ProcessStartRef, WindowRef: request.WindowRef, ElementRef: request.ElementRef}
	classification.Role, classification.Identifier, classification.InputClass = "AXTextField", "textfield", "fixture_normal_text_field"
	return classification, nil
}

func (backend *testBackend) inputCall(context.Context, bridgeclient.HostInputRequest) ([]byte, error) {
	backend.calls++
	return backend.response, backend.err
}

func TestFreshNativeClassificationRejectsProtectedBeforeAdmissionAndDispatch(t *testing.T) {
	executor, request, journal, lease, backend, host := fixtureInput(t)
	backend.classification = &NativeClassification{Complete: true, ProcessStartRef: request.Scope.Process.LaunchGeneration, WindowRef: request.Scope.WindowRef, ElementRef: request.Scope.ElementRef, Role: "AXTextField", Identifier: "securefield", InputClass: "protected_or_uncertain_text_field"}
	request.Approval = approved(t, host, request)
	response, err := executor.execute(context.Background(), request)
	if err == nil || backend.inspects != 1 || backend.calls != 0 || journal.begins != 0 || lease.releases != 1 {
		t.Fatalf("response=%s err=%v backend=%+v journal=%+v lease=%+v", response, err, backend, journal, lease)
	}
}

func TestFinishUnknownPersistsOnlyAllowlistedMetadata(t *testing.T) {
	executor, _, journal, lease, _, _ := fixtureInput(t)
	journal.records = make(map[string]JournalRecord)
	const canary = "private_text_canary"
	response := []byte(`{"schema_version":"fixture.v0","request_id":"action-1","action_id":"action-1","action":"secret_action_canary","execution":"unknown","verification":{"status":"secret_verification_canary"},"state_status":"secret_state_canary","cleanup":{"status":"secret_cleanup_canary"},"error":"private_text_canary","result":null}`)
	record := JournalRecord{ActionID: "action-1", Execution: "pending"}
	if err := executor.finishUnknown(context.Background(), lease, record, response); err != nil {
		t.Fatal(err)
	}
	stored, err := json.Marshal(journal.records[record.ActionID])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(canary)) || bytes.Contains(stored, []byte("secret_")) {
		t.Fatalf("unsafe terminal metadata persisted: %s", stored)
	}
	persisted := journal.records[record.ActionID]
	if persisted.Execution != "unknown" || persisted.ErrorCode != "native_error" ||
		persisted.Verification != "unavailable" || persisted.StateStatus != "unavailable" || persisted.Cleanup != "unknown" || persisted.Action != "" {
		t.Fatalf("unexpected sanitized record: %+v", persisted)
	}
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
	response, err := executor.execute(context.Background(), request)
	if err != nil {
		t.Fatalf("denial should be a terminal response: %v", err)
	}
	var envelope nativeEnvelope
	if err := json.Unmarshal(response, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Execution != "not_applied" || envelope.Error == nil || *envelope.Error != "policy_refused" {
		t.Fatalf("denial response=%s", response)
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
	if backend.calls != 0 || journal.begins != 0 || lease.releases != 1 {
		t.Fatalf("calls=%d begins=%d releases=%d", backend.calls, journal.begins, lease.releases)
	}
}

func TestTerminalResponsePersistsOnlyRedactedMetadataAndReplaysWithoutPlaintext(t *testing.T) {
	executor, request, journal, lease, backend, host := fixtureInput(t)
	request.Approval = approved(t, host, request)
	const canary = "PRIVATE-CANARY-TEXT-DO-NOT-PERSIST"
	backend.response = []byte(`{"schema_version":"fixture.v0","ok":true,"request_id":"action-1","action_id":"action-1","action":"read_value","execution":"applied","verification":{"status":"verified"},"state_status":"available","cleanup":{"status":"not_required"},"error":null,"result":{"value":"PRIVATE-CANARY-TEXT-DO-NOT-PERSIST"}}`)
	response, err := executor.execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var envelope nativeEnvelope
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Execution != "applied" {
		t.Fatalf("response=%s err=%v", response, err)
	}
	stored, err := json.Marshal(journal.records[request.ActionID])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(canary)) {
		t.Fatalf("plaintext persisted in journal: %s", stored)
	}
	request.Approval = policyprobe.Approval{}
	replayed, err := executor.execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var replay nativeEnvelope
	if err := json.Unmarshal(replayed, &replay); err != nil {
		t.Fatal(err)
	}
	if replay.Execution != "applied" || string(replay.Result) != "null" || !bytes.Contains(replayed, []byte(`"replayed":true`)) || bytes.Contains(replayed, []byte(canary)) || backend.calls != 1 || lease.releases != 2 || journal.finishes != 1 {
		t.Fatalf("replayed=%s calls=%d releases=%d finishes=%d", replayed, backend.calls, lease.releases, journal.finishes)
	}
}

func TestReplayPreservesSafeOutcomeClassification(t *testing.T) {
	for _, test := range []struct {
		name, execution, code string
		wantOK                bool
	}{
		{"pending becomes unknown", "pending", "", false},
		{"unknown stays failure", "unknown", "dispatch_unknown", false},
		{"refusal stays not applied", "not_applied", "policy_refused", false},
		{"partial stays partial", "partial", "", true},
		{"partial failure stays failure", "partial", "postcondition_failed", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := replaySummary(JournalRecord{ActionID: "a", Execution: test.execution, ErrorCode: test.code})
			var response map[string]any
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatal(err)
			}
			if response["execution"] != mapPendingToUnknown(test.execution) || response["ok"] != test.wantOK {
				t.Fatalf("replay=%s", data)
			}
			if response["result"] != nil {
				t.Fatalf("replay retained result: %s", data)
			}
		})
	}
}

func mapPendingToUnknown(execution string) string {
	if execution == "pending" {
		return "unknown"
	}
	return execution
}

func TestUncertainCleanupQuarantinesWriterAndDoesNotRetry(t *testing.T) {
	executor, request, journal, lease, backend, host := fixtureInput(t)
	request.Approval = approved(t, host, request)
	backend.response = []byte(`{"schema_version":"fixture.v0","ok":true,"request_id":"action-1","action_id":"action-1","action":"read_value","execution":"applied","verification":{"status":"verified"},"state_status":"available","cleanup":{"status":"unknown"},"error":null,"result":{}}`)
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
