package semantic

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// identityNormalizer is synthetic test plumbing; it does not exercise the
// canonical semantic engine.
func identityNormalizer(snapshot Snapshot) (Snapshot, error) { return snapshot, nil }

func historyFixture(id string, observed time.Time, action uint64, label string) Snapshot {
	root := "root-ref"
	child := "leaf-ref"
	return Snapshot{
		Metadata: Metadata{
			SchemaVersion:  SchemaVersion,
			StateID:        fmt.Sprintf("%064x", mustHex(id)),
			ScopeID:        "scope-1",
			WindowRef:      "window-1",
			ObservedAt:     observed,
			ActionSequence: action,
			Coverage:       Coverage{Status: "complete", Limitations: []string{}},
			Context:        Context{RootRefs: []string{root}},
		},
		Kind: "snapshot",
		Nodes: map[string]Node{
			root:  {Role: "group", Classification: "normal", ParentRef: nil, ChildRefs: []string{child}, Actions: []string{}},
			child: {Role: "text_field", Classification: "normal", Label: stringPointer(label), ParentRef: stringPointer(root), ChildRefs: []string{}, Actions: []string{"replace"}},
		},
	}
}

func mustHex(value string) uint64 {
	var n uint64
	for _, r := range value {
		n = n*16 + uint64(r-'0')
	}
	return n
}

func newTestHistory(t *testing.T) *History {
	t.Helper()
	history, err := NewHistory(identityNormalizer)
	if err != nil {
		t.Fatalf("NewHistory: %v", err)
	}
	return history
}

func TestNewHistoryRequiresNormalizer(t *testing.T) {
	if h, err := NewHistory(nil); h != nil || err == nil || err.Error() != "invalid_request" {
		t.Fatalf("NewHistory(nil) = (%v, %v), want safe invalid_request", h, err)
	}
}

func TestHistoryHidesNormalizerErrorDetails(t *testing.T) {
	history, err := NewHistory(func(Snapshot) (Snapshot, error) {
		return Snapshot{}, errors.New("private label and native reference")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.Publish(historyFixture("c", time.Now().UTC(), 0, "synthetic"), time.Now().UTC()); err == nil || err.Error() != "invalid_request" {
		t.Fatalf("normalizer error was not mapped to a safe constant: %v", err)
	}
}

func TestHistoryPublishLookupDefensiveAndPreservesOriginal(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	h := newTestHistory(t)
	first := historyFixture("1", now, 1, "original")
	published, err := h.Publish(first, now)
	if err != nil {
		t.Fatal(err)
	}
	firstLeaf := first.Nodes["leaf-ref"]
	firstLeaf.Label = stringPointer("caller mutated")
	first.Nodes["leaf-ref"] = firstLeaf
	if *published.Nodes["leaf-ref"].Label != "original" {
		t.Fatal("Publish returned caller-owned data")
	}
	key := published.StateID
	second := historyFixture("1", now.Add(30*time.Second), 9, "fresh caller")
	fresh, err := h.Publish(second, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Historical || fresh.ActionSequence != 9 || *fresh.Nodes["leaf-ref"].Label != "fresh caller" {
		t.Fatalf("repeat Publish did not return fresh caller snapshot: %+v", fresh.Metadata)
	}
	stored, err := h.Lookup("scope-1", "window-1", key, now.Add(31*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Historical || stored.ActionSequence != 1 || *stored.Nodes["leaf-ref"].Label != "original" {
		t.Fatalf("stored original changed: historical=%v seq=%d label=%q", stored.Historical, stored.ActionSequence, *stored.Nodes["leaf-ref"].Label)
	}
	storedLeaf := stored.Nodes["leaf-ref"]
	storedLeaf.Label = stringPointer("lookup caller mutation")
	storedLeaf.Actions[0] = "mutated"
	stored.Nodes["leaf-ref"] = storedLeaf
	stored.Coverage.Limitations = append(stored.Coverage.Limitations, "mutated")
	stored.Context.RootRefs[0] = "mutated"
	again, err := h.Lookup("scope-1", "window-1", key, now.Add(32*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if *again.Nodes["leaf-ref"].Label != "original" || again.Nodes["leaf-ref"].Actions[0] != "replace" || again.Context.RootRefs[0] != "root-ref" || len(again.Coverage.Limitations) != 0 {
		t.Fatal("Lookup exposed mutable retained storage")
	}
}

func TestHistoryTTLFromOriginalAndClockRollbackClamp(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	h := newTestHistory(t)
	s, err := h.Publish(historyFixture("2", now, 0, "ttl"), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Publish(historyFixture("2", now.Add(90*time.Second), 1, "repeat"), now.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, err := h.Lookup("scope-1", "window-1", s.StateID, now.Add(2*time.Minute)); err == nil || got.StateID != "" || err.Error() != "state_expired" {
		t.Fatalf("original TTL not honored: (%q, %v)", got.StateID, err)
	}
	if _, err := h.Publish(historyFixture("3", now.Add(3*time.Minute), 0, "rollback"), now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, err := h.Lookup("scope-1", "window-1", fmt.Sprintf("%064x", 2), now.Add(time.Second)); err == nil || got.StateID != "" || err.Error() != "state_expired" {
		t.Fatalf("backwards time revived expired state: (%q, %v)", got.StateID, err)
	}
}

func TestHistoryCrossScopeWindowAndMissingAreSafe(t *testing.T) {
	now := time.Now().UTC()
	h := newTestHistory(t)
	s, err := h.Publish(historyFixture("4", now, 0, "scoped"), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, lookup := range [][3]string{{"other-scope", "window-1", s.StateID}, {"scope-1", "other-window", s.StateID}, {"scope-1", "window-1", strings.Repeat("0", 64)}} {
		if got, err := h.Lookup(lookup[0], lookup[1], lookup[2], now); err == nil || got.StateID != "" || err.Error() != "state_expired" {
			t.Fatalf("foreign or absent lookup leaked data: (%q, %v)", got.StateID, err)
		}
	}
}

func TestHistoryCountLimitEvictsOldestAcrossSession(t *testing.T) {
	now := time.Now().UTC()
	h := newTestHistory(t)
	for i := 1; i <= MaxGenerations+1; i++ {
		s := historyFixture(fmt.Sprintf("%x", i), now.Add(time.Duration(i)*time.Second), 0, "count")
		s.ScopeID = fmt.Sprintf("scope-%d", i) // the generation cap is session-wide.
		if _, err := h.Publish(s, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	oldestID := fmt.Sprintf("%064x", 1)
	if _, err := h.Lookup("scope-1", "window-1", oldestID, now.Add(20*time.Second)); err == nil {
		t.Fatal("oldest generation survived count eviction")
	}
	for i := 2; i <= MaxGenerations+1; i++ {
		if _, err := h.Lookup(fmt.Sprintf("scope-%d", i), "window-1", fmt.Sprintf("%064x", i), now.Add(20*time.Second)); err != nil {
			t.Fatalf("retained generation %d missing: %v", i, err)
		}
	}
}

func TestHistoryBytesBoundAndReferenceSpellingDoesNotAffectCharge(t *testing.T) {
	now := time.Now().UTC()
	left := historyFixture("5", now, 0, strings.Repeat("x", 128))
	right := CloneSnapshot(left)
	delete(right.Nodes, "leaf-ref")
	root := right.Nodes["root-ref"]
	root.ChildRefs = []string{"some-other-opaque-reference"}
	right.Nodes["root-ref"] = root
	leaf := Node{Role: "text_field", Classification: "normal", Label: stringPointer(strings.Repeat("x", 128)), ParentRef: stringPointer("root-ref"), ChildRefs: []string{}, Actions: []string{"replace"}}
	right.Nodes["some-other-opaque-reference"] = leaf
	leftCharge, ok := retainedCharge(left)
	if !ok {
		t.Fatal("left charge failed")
	}
	rightCharge, ok := retainedCharge(right)
	if !ok || rightCharge != leftCharge {
		t.Fatalf("reference spelling changed charge: %d vs %d", leftCharge, rightCharge)
	}
	h := newTestHistory(t)
	large := historyFixture("6", now, 0, strings.Repeat("L", MaxStateBytes/2))
	if _, err := h.Publish(large, now); err != nil {
		t.Fatal(err)
	}
	large2 := historyFixture("7", now.Add(time.Second), 0, strings.Repeat("R", MaxStateBytes/2))
	if _, err := h.Publish(large2, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if h.RetainedBytes() > MaxStateBytes {
		t.Fatalf("retained bytes exceed contract: %d", h.RetainedBytes())
	}
	if _, err := h.Lookup("scope-1", "window-1", fmt.Sprintf("%064x", 6), now.Add(2*time.Second)); err == nil {
		t.Fatal("byte pressure did not evict oldest state")
	}
}

func TestHistoryRejectsPartialMalformedAndOversizeWithoutAdmission(t *testing.T) {
	now := time.Now().UTC()
	h := newTestHistory(t)
	partial := historyFixture("8", now, 0, "partial")
	partial.Coverage.Status = "partial"
	if _, err := h.Publish(partial, now); err != nil {
		t.Fatalf("partial snapshots may be retained: %v", err)
	}
	before := h.RetainedBytes()
	bad := historyFixture("9", now, 0, "bad")
	node := bad.Nodes["leaf-ref"]
	node.Value = stringPointer("unredacted secret")
	node.Classification = "secure"
	bad.Nodes["leaf-ref"] = node
	if _, err := h.Publish(bad, now); err == nil || err.Error() != "invalid_request" {
		t.Fatalf("invalid normalized snapshot accepted: %v", err)
	}
	tooLarge := historyFixture("a", now, 0, strings.Repeat("x", MaxStateBytes))
	if _, err := h.Publish(tooLarge, now); err == nil || err.Error() != "invalid_request" {
		t.Fatalf("oversize snapshot accepted: %v", err)
	}
	if h.RetainedBytes() != before {
		t.Fatalf("failed publication changed retained state: before=%d after=%d", before, h.RetainedBytes())
	}
}

func TestHistoryPurgeAndConcurrentAccess(t *testing.T) {
	now := time.Now().UTC()
	h := newTestHistory(t)
	s, err := h.Publish(historyFixture("b", now, 0, "concurrent"), now)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = h.Lookup("scope-1", "window-1", s.StateID, now.Add(time.Duration(i+j)*time.Millisecond))
				_, _ = h.Publish(historyFixture("b", now.Add(time.Duration(i+j)*time.Millisecond), uint64(j), "fresh"), now.Add(time.Duration(i+j)*time.Millisecond))
				_ = h.RetainedBytes()
			}
		}(i)
	}
	wg.Wait()
	h.Purge()
	if h.RetainedBytes() != 0 {
		t.Fatalf("Purge left retained bytes: %d", h.RetainedBytes())
	}
	if got, err := h.Lookup("scope-1", "window-1", s.StateID, now.Add(time.Second)); err == nil || got.StateID != "" || err.Error() != "state_expired" {
		t.Fatalf("Purge did not invalidate history: (%q, %v)", got.StateID, err)
	}
}
