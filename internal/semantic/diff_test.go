package semantic

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
	"unicode/utf8"
)

func TestDiffReorderReparentAndPropertySeeds(t *testing.T) {
	base, _, _ := loadContractFixture(t)
	seeds := []func(*Snapshot){
		func(s *Snapshot) {
			n := s.Nodes["root"]
			n.ChildRefs = []string{"secure", "normal"}
			s.Nodes["root"] = n
		},
		func(s *Snapshot) {
			root := s.Nodes["root"]
			root.ChildRefs = []string{"secure"}
			s.Nodes["root"] = root
			n := s.Nodes["normal"]
			n.ParentRef = nil
			s.Nodes["normal"] = n
			s.Context.RootRefs = []string{"root", "normal"}
		},
		func(s *Snapshot) { v := "seed-property"; n := s.Nodes["normal"]; n.Value = &v; s.Nodes["normal"] = n },
		func(s *Snapshot) {
			n := s.Nodes["normal"]
			n.Actions = []string{"press", "replace"}
			s.Nodes["normal"] = n
		},
	}
	for i, mutate := range seeds {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			next := CloneSnapshot(base)
			next.ObservedAt = base.ObservedAt.Add(time.Second)
			next.ActionSequence++
			next.StateID = ""
			mutate(&next)
			if err := Validate(next); err != nil {
				t.Fatalf("invalid seed state: %v", err)
			}
			d, err := Diff(base, next)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Apply(base, d)
			if err != nil {
				t.Fatal(err)
			}
			want, err := Normalize(next)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("reconstruction mismatch\ngot %#v\nwant %#v", got, want)
			}
		})
	}
}

func TestDiffRejectsPartialAndInvalidGraphs(t *testing.T) {
	base, current, _ := loadContractFixture(t)
	partial := CloneSnapshot(current)
	partial.StateID = ""
	partial.Coverage.Status = "partial"
	if _, err := Diff(base, partial); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("partial diff error = %v", err)
	}
	historical := CloneSnapshot(current)
	historical.Historical = true
	historical.StateID = ""
	if _, err := Diff(base, historical); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("historical diff error = %v", err)
	}
	cycle := CloneSnapshot(current)
	cycle.StateID = ""
	root := cycle.Nodes["root"]
	root.ParentRef = stringPtr("normal")
	cycle.Nodes["root"] = root
	if _, err := Diff(base, cycle); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cyclic graph error = %v", err)
	}
}

func TestApplyRejectsTamperedBaseAndConflictingChanges(t *testing.T) {
	base, current, _ := loadContractFixture(t)
	d, err := Diff(base, current)
	if err != nil {
		t.Fatal(err)
	}
	wrong := CloneSnapshot(base)
	wrong.ActionSequence++
	// Metadata is excluded from identity, so change semantic content instead.
	n := wrong.Nodes["normal"]
	v := "different"
	n.Value = &v
	wrong.Nodes["normal"] = n
	wrong.StateID = ""
	if _, err := Apply(wrong, d); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("wrong base error = %v", err)
	}
	conflict := d
	conflict.Upsert = map[string]Node{"secure": {Role: "text_field", Classification: "normal", ParentRef: stringPtr("root"), ChildRefs: []string{}, Actions: []string{}}}
	conflict.Removed = append(append([]string{}, d.Removed...), "secure")
	if _, err := Apply(base, conflict); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("upsert/removal conflict error = %v", err)
	}
	duplicate := d
	duplicate.Removed = []string{"secure", "secure"}
	if _, err := Apply(base, duplicate); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("duplicate removal error = %v", err)
	}
	tampered := d
	tampered.StateID = stringsOf('0', 64)
	if _, err := Apply(base, tampered); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("tampered final hash error = %v", err)
	}
}

func FuzzDiffReconstructProperties(f *testing.F) {
	f.Add("seed-value", false)
	f.Add("café <text>", true)
	f.Fuzz(func(t *testing.T, value string, removeSecure bool) {
		if !utf8.ValidString(value) || len(value) > 8192 {
			t.Skip()
		}
		base, _, _ := loadContractFixture(t)
		next := CloneSnapshot(base)
		next.StateID = ""
		next.ObservedAt = base.ObservedAt.Add(time.Second)
		next.ActionSequence++
		n := next.Nodes["normal"]
		n.Value = &value
		next.Nodes["normal"] = n
		if removeSecure {
			root := next.Nodes["root"]
			root.ChildRefs = []string{"normal"}
			next.Nodes["root"] = root
			delete(next.Nodes, "secure")
		}
		delta, err := Diff(base, next)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Apply(base, delta)
		if err != nil {
			t.Fatal(err)
		}
		want, err := Normalize(next)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("fuzz reconstruction mismatch")
		}
	})
}

func stringPtr(v string) *string { return &v }
func stringsOf(b byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return string(out)
}
