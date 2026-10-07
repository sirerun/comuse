package semantic

import (
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
