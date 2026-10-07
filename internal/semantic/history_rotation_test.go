package semantic

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestHistoryExpiredHashCannotSubstituteFreshMetadata(t *testing.T) {
	base, _, _ := loadContractFixture(t)
	h, err := NewHistory(Normalize)
	if err != nil {
		t.Fatal(err)
	}
	now := base.ObservedAt
	original, err := h.Publish(base, now)
	if err != nil {
		t.Fatal(err)
	}
	base.ObservedAt = now.Add(StateTTL + time.Second)
	base.ActionSequence++
	if _, err := h.Publish(base, base.ObservedAt); err == nil {
		t.Fatal("expired state hash was revived with fresh observation metadata")
	}
	if _, err := h.Lookup(original.ScopeID, original.WindowRef, original.StateID, base.ObservedAt); err == nil {
		t.Fatal("expired lookup substituted a new record")
	}
}

func TestCanonicalEmptyForestAndRemovalReconstruction(t *testing.T) {
	base, _, _ := loadContractFixture(t)
	empty := CloneSnapshot(base)
	empty.StateID = ""
	empty.Nodes = map[string]Node{}
	empty.Context = Context{RootRefs: []string{}}
	normalized, err := Normalize(empty)
	if err != nil {
		t.Fatal(err)
	}
	delta, err := Diff(base, normalized)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(base, delta)
	if err != nil || result.StateID != normalized.StateID || len(result.Nodes) != 0 {
		t.Fatalf("empty reconstruction=%+v err=%v", result, err)
	}
}

func TestHistoryEvictedIdentityRequiresScopeRotation(t *testing.T) {
	base, _, _ := loadContractFixture(t)
	h, err := NewHistory(Normalize)
	if err != nil {
		t.Fatal(err)
	}
	original, err := h.Publish(base, base.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(original); err != nil {
		t.Fatalf("returned normalized record invalid: %v", err)
	}
	for i := 0; i < MaxGenerations; i++ {
		changed := CloneSnapshot(base)
		changed.StateID = ""
		for ref, node := range changed.Nodes {
			if node.Classification != "normal" {
				continue
			}
			label := fmt.Sprintf("generation-%d", i)
			node.Label = &label
			changed.Nodes[ref] = node
			break
		}
		if _, err := h.Publish(changed, base.ObservedAt); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := h.Publish(original, base.ObservedAt); !errors.Is(err, ErrScopeRotationRequired) {
		t.Fatalf("evicted identity err=%v", err)
	}
}

func TestHistoryRetiredCommitmentsStayBounded(t *testing.T) {
	base, _, _ := loadContractFixture(t)
	h, err := NewHistory(Normalize)
	if err != nil {
		t.Fatal(err)
	}
	var rotation bool
	for i := 0; i < maxHistoryTombstones+MaxGenerations+2; i++ {
		changed := CloneSnapshot(base)
		changed.StateID = ""
		for ref, node := range changed.Nodes {
			if node.Classification != "normal" {
				continue
			}
			label := fmt.Sprintf("bounded-generation-%d", i)
			node.Label = &label
			changed.Nodes[ref] = node
			break
		}
		_, err := h.Publish(changed, base.ObservedAt)
		if errors.Is(err, ErrScopeRotationRequired) {
			rotation = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(h.retired) > maxHistoryTombstones || h.bytes > MaxStateBytes {
			t.Fatal("retention bound exceeded")
		}
	}
	if !rotation {
		t.Fatal("unbounded retired identity admission")
	}
	rotated := CloneSnapshot(base)
	rotated.ScopeID = "rotated-scope"
	rotated.StateID = ""
	h.Purge()
	if _, err := h.Publish(rotated, base.ObservedAt); err != nil {
		t.Fatal(err)
	}
}
