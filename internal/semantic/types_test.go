package semantic

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestFrozenRecordsRoundtripAndCloneIsolation(t *testing.T) {
	data, err := os.ReadFile("testdata/contract-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Baseline  Snapshot  `json:"baseline"`
		Current   Snapshot  `json:"current"`
		Delta     Delta     `json:"delta"`
		Unchanged Unchanged `json:"unchanged"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if err = json.Unmarshal(data, &before); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("wire fields differ from frozen vectors")
	}
	original := fixture.Baseline
	copy := CloneSnapshot(original)
	n := copy.Nodes["normal"]
	*n.Value = "changed clone"
	n.Actions[0] = "press"
	copy.Nodes["normal"] = n
	copy.Context.RootRefs[0] = "changed"
	copy.Nodes["root"].ChildRefs[0] = "changed"
	if *original.Nodes["normal"].Value != "fixture old" || original.Nodes["normal"].Actions[0] != "replace" || original.Context.RootRefs[0] != "root" || original.Nodes["root"].ChildRefs[0] != "normal" {
		t.Fatal("clone aliases retained containers")
	}
	if copy.Nodes["secure"].Value != nil {
		t.Fatal("secure fixture has a value")
	}
}
