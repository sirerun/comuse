package semanticprobe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fixtureElement struct {
	Ref         string   `json:"ref"`
	Role        string   `json:"role"`
	ValueStatus string   `json:"value_status"`
	ChildRefs   []string `json:"child_refs"`
	ParentRef   *string  `json:"parent_ref"`
	Identifier  *string  `json:"identifier,omitempty"`
	Value       *string  `json:"value,omitempty"`
}

func scopeFor(suffix string) ExpectedScope {
	return ExpectedScope{
		RequestID: "request-1", PID: 4321, BundleID: FixtureBundleID,
		FixtureNonce: "fixture-a", ProcessLaunchGeneration: "launch-generation-a",
		ProcessStartRef: "process-" + suffix, WindowRef: "window-" + suffix,
	}
}

func normalFixture(suffix, secureValue string, includeValues bool) (ExpectedScope, []byte) {
	scope := scopeFor(suffix)
	rootRef := "root-" + suffix
	textRef := "text-" + suffix
	secureRef := "secure-" + suffix
	counterRef := "counter-" + suffix
	rootChildren := []string{textRef, secureRef, counterRef}
	root := fixtureElement{Ref: rootRef, Role: "AXWindow", ValueStatus: "omitted", ChildRefs: rootChildren}
	textID := "textfield"
	secureID := "securefield"
	counterID := "counter-value"
	textStatus, counterStatus := "omitted", "omitted"
	var textValue, counterValue, secret *string
	if includeValues {
		textStatus, counterStatus = "included_synthetic_normal", "included_synthetic_counter"
		textValue = stringPointer("synthetic text")
		counterValue = stringPointer("Counter: 4")
	}
	if secureValue != "" {
		secret = stringPointer(secureValue)
	}
	text := fixtureElement{Ref: textRef, Role: "AXTextField", ValueStatus: textStatus, ChildRefs: []string{}, ParentRef: stringPointer(rootRef), Identifier: &textID, Value: textValue}
	secure := fixtureElement{Ref: secureRef, Role: "AXTextField", ValueStatus: "omitted_protected_or_unavailable", ChildRefs: []string{}, ParentRef: stringPointer(rootRef), Identifier: &secureID, Value: secret}
	counter := fixtureElement{Ref: counterRef, Role: "AXStaticText", ValueStatus: counterStatus, ChildRefs: []string{}, ParentRef: stringPointer(rootRef), Identifier: &counterID, Value: counterValue}
	rows := []fixtureElement{root, text, secure, counter}
	textBytes := len(textID) + len(secureID) + len(counterID)
	if includeValues {
		textBytes += len("synthetic text") + len("Counter: 4")
	}
	result := map[string]any{
		"observation_id":    "observation-" + suffix,
		"state_id":          strings.Repeat("a", 64),
		"process_start_ref": scope.ProcessStartRef,
		"window_ref":        scope.WindowRef,
		"root_refs":         []string{rootRef},
		"elements":          rows,
		"coverage": map[string]any{
			"status": "complete", "reason": nil, "depth_limit": MaxDepth,
			"node_limit": MaxElements, "text_byte_limit": MaxTextBytes,
			"deadline_ms": MaxDeadlineMS, "visited": len(rows), "text_bytes": textBytes,
			"truncated": false,
		},
	}
	return scope, marshalEnvelope("completed", result, "")
}

func marshalEnvelope(status string, result any, nativeError string) []byte {
	value := map[string]any{
		"schema_version": SchemaVersion,
		"request_id":     "request-1",
		"status":         status,
	}
	if result != nil {
		value["result"] = result
	}
	if nativeError != "" {
		value["error"] = nativeError
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func resultFromEnvelope(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	return response["result"].(map[string]any)
}

func reencodeEnvelope(t *testing.T, raw []byte, result map[string]any) []byte {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	response["result"] = result
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func stringPointer(value string) *string { return &value }

func TestDefaultProjectionRedactsValuesAndSeparatesNativeAndCanonicalIDs(t *testing.T) {
	scope, raw := normalFixture("one", "secret-canary-one", true)
	snapshot, err := NormalizeEnvelope(raw, scope, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != SnapshotComplete || snapshot.NativeStateID == "" || snapshot.CanonicalStateID == "" {
		t.Fatalf("complete identity status not preserved: %+v", snapshot)
	}
	for _, element := range snapshot.Elements {
		if element.Value != nil {
			t.Fatalf("default projection retained value for %s", element.Identifier)
		}
	}
	if bytes.Contains(snapshot.CanonicalJSON, []byte("secret-canary-one")) || bytes.Contains(snapshot.CanonicalJSON, []byte("synthetic text")) {
		t.Fatal("default canonical projection contains a field value")
	}
	var firstField map[string]any
	if err := json.Unmarshal(snapshot.CanonicalJSON, &firstField); err != nil {
		t.Fatal(err)
	}
	if firstField["fixture_nonce"] != scope.FixtureNonce || firstField["bundle_id"] != scope.BundleID {
		t.Fatalf("canonical scope missing trusted fixture fields: %v", firstField)
	}
	encodedSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encodedSnapshot, []byte(snapshot.NativeStateID)) {
		t.Fatal("native precondition was serialized as ordinary snapshot JSON")
	}
	var wire struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(encodedSnapshot, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Status != "complete" || bytes.Contains(encodedSnapshot, []byte(`"status":1`)) {
		t.Fatalf("snapshot status wire value = %q, want string complete: %s", wire.Status, encodedSnapshot)
	}
}

func TestExplicitSyntheticValuesAreAllowlistedAndSecureChangesDoNotChangeCanonicalID(t *testing.T) {
	scopeA, rawA := normalFixture("one", "hidden-value-A", true)
	scopeB, rawB := normalFixture("one", "hidden-value-B", true)
	options := Options{IncludeSyntheticNormalValue: true}
	snapshotA, err := NormalizeEnvelope(rawA, scopeA, options)
	if err != nil {
		t.Fatal(err)
	}
	snapshotB, err := NormalizeEnvelope(rawB, scopeB, options)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotA.CanonicalStateID != snapshotB.CanonicalStateID || !bytes.Equal(snapshotA.CanonicalJSON, snapshotB.CanonicalJSON) {
		t.Fatal("hidden secure value changed the canonical semantic identity")
	}
	if snapshotA.NativeStateID != "" || snapshotB.NativeStateID != "" {
		t.Fatal("unexpected protected raw value left a native precondition eligible")
	}
	for _, element := range snapshotA.Elements {
		switch element.Identifier {
		case "textfield":
			if element.Value == nil || *element.Value != "synthetic text" {
				t.Fatalf("explicit normal text value missing: %+v", element)
			}
		case "counter-value":
			if element.Value == nil || *element.Value != "Counter: 4" {
				t.Fatalf("explicit counter value missing: %+v", element)
			}
		case "securefield":
			if element.Value != nil || element.ValueStatus != "omitted_protected_or_unavailable" {
				t.Fatalf("protected value survived projection: %+v", element)
			}
		}
	}
	if bytes.Contains(snapshotA.CanonicalJSON, []byte("hidden-value-A")) {
		t.Fatal("canonical JSON contains protected value")
	}
}

func TestNativeAndCanonicalIDsHaveSeparateRoles(t *testing.T) {
	scope, raw := normalFixture("one", "", true)
	snapshot, err := NormalizeEnvelope(raw, scope, Options{IncludeSyntheticNormalValue: true})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.NativeStateID != strings.Repeat("a", 64) {
		t.Fatalf("native state ID = %q", snapshot.NativeStateID)
	}
	if snapshot.CanonicalStateID == snapshot.NativeStateID {
		t.Fatal("semantic hash was substituted for the native precondition")
	}
}

func TestStableProjectionIgnoresObservationAndOpaqueReferenceChurn(t *testing.T) {
	scopeA, rawA := normalFixture("one", "", false)
	scopeB, rawB := normalFixture("two", "", false)
	snapshotA, err := NormalizeEnvelope(rawA, scopeA, Options{})
	if err != nil {
		t.Fatal(err)
	}
	snapshotB, err := NormalizeEnvelope(rawB, scopeB, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshotA.CanonicalStateID != snapshotB.CanonicalStateID {
		t.Fatal("ephemeral observation/process/window/element refs changed the stable projection")
	}
	if snapshotA.ObservationID == snapshotB.ObservationID {
		t.Fatal("fixture helper should provide different observation IDs")
	}
}

func TestInputRowOrderDoesNotMatterButSemanticChildOrderDoes(t *testing.T) {
	scope, raw := normalFixture("one", "", false)
	result := resultFromEnvelope(t, raw)
	rows := result["elements"].([]any)
	rows[1], rows[3] = rows[3], rows[1]
	rowReordered := reencodeEnvelope(t, raw, result)
	first, err := NormalizeEnvelope(raw, scope, Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NormalizeEnvelope(rowReordered, scope, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first.CanonicalStateID != second.CanonicalStateID {
		t.Fatal("transport row order changed canonical state")
	}

	result = resultFromEnvelope(t, raw)
	root := result["elements"].([]any)[0].(map[string]any)
	children := root["child_refs"].([]any)
	children[0], children[2] = children[2], children[0]
	childOrderChanged := reencodeEnvelope(t, raw, result)
	third, err := NormalizeEnvelope(childOrderChanged, scope, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first.CanonicalStateID == third.CanonicalStateID {
		t.Fatal("semantic child order did not change canonical state")
	}
}

func TestTopologyMismatchesAndCyclesAreRejected(t *testing.T) {
	scope, raw := normalFixture("one", "", false)
	result := resultFromEnvelope(t, raw)
	rows := result["elements"].([]any)
	text := rows[1].(map[string]any)
	text["parent_ref"] = "wrong-parent"
	if _, err := NormalizeEnvelope(reencodeEnvelope(t, raw, result), scope, Options{}); !errors.Is(err, ErrTopology) {
		t.Fatalf("parent mismatch error = %v", err)
	}
}

func TestPartialCoverageCarriesNoIdentityAndInfersNoDeletion(t *testing.T) {
	scope, raw := normalFixture("one", "", false)
	result := resultFromEnvelope(t, raw)
	result["elements"] = result["elements"].([]any)[:2]
	coverage := result["coverage"].(map[string]any)
	coverage["status"] = "truncated"
	coverage["reason"] = "bounded_or_ax_uncertainty"
	coverage["visited"] = float64(2)
	coverage["truncated"] = true
	root := result["elements"].([]any)[0].(map[string]any)
	root["child_refs"] = []any{"text-one"}
	raw = reencodeEnvelope(t, raw, result)
	var response map[string]any
	_ = json.Unmarshal(raw, &response)
	response["status"] = "partial"
	raw, _ = json.Marshal(response)
	snapshot, err := NormalizeEnvelope(raw, scope, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != SnapshotPartial || snapshot.NativeStateID != "" || snapshot.CanonicalStateID != "" {
		t.Fatalf("partial result carried identity: %+v", snapshot)
	}
	encodedSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(encodedSnapshot, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Status != "partial" {
		t.Fatalf("partial snapshot status wire value = %q, want partial", wire.Status)
	}
	if len(snapshot.Elements) != 2 || snapshot.Coverage.Status != "truncated" {
		t.Fatalf("partial observed tree was not preserved: %+v", snapshot)
	}
}

func TestExpiredAndStaleNativeReferencesRemainTyped(t *testing.T) {
	scope := scopeFor("one")
	for _, test := range []struct {
		code string
		want error
	}{
		{code: "reference_expired", want: ErrExpiredReference},
		{code: "reference_stale", want: ErrStaleReference},
		{code: "scope_or_permission_denied", want: ErrNativeOperation},
	} {
		raw := marshalEnvelope("error", nil, test.code)
		if _, err := NormalizeEnvelope(raw, scope, Options{}); !errors.Is(err, test.want) {
			t.Errorf("native error %q returned %v, want %v", test.code, err, test.want)
		}
	}
}

func TestRequestAndExpectedScopeAreExact(t *testing.T) {
	scope, raw := normalFixture("one", "", false)
	wrongScope := scope
	wrongScope.WindowRef = "different-window"
	if _, err := NormalizeEnvelope(raw, wrongScope, Options{}); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("window mismatch error = %v", err)
	}
	var envelope map[string]any
	_ = json.Unmarshal(raw, &envelope)
	envelope["request_id"] = "another-request"
	wrongRequest, _ := json.Marshal(envelope)
	if _, err := NormalizeEnvelope(wrongRequest, scope, Options{}); !errors.Is(err, ErrRequestMismatch) {
		t.Fatalf("request mismatch error = %v", err)
	}
}

func TestCoverageInconsistencyAndValueStatusAreRejected(t *testing.T) {
	scope, raw := normalFixture("one", "", false)
	result := resultFromEnvelope(t, raw)
	coverage := result["coverage"].(map[string]any)
	coverage["visited"] = float64(3)
	if _, err := NormalizeEnvelope(reencodeEnvelope(t, raw, result), scope, Options{}); !errors.Is(err, ErrCoverage) {
		t.Fatalf("coverage inconsistency error = %v", err)
	}

	result = resultFromEnvelope(t, raw)
	result["elements"].([]any)[1].(map[string]any)["value_status"] = "not-a-native-status"
	if _, err := NormalizeEnvelope(reencodeEnvelope(t, raw, result), scope, Options{}); !errors.Is(err, ErrValueStatus) {
		t.Fatalf("unknown value status error = %v", err)
	}
}

func TestUnknownGeometryAndOversizedEnvelopeFailClosed(t *testing.T) {
	scope, raw := normalFixture("one", "", false)
	result := resultFromEnvelope(t, raw)
	result["elements"].([]any)[1].(map[string]any)["frame"] = map[string]any{"x": 10}
	if _, err := NormalizeEnvelope(reencodeEnvelope(t, raw, result), scope, Options{}); !errors.Is(err, ErrMalformedEnvelope) {
		t.Fatalf("geometry field error = %v", err)
	}
	tooLarge := bytes.Repeat([]byte("x"), MaxEnvelopeBytes+1)
	if _, err := NormalizeEnvelope(tooLarge, scope, Options{}); !errors.Is(err, ErrResponseLimit) {
		t.Fatalf("oversized envelope error = %v", err)
	}
}

func TestDepthLimitPreventsOverdeepProjection(t *testing.T) {
	scope := scopeFor("deep")
	const nodes = MaxDepth + 2
	rows := make([]fixtureElement, nodes)
	rootRefs := []string{"node-0"}
	for index := 0; index < nodes; index++ {
		row := fixtureElement{Ref: fmt.Sprintf("node-%d", index), Role: "AXStaticText", ValueStatus: "omitted", ChildRefs: []string{}}
		if index > 0 {
			row.ParentRef = stringPointer(fmt.Sprintf("node-%d", index-1))
		}
		if index+1 < nodes {
			row.ChildRefs = []string{fmt.Sprintf("node-%d", index+1)}
		}
		rows[index] = row
	}
	result := map[string]any{
		"observation_id": "obs-deep", "state_id": strings.Repeat("b", 64),
		"process_start_ref": scope.ProcessStartRef, "window_ref": scope.WindowRef,
		"root_refs": rootRefs, "elements": rows,
		"coverage": map[string]any{"status": "complete", "reason": nil, "depth_limit": MaxDepth, "node_limit": MaxElements, "text_byte_limit": MaxTextBytes, "deadline_ms": MaxDeadlineMS, "visited": nodes, "text_bytes": 0, "truncated": false},
	}
	if _, err := NormalizeEnvelope(marshalEnvelope("completed", result, ""), scope, Options{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("depth cap error = %v", err)
	}
}
