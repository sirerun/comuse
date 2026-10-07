package comuse

import (
	"context"
)

// Ledger returns a detached cumulative accounting snapshot after rechecking
// the session lifecycle and current accessibility permission. The returned
// snapshot is captured before its standalone canonical JSON byte charge.
func (s *Session) Ledger(ctx context.Context) (LedgerSnapshot, error) {
	return s.ledgerView(ctx, true)
}

// SetModelUsageSnapshot replaces the trusted host's cumulative model-usage
// snapshot. The host supplies cumulative totals; Comuse does not combine
// reports from different source/model identities or infer usage from bytes.
// Passing nil for both records clears model_usage.
func (s *Session) SetModelUsageSnapshot(actual *ActualModelUsage, estimate *EstimateModelUsage) error {
	if s == nil {
		return coreError("invalid_request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.closing {
		return coreError("session_closed")
	}
	if !s.now().Before(s.scope.ExpiresAt) {
		s.purgeSemanticStateLocked()
		return coreError("state_expired")
	}
	if s.ledger == nil {
		return coreError("internal_error")
	}
	if err := s.ledger.SetModelUsageSnapshot(actual, estimate); err != nil {
		return coreError("invalid_request")
	}
	return nil
}

// ledgerView returns the exact immutable precharge value. charge is false only
// when that value will be embedded in a coordinator-owned domain envelope.
func (s *Session) ledgerView(ctx context.Context, charge bool) (LedgerSnapshot, error) {
	callCtx, done, err := s.enter(ctx)
	if err != nil {
		return LedgerSnapshot{}, err
	}
	defer done()
	if s.ledger == nil {
		return LedgerSnapshot{}, coreError("internal_error")
	}
	epoch := s.currentPermissionEpoch()
	contextEpoch := s.contextEpochNow()
	if _, _, err := s.revalidateDesktopAuthority(callCtx, contextEpoch); err != nil {
		return LedgerSnapshot{}, err
	}
	s.mu.Lock()
	if s.closed || s.closing {
		s.mu.Unlock()
		return LedgerSnapshot{}, coreError("session_closed")
	}
	if !s.now().Before(s.scope.ExpiresAt) {
		s.purgeSemanticStateLocked()
		s.mu.Unlock()
		return LedgerSnapshot{}, coreError("state_expired")
	}
	if s.permissionEpoch != epoch {
		s.mu.Unlock()
		return LedgerSnapshot{}, coreError("permission_denied")
	}
	if err := s.ledger.SetRetainedBytes(uint64(max(s.snapshotBytes, 0))); err != nil {
		s.mu.Unlock()
		return LedgerSnapshot{}, coreError("internal_error")
	}
	id := s.sessionID
	s.mu.Unlock()

	snapshot := s.ledger.Snapshot(id)
	if charge {
		if _, err := s.ledger.ChargeLedgerSnapshot(snapshot); err != nil {
			return LedgerSnapshot{}, coreError("internal_error")
		}
	}
	return snapshot, nil
}
