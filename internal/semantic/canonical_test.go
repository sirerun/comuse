package semantic

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func loadContractFixture(t *testing.T) (Snapshot, Snapshot, Delta) {
	t.Helper()
	b, err := os.ReadFile("testdata/contract-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Baseline  Snapshot  `json:"baseline"`
		Current   Snapshot  `json:"current"`
		Delta     Delta     `json:"delta"`
		Unchanged Unchanged `json:"unchanged"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	return f.Baseline, f.Current, f.Delta
}

func TestCanonicalFixtureHashesAndExactApply(t *testing.T) {
	base, current, fixtureDelta := loadContractFixture(t)
	for _, tc := range []struct {
		snapshot Snapshot
		want     string
	}{
		{base, "636e10ecc1007d87981a08a285e867a528ddfe8423385ef42d29a133f89357f5"},
		{current, "1b0a4323f0249afdf14f42d2056a2494f9dbd2d46aaca7b3e284e06c0983ac2e"},
	} {
		got, err := StateID(tc.snapshot)
		if err != nil || got != tc.want {
			t.Fatalf("StateID() = %q, %v; want %q", got, err, tc.want)
		}
	}
	d, err := Diff(base, current)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d, fixtureDelta) {
		t.Fatalf("Diff differs from frozen vector: got %#v want %#v", d, fixtureDelta)
	}
	rebuilt, err := Apply(base, d)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Normalize(current)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rebuilt, want) {
		t.Fatalf("Apply did not reconstruct current state\n got: %#v\nwant: %#v", rebuilt, want)
	}
}

func TestRedactionPrecedesBoundsAndHashing(t *testing.T) {
	base, _, _ := loadContractFixture(t)
	secretA, secretB := strings.Repeat("secret-a", 5000), strings.Repeat("secret-b", 5000)
	n := base.Nodes["secure"]
	n.Label = &secretA
	n.Value = &secretA
	n.Enabled = boolPtr(true)
	n.Actions = []string{"press"}
	base.Nodes["secure"] = n
	idA, err := StateID(base)
	if err != nil {
		t.Fatal(err)
	}
	bytesA, err := CanonicalBytes(base)
	if err != nil {
		t.Fatal(err)
	}
	n = base.Nodes["secure"]
	n.Label = &secretB
	n.Value = &secretB
	base.Nodes["secure"] = n
	idB, err := StateID(base)
	if err != nil {
		t.Fatal(err)
	}
	if idA != idB {
		t.Fatal("hidden changes altered public identity")
	}
	bytesB, err := CanonicalBytes(base)
	if err != nil || !bytes.Equal(bytesA, bytesB) {
		t.Fatalf("hidden changes altered canonical bytes: %v", err)
	}
	if got := base.Nodes["secure"].Value; got == nil || *got != secretB {
		t.Fatal("normalization mutated caller-owned input")
	}
	normalized, err := Normalize(base)
	if err != nil {
		t.Fatal(err)
	}
	if x := normalized.Nodes["secure"]; x.Label != nil || x.Value != nil || x.Enabled != nil || len(x.Actions) != 0 {
		t.Fatal("secure node was not fully sanitized")
	}
}

func TestSemanticHashExcludesMetadataAndSortsActions(t *testing.T) {
	base, _, _ := loadContractFixture(t)
	id, err := StateID(base)
	if err != nil {
		t.Fatal(err)
	}
	base.ObservedAt = base.ObservedAt.Add(time.Hour)
	base.ActionSequence = 8
	base.Historical = true
	base.ResetReason = "expired_baseline"
	changed, err := StateID(base)
	if err != nil || changed != id {
		t.Fatalf("metadata changed identity: %q %v", changed, err)
	}
	base.Historical = false
	base.ResetReason = ""
	base.StateID = ""
	n := base.Nodes["normal"]
	n.Actions = []string{"replace", "press", "replace"}
	base.Nodes["normal"] = n
	norm, err := Normalize(base)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(norm.Nodes["normal"].Actions, []string{"press", "replace"}) {
		t.Fatalf("actions not sorted/deduped: %#v", norm.Nodes["normal"].Actions)
	}
}

func TestNormalizeDeepCopyAndHashTampering(t *testing.T) {
	base, _, _ := loadContractFixture(t)
	n, err := Normalize(base)
	if err != nil {
		t.Fatal(err)
	}
	n.Nodes["normal"].Actions[0] = "press"
	n.Nodes["root"].ChildRefs[0] = "other"
	*n.Nodes["normal"].Value = "changed"
	if *base.Nodes["normal"].Value != "fixture old" || base.Nodes["normal"].Actions[0] != "replace" || base.Nodes["root"].ChildRefs[0] != "normal" {
		t.Fatal("normalized result aliases caller memory")
	}
	base.StateID = strings.Repeat("0", 64)
	if _, err := Normalize(base); err != ErrInvalidRequest {
		t.Fatalf("tampered hash error = %v", err)
	}
}

func TestCanonicalBytesSortedObjectKeysAndUTF8(t *testing.T) {
	base, _, _ := loadContractFixture(t)
	value := "café <tag>"
	n := base.Nodes["normal"]
	n.Value = &value
	base.Nodes["normal"] = n
	base.StateID = ""
	b, err := CanonicalBytes(base)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, `\u003c`) || !strings.Contains(s, "café <tag>") {
		t.Fatalf("unexpected JSON escaping: %s", s)
	}
	if strings.Index(s, `"context"`) > strings.Index(s, `"coverage"`) || strings.Contains(s, ": ") || strings.Contains(s, ", ") {
		t.Fatalf("object keys or whitespace are not canonical: %s", s)
	}
}

func boolPtr(v bool) *bool { return &v }
