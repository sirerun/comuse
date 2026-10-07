package comuse

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func validEnvelope(t *testing.T, payload ResultPayload) ResultEnvelope {
	t.Helper()
	state, err := NewStatePayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := NewObservationPayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	return ResultEnvelope{
		SchemaVersion: 1, Status: "ok", OK: true, Action: "doctor", Execution: "not_applied",
		DurationMS: 0, StateStatus: "unavailable", State: state,
		Verification: MetadataVerification{Status: "unavailable", Reason: "no_postcondition_requested"},
		Cleanup:      "complete", Observation: observation,
		Result: payload, CompletedSteps: []string{},
	}
}

func TestFinishCallMeasuresEnvelopeWithoutUsageAndDoesNotRecurse(t *testing.T) {
	ledger := NewLedger()
	call := ledger.BeginCall()
	if _, err := call.Add(CounterActions, 2); err != nil {
		t.Fatal(err)
	}
	if err := ledger.SetRetainedBytes(128); err != nil {
		t.Fatal(err)
	}
	payload, err := NewResultPayload(MetadataDoctor{
		Capabilities: MetadataCapabilities{Accessibility: true},
		Permissions:  map[string]string{"accessibility": "granted"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := call.FinishCall(validEnvelope(t, payload))
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.Actions != 2 || result.Usage.RetainedBytes != 128 || result.Usage.SerializedTextBytes == 0 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if result.Usage.ModelUsage != nil {
		t.Fatalf("unsourced model usage is not null: %+v", result.Usage.ModelUsage)
	}
	withoutUsage, err := envelopeWithoutUsage(result)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Usage.SerializedTextBytes; got != uint64(len(withoutUsage)) {
		t.Fatalf("counted bytes = %d, serialized envelope without usage = %d", got, len(withoutUsage))
	}
	if got := ledger.Snapshot("s").SerializedTextBytes; got != result.Usage.SerializedTextBytes {
		t.Fatalf("cumulative byte count = %d, per-call = %d", got, result.Usage.SerializedTextBytes)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"usage":null`) || !strings.Contains(string(encoded), `"serialized_text_bytes"`) {
		t.Fatalf("unexpected response JSON: %s", encoded)
	}
	if _, err := call.FinishCall(validEnvelope(t, payload)); !errors.Is(err, ErrCallFinished) {
		t.Fatalf("second finish error = %v", err)
	}
}

func TestLedgerPayloadSnapshotIsPrechargeAndCallCountersStayAttributed(t *testing.T) {
	ledger := NewLedger()
	first, second := ledger.BeginCall(), ledger.BeginCall()
	if _, err := first.Add(CounterActions, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Add(CounterObservationA11y, 1); err != nil {
		t.Fatal(err)
	}
	precharge := ledger.Snapshot("session")
	payload, err := NewResultPayload(precharge)
	if err != nil {
		t.Fatal(err)
	}
	firstResult, err := first.FinishCall(validEnvelope(t, payload))
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.Usage.Actions != 1 || firstResult.Usage.Observations.A11y != 0 {
		t.Fatalf("first call usage includes another call: %+v", firstResult.Usage)
	}
	if precharge.SerializedTextBytes != 0 {
		t.Fatalf("immutable precharge snapshot changed: %+v", precharge)
	}
	secondPayload, err := NewResultPayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := second.FinishCall(validEnvelope(t, secondPayload))
	if err != nil {
		t.Fatal(err)
	}
	if secondResult.Usage.Actions != 0 || secondResult.Usage.Observations.A11y != 1 {
		t.Fatalf("second call usage = %+v", secondResult.Usage)
	}
}

func TestSafePayloadAndErrorBuildersRejectArbitraryOrOpenValues(t *testing.T) {
	for _, value := range []any{
		map[string]string{"private": "secret"},
		json.RawMessage(`{"private":"secret"}`),
		"typed action text",
		MetadataDoctor{Permissions: map[string]string{"private": "secret"}},
		MetadataDoctor{},
		MetadataLegacySnapshot{WindowRef: "w1", StateID: strings.Repeat("a", 64), ObservedAt: "2026-10-07T00:00:00Z", Elements: []MetadataLegacyElement{{Ref: "e1", Order: 0, Role: "text field", Classification: "secure", Value: ptrString("secret")}}},
	} {
		if _, err := NewResultPayload(value); !errors.Is(err, ErrInvalidPayload) {
			t.Errorf("accepted unsafe payload %#v: %v", value, err)
		}
	}
	if _, err := NewSafeError("backend said secret content"); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatalf("accepted open error: %v", err)
	}
	errorValue, err := NewSafeError("permission_denied")
	if err != nil || errorValue.Message != errorValue.Code {
		t.Fatalf("safe error = %+v, %v", errorValue, err)
	}
}

func TestErrorEnvelopeMeasuresOnlySafeResponseAndOmitsNoInputText(t *testing.T) {
	state, err := NewStatePayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := NewObservationPayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	safeError, err := NewSafeError("invalid_request")
	if err != nil {
		t.Fatal(err)
	}
	resultPayload, err := NewResultPayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := NewResultEnvelope(EnvelopeMetadata{
		Status: "error", OK: false, Action: "doctor", Execution: "not_applied", StateStatus: "unavailable",
		Verification: MetadataVerification{Status: "unavailable", Reason: "no_postcondition_requested"}, Cleanup: "complete", Error: safeError,
		CompletedSteps: []string{},
	}, state, observation, resultPayload)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewLedger().BeginCall().FinishCall(envelope)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "backend secret") || !strings.Contains(string(encoded), `"message":"invalid_request"`) {
		t.Fatalf("error response leaked or lost safe code: %s", encoded)
	}
	withoutUsage, err := envelopeWithoutUsage(result)
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.SerializedTextBytes != uint64(len(withoutUsage)) {
		t.Fatalf("error response bytes = %d, want %d", result.Usage.SerializedTextBytes, len(withoutUsage))
	}
}

func TestObservationBuilderUsesClosedMapAndBlocksHiddenValues(t *testing.T) {
	value := "private"
	validNode := MetadataNode{ChildRefs: []string{}, Role: "text field", Actions: []string{}, Classification: "normal", Value: &value}
	observation := MetadataSnapshot{
		SchemaVersion: 1, ScopeID: "scope-1", WindowRef: "w1", StateID: strings.Repeat("b", 64),
		ObservedAt: "2026-10-07T00:00:00Z", Coverage: MetadataCoverage{Status: "complete", Limitations: []string{}},
		Context: MetadataContext{RootRefs: []string{}, FocusedElementRef: nil}, Nodes: map[string]MetadataNode{"e1": validNode},
		Kind: "snapshot", Historical: false,
	}
	if _, err := NewObservationPayload(observation); err != nil {
		t.Fatalf("valid normal-value observation rejected: %v", err)
	}
	secureNode := validNode
	secureNode.Classification = "secure"
	observation.Nodes["e1"] = secureNode
	if _, err := NewObservationPayload(observation); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("secure node with value accepted: %v", err)
	}
	if _, err := NewObservationPayload(map[string]any{"kind": "snapshot"}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("arbitrary observation map accepted: %v", err)
	}
}

func ptrString(value string) *string { return &value }

func TestCallModelUsageRequiresTrustedBoundedProvenanceAndPreservesTypes(t *testing.T) {
	call := NewLedger().BeginCall()
	cost := "0.001"
	if err := call.ReportActual(ActualModelUsage{Source: "host.adapter", Model: "model-v1", InputTokens: 12, CostUSD: &cost}); err != nil {
		t.Fatal(err)
	}
	if err := call.ReportEstimate(EstimateModelUsage{Source: "host.estimator", Model: "model-v1", Version: "v2", Detail: "bounded", Method: "host_count"}); err != nil {
		t.Fatal(err)
	}
	bad := ActualModelUsage{Source: "prompt content", Model: "model", InputTokens: 0}
	if err := call.ReportActual(bad); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatalf("accepted unsafe provenance: %v", err)
	}
	payload, err := NewResultPayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := call.FinishCall(validEnvelope(t, payload))
	if err != nil {
		t.Fatal(err)
	}
	usage := result.Usage.ModelUsage
	if usage == nil || usage.Actual == nil || usage.Estimate == nil || usage.Actual.InputTokens != 12 || usage.Estimate.Method != "host_count" {
		t.Fatalf("model provenance = %+v", usage)
	}
	if usage.Actual.CostUSD == nil || *usage.Actual.CostUSD != cost {
		t.Fatalf("actual cost = %+v", usage.Actual.CostUSD)
	}
}

func TestEnvelopeRejectsOpenMethodAndCompletedStepVocabulary(t *testing.T) {
	payload, err := NewResultPayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope := validEnvelope(t, payload)
	method := "cg_unknown"
	envelope.Method = &method
	if err := envelope.Validate(); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatalf("accepted open method: %v", err)
	}
	envelope.Method = nil
	envelope.CompletedSteps = []string{"typed user text"}
	if err := envelope.Validate(); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatalf("accepted open completed step: %v", err)
	}
}
