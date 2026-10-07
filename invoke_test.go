package comuse

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestSessionCallProjectsReadOnlyOperationsAndChargesLedger(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-a", elements: testElements()}
	session := newTestSession(t, backend, false)
	t.Run("doctor", func(t *testing.T) {
		envelope, err := session.Call(context.Background(), Request{Operation: OperationDoctor})
		if err != nil || envelope.Status != "ok" || envelope.StateStatus != "unavailable" {
			t.Fatalf("doctor call: status=%s err=%v", envelope.Status, err)
		}
		if envelope.Usage.Observations.A11y != 0 || envelope.CompletedSteps == nil {
			t.Fatalf("doctor usage/steps: %+v", envelope)
		}
	})
	t.Run("windows", func(t *testing.T) {
		envelope, err := session.Call(context.Background(), Request{Operation: OperationWindows})
		if err != nil || envelope.Result.encoded == nil {
			t.Fatalf("windows call: %v", err)
		}
	})
	t.Run("full observation", func(t *testing.T) {
		if _, err := session.Windows(context.Background()); err != nil {
			t.Fatal(err)
		}
		envelope, err := session.Call(context.Background(), Request{Operation: OperationObserve, Observe: &ObserveParams{WindowRef: "window-1", Mode: "full"}})
		if err != nil {
			t.Fatalf("observe call: %v", err)
		}
		if envelope.Usage.Observations.A11y != 1 || envelope.Usage.SemanticResults.Snapshot != 1 || envelope.StateStatus != "unavailable" || string(envelope.State.encoded) != "null" {
			t.Fatalf("observe accounting/state: %+v", envelope)
		}
		var result MetadataLegacySnapshot
		if err := json.Unmarshal(envelope.Result.encoded, &result); err != nil {
			t.Fatal(err)
		}
		var full MetadataSnapshot
		if err := json.Unmarshal(envelope.Observation.encoded, &full); err != nil {
			t.Fatal(err)
		}
		if full.Kind != "snapshot" || full.StateID == "" || full.Nodes == nil || full.Context.RootRefs == nil {
			t.Fatalf("full snapshot projection: %+v", full)
		}
		if result.StateID != full.StateID || result.ObservedAt != full.ObservedAt {
			t.Fatalf("legacy/full identity mismatch: legacy=%+v full=%+v", result, full)
		}
	})
	t.Run("ledger precharge", func(t *testing.T) {
		envelope, err := session.Call(context.Background(), Request{Operation: OperationLedger})
		if err != nil {
			t.Fatal(err)
		}
		var before LedgerSnapshot
		if err := json.Unmarshal(envelope.Result.encoded, &before); err != nil {
			t.Fatal(err)
		}
		encoded, err := canonicalJSON(before)
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Usage.SerializedTextBytes < uint64(len(encoded)) {
			t.Fatalf("ledger precharge/usage: before=%+v usage=%+v bytes=%d", before, envelope.Usage, len(encoded))
		}
		after := session.ledger.Snapshot(session.sessionID)
		if after.SerializedTextBytes != before.SerializedTextBytes+envelope.Usage.SerializedTextBytes {
			t.Fatalf("ledger charged bytes more or less than this call: before=%d after=%d usage=%d", before.SerializedTextBytes, after.SerializedTextBytes, envelope.Usage.SerializedTextBytes)
		}
	})
}

func TestSessionCallRoutesMutationThroughHostAuthorization(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-a", elements: testElements()}
	session := newTestSession(t, backend, true)
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	observed, err := session.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := session.Call(context.Background(), Request{Operation: OperationClickElement, ClickElement: &ElementTarget{
		WindowTarget: WindowTarget{ActionID: "call-act-01", WindowRef: "window-1"}, ElementRef: "normal-1", StateID: observed.StateID,
	}})
	if ErrorCode(err) != "policy_refused" || envelope.Error == nil || envelope.Error.Code != "policy_refused" {
		t.Fatalf("mutation outcome: envelope=%+v err=%v", envelope, err)
	}
	if envelope.ActionID == nil || *envelope.ActionID != "call-act-01" || envelope.Execution != string(ExecutionNotApplied) || envelope.StateStatus != string(StateUnavailable) || envelope.Method != nil || envelope.CompletedSteps == nil {
		t.Fatalf("mutation metadata did not preserve actual not-applied outcome: %+v", envelope)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.executed) != 0 {
		t.Fatalf("unqualified input reached backend Execute: %d", len(backend.executed))
	}
}

func TestSessionCallRejectsBeforeBackendAndSanitizesError(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-a"}
	session := newTestSession(t, backend, false)
	envelope, err := session.Call(context.Background(), Request{Operation: OperationTypeText, TypeText: &TypeTextParams{WindowTarget: WindowTarget{ActionID: "a-1", WindowRef: "window-1"}, Text: "hello"}})
	if ErrorCode(err) != "unsupported" || envelope.Error == nil || envelope.Error.Code != "unsupported" {
		t.Fatalf("unsupported operation: envelope=%+v err=%v", envelope, err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.doctorCalls != 0 {
		t.Fatalf("rejected call reached backend: doctor calls=%d", backend.doctorCalls)
	}
}

func TestSessionCallLegacyWaitKeepsSnapshotAndFullObservation(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-a", elements: testElements()}
	session := newTestSession(t, backend, false)
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	envelope, err := session.Call(context.Background(), Request{Operation: OperationWait, Wait: &WaitParams{WindowRef: "window-1", TimeoutMS: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	var legacy MetadataLegacySnapshot
	var full MetadataSnapshot
	if err := json.Unmarshal(envelope.Result.encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(envelope.Observation.encoded, &full); err != nil {
		t.Fatal(err)
	}
	if legacy.StateID == "" || legacy.StateID != full.StateID || envelope.Usage.Observations.A11y != 1 || envelope.Usage.SemanticResults.Snapshot != 1 {
		t.Fatalf("legacy wait projection mismatch: legacy=%+v full=%+v usage=%+v", legacy, full, envelope.Usage)
	}
}

func TestSessionCallUnknownOperationIsRejected(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-a"}
	session := newTestSession(t, backend, false)
	if envelope, err := session.Call(context.Background(), Request{Operation: "future_operation"}); ErrorCode(err) != "invalid_request" || envelope.SchemaVersion != 1 || envelope.Action != "doctor" || envelope.ActionID != nil {
		t.Fatalf("unknown operation response: envelope=%+v err=%v", envelope, err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.doctorCalls != 0 {
		t.Fatalf("unknown operation reached backend: %d", backend.doctorCalls)
	}
}

func TestSessionCallKeepsConcurrentPerCallCounters(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-a", elements: testElements()}
	session := newTestSession(t, backend, false)
	if _, err := session.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			envelope, err := session.Call(context.Background(), Request{Operation: OperationObserve, Observe: &ObserveParams{WindowRef: "window-1", Mode: "full"}})
			if err != nil {
				errs <- err
				return
			}
			if envelope.Usage.Observations.A11y != 1 {
				errs <- ErrInvalidMetadata
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent call: %v", err)
	}
	if got := session.ledger.Snapshot(session.sessionID).Observations.A11y; got != n {
		t.Fatalf("global observation count=%d, want %d", got, n)
	}
}
