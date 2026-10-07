package comuse

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSessionLedgerReturnsDefensivePrechargeSnapshot(t *testing.T) {
	backend := &fakeBackend{process: testProcess()}
	session := newTestSession(t, backend, false)
	first, err := session.Ledger(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if first.SerializedTextBytes != 0 {
		t.Fatalf("first precharge snapshot contains own charge: %+v", first)
	}
	second, err := session.Ledger(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.SerializedTextBytes != uint64(len(encoded)) {
		t.Fatalf("cumulative ledger bytes=%d want first record charge %d", second.SerializedTextBytes, len(encoded))
	}
	first.Actions = 99
	first.ModelUsage = &ModelUsage{Actual: &ActualUsageRecord{Source: "mutated", Model: "mutated"}}
	third, err := session.Ledger(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if third.Actions != 0 || third.ModelUsage != nil {
		t.Fatalf("caller mutation changed live ledger: %+v", third)
	}
}

func TestSessionLedgerRefusesPermissionLossAndClosedState(t *testing.T) {
	backend := &fakeBackend{process: testProcess()}
	session := newTestSession(t, backend, false)
	backend.accessibilityDenied = true
	if _, err := session.Ledger(context.Background()); ErrorCode(err) != "permission_denied" {
		t.Fatalf("ledger after permission loss = %v", err)
	}
	backend.accessibilityDenied = false
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Ledger(context.Background()); ErrorCode(err) != "session_closed" {
		t.Fatalf("ledger after close = %v", err)
	}
}

func TestSessionLedgerRefusesExpiredScope(t *testing.T) {
	backend := &fakeBackend{process: testProcess()}
	session := newTestSession(t, backend, false)
	session.mu.Lock()
	session.scope.ExpiresAt = session.now().Add(-1)
	session.mu.Unlock()
	if _, err := session.Ledger(context.Background()); ErrorCode(err) != "state_expired" {
		t.Fatalf("ledger after expiry = %v", err)
	}
}

func TestTrustedCumulativeModelUsageSnapshotIsValidatedAndDetached(t *testing.T) {
	backend := &fakeBackend{process: testProcess()}
	session := newTestSession(t, backend, false)
	cost := "0.125"
	actual := &ActualModelUsage{Source: "host", Model: "model-a", InputTokens: 10, OutputTokens: 4, CostUSD: &cost}
	if err := session.SetModelUsageSnapshot(actual, nil); err != nil {
		t.Fatal(err)
	}
	cost = "9"
	actual.Model = "mutated"
	first, err := session.Ledger(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.ModelUsage == nil || first.ModelUsage.Actual == nil || first.ModelUsage.Actual.Model != "model-a" || *first.ModelUsage.Actual.CostUSD != "0.125" {
		t.Fatalf("reported cumulative snapshot not detached: %+v", first.ModelUsage)
	}
	bad := &ActualModelUsage{Source: "host", Model: "model-b", InputTokens: JSONSafeIntegerMax + 1}
	if err := session.SetModelUsageSnapshot(bad, nil); ErrorCode(err) != "invalid_request" {
		t.Fatalf("invalid report error = %v", err)
	}
	second, err := session.Ledger(context.Background())
	if err != nil || second.ModelUsage == nil || second.ModelUsage.Actual.Model != "model-a" {
		t.Fatalf("invalid report replaced trusted snapshot: %+v, %v", second.ModelUsage, err)
	}
	if err := session.SetModelUsageSnapshot(nil, nil); err != nil {
		t.Fatal(err)
	}
	cleared, err := session.Ledger(context.Background())
	if err != nil || cleared.ModelUsage != nil {
		t.Fatalf("clear model usage = %+v, %v", cleared.ModelUsage, err)
	}
}
