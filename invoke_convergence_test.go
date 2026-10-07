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
