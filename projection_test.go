package comuse

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/sirerun/comuse/internal/semantic"
)

func TestClosedProjectionsCloneSemanticArraysAndPointers(t *testing.T) {
	label := "button"
	enabled := true
	state := semantic.Snapshot{Metadata: semantic.Metadata{SchemaVersion: 1, StateID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ScopeID: "scope", WindowRef: "window-1", ObservedAt: time.Now().UTC(), Coverage: semantic.Coverage{Status: "complete", Limitations: []string{}},
		Context: semantic.Context{RootRefs: []string{"node-1"}}}, Kind: "snapshot", Nodes: map[string]semantic.Node{
		"node-1": {Role: "button", Classification: "normal", Label: &label, ParentRef: nil, ChildRefs: []string{}, Enabled: &enabled, Actions: []string{"press"}},
	}}
	projected, err := projectObservation(state)
	if err != nil {
		t.Fatal(err)
	}
	label = "mutated"
	state.Context.RootRefs[0] = "mutated"
	node := state.Nodes["node-1"]
	node.ChildRefs = append(node.ChildRefs, "other")
	state.Nodes["node-1"] = node
	var result MetadataSnapshot
	if err := json.Unmarshal(projected.encoded, &result); err != nil {
		t.Fatal(err)
	}
	if *result.Nodes["node-1"].Label != "button" || result.Context.RootRefs[0] != "node-1" || len(result.Nodes["node-1"].ChildRefs) != 0 {
		t.Fatalf("projection aliases caller memory: %+v", result)
	}
}

func TestProjectionOnlyAcceptsClosedTypedPayloads(t *testing.T) {
	if _, err := projectResult(map[string]any{"error": "private dynamic detail"}); err == nil {
		t.Fatal("generic map escaped the closed result projector")
	}
	if _, err := projectObservation(json.RawMessage(`{"raw":"private"}`)); err == nil {
		t.Fatal("raw JSON escaped the closed observation projector")
	}
}

func TestFullObservationUsesCanonicalRedactedIdentityAndActualMetadata(t *testing.T) {
	backend := &fakeBackend{process: testProcess(), nativeState: "native-a", elements: testElements()}
	session := newTestSession(t, backend, false)
	if _, err := session.Windows(t.Context()); err != nil {
		t.Fatal(err)
	}
	observed, err := session.Observe(t.Context(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	full, err := projectFullObservation(session, observed)
	if err != nil {
		t.Fatal(err)
	}
	if full.StateID == observed.StateID {
		t.Fatal("canonical state ID retained the private legacy hash")
	}
	if full.ObservedAt != observed.ObservedAt || full.ActionSequence != 0 {
		t.Fatalf("invented timestamp/action sequence: %+v", full.Metadata)
	}
	if full.Nodes["normal-1"].Value != nil || full.Nodes["secure-1"].Value != nil {
		t.Fatal("canonical projection retained redacted values")
	}
}
