package comuse

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCanonicalEnvelopeBaselineIsActionableAndLedgerChargeIsExact(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native", elements: testElements(), input: true, qualified: true}
	session := newTestSession(t, backend, true)
	_, err := session.Call(context.Background(), Request{Operation: OperationWindows})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := session.Call(context.Background(), Request{Operation: OperationObserve, Observe: &ObserveParams{WindowRef: "window-1", Mode: "full"}})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot MetadataSnapshot
	if err := json.Unmarshal(observed.Observation.encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	acted, err := session.Call(context.Background(), Request{Operation: OperationClickElement, ClickElement: &ElementTarget{WindowTarget: WindowTarget{ActionID: "canonical-call", WindowRef: "window-1"}, ElementRef: "normal-1", StateID: snapshot.StateID}})
	if err != nil || acted.Execution != "applied" || acted.Usage.Actions != 1 || len(backend.executed) != 1 {
		t.Fatalf("action=%+v err=%v", acted, err)
	}
	if acted.StateStatus != "unavailable" || string(acted.State.encoded) != "null" {
		t.Fatal("invented compact state")
	}
	ledger, err := session.Call(context.Background(), Request{Operation: OperationLedger})
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := envelopeWithoutUsage(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Usage.SerializedTextBytes != uint64(len(bytes)) {
		t.Fatalf("double ledger charge: %d vs %d", ledger.Usage.SerializedTextBytes, len(bytes))
	}
}

func TestInvalidMutationUsesInertTransportDiscriminator(t *testing.T) {
	backend := &fakeBackend{process: testProcess()}
	session := newTestSession(t, backend, false)
	envelope, err := session.Call(context.Background(), Request{Operation: OperationClickElement, ClickElement: &ElementTarget{}})
	if ErrorCode(err) != "invalid_request" || envelope.Action != "doctor" || envelope.ActionID != nil || envelope.Error == nil || envelope.Error.Message != "invalid_request" {
		t.Fatalf("envelope=%+v err=%v", envelope, err)
	}
}

func TestSharedExplicitReadReturnsOneFreshAuthorizedRecord(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native", elements: testElements()}
	session := newTestSession(t, backend, false)
	_, _ = session.Windows(t.Context())
	observed, err := session.Observe(t.Context(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	response, err := session.Call(t.Context(), Request{Operation: OperationReadElement, ReadElement: &ReadElementParams{WindowRef: "window-1", ElementRef: "normal-1", StateID: observed.StateID}})
	if err != nil || string(response.Result.encoded) != string(response.Observation.encoded) || response.Usage.Observations.A11y != 2 {
		t.Fatalf("read=%+v err=%v", response, err)
	}
	var read MetadataElementContent
	if err := json.Unmarshal(response.Result.encoded, &read); err != nil {
		t.Fatal(err)
	}
	if read.Text != "explicit value" || read.ObservedAt == "" || read.Truncated || read.StateID != observed.StateID {
		t.Fatalf("read=%+v", read)
	}
}

func TestSharedCallKeepsConditionWaitOutcomeAndNoObservation(t *testing.T) {
	checked := false
	base := &fakeBackend{process: testProcess(), nativeState: "condition-state", elements: []Element{{Ref: "normal-1", Role: "AXCheckBox", Classification: "normal", Checked: &checked}}}
	s := newWaitTestSession(t, &semanticBooleanWaitBackend{fakeBackend: base}, testProcess())
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if _, err := s.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	obs, err := s.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []bool{false, true} {
		env, err := s.Call(context.Background(), Request{Operation: OperationWait, Wait: &WaitParams{Condition: "element_checked", WindowRef: "window-1", ElementRef: "normal-1", StateID: obs.StateID, Expected: &expected, TimeoutMS: 1, PollIntervalMS: 50}})
		if expected && ErrorCode(err) != "budget_exceeded" {
			t.Fatalf("timeout err=%v", err)
		}
		if !expected && err != nil {
			t.Fatal(err)
		}
		encoded, e := json.Marshal(env.Result)
		if e != nil {
			t.Fatal(e)
		}
		var result MetadataWaitResult
		if e := json.Unmarshal(encoded, &result); e != nil {
			t.Fatal(e)
		}
		code := ""
		if err != nil {
			code = ErrorCode(err)
		}
		if result.Condition != "element_checked" || result.Satisfied == expected || !validWaitEnvelopeOutcome(WaitResult{Reason: result.Reason, Satisfied: result.Satisfied}, code) {
			t.Fatalf("wait result=%s err=%v", encoded, err)
		}
		observation, e := json.Marshal(env.Observation)
		if e != nil || string(observation) != "null" {
			t.Fatalf("condition observation=%s err=%v", observation, e)
		}
	}
}
