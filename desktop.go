package comuse

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/sirerun/comuse/internal/writer"
)

const actionPolicyVersion uint64 = 1

// Do admits one host-approved action against a fresh complete scoped snapshot.
func (s *Session) Do(ctx context.Context, action Action) (ActionResult, error) {
	if err := validateAction(action); err != nil {
		return notApplied(action.ID), err
	}
	if s == nil || !s.mutationEnabled {
		return notApplied(action.ID), coreError("approval_required")
	}
	callCtx, done, err := s.enter(ctx)
	if err != nil {
		return notApplied(action.ID), err
	}
	defer done()
	s.actionMu.Lock()
	defer s.actionMu.Unlock()

	prior, ok := s.findSnapshot(action.WindowRef, action.StateID)
	if !ok {
		return notApplied(action.ID), coreError("state_expired")
	}
	if !normalTarget(prior.public, action.ElementRef) || !hasAdvertisedAction(prior.public, action.ElementRef, action.Kind) {
		return notApplied(action.ID), coreError("policy_refused")
	}
	window, ok := s.window(action.WindowRef)
	if !ok || !scopeContains(s.scope, window.Process) {
		return notApplied(action.ID), coreError("element_stale")
	}

	// Refresh the observation and capability immediately before admission. A
	// caller's public state hash remains stable across hidden native state, but
	// the exact private native state ID is always passed to the backend.
	nativeSnapshot, callErr := s.backend.Observe(callCtx, action.WindowRef, s.budget)
	if callErr != nil {
		return notApplied(action.ID), stableContextError(callCtx, callErr)
	}
	current, binding, normalizeErr := s.normalizeObservation(action.WindowRef, nativeSnapshot)
	if normalizeErr != nil {
		return notApplied(action.ID), normalizeErr
	}
	s.rememberSnapshot(binding)
	if current.StateID != action.StateID {
		return notApplied(action.ID), coreError("element_stale")
	}
	if !current.Coverage.Complete {
		return notApplied(action.ID), coreError("policy_refused")
	}
	if !normalTarget(current, action.ElementRef) || !hasAdvertisedAction(current, action.ElementRef, action.Kind) {
		return notApplied(action.ID), coreError("element_stale")
	}
	doctor, callErr := s.backend.Doctor(callCtx)
	if callErr != nil {
		return notApplied(action.ID), stableContextError(callCtx, callErr)
	}
	if !doctor.Capabilities.Input || !doctor.Capabilities.QualifiedInput {
		return notApplied(action.ID), coreError("policy_refused")
	}
	if s.approvalProvider == nil {
		return notApplied(action.ID), coreError("approval_required")
	}

	callCtx, cancel := context.WithTimeout(callCtx, s.budget.Timeout)
	defer cancel()
	lease, acquireErr := writer.Acquire(callCtx, s.writerDirectory)
	if acquireErr != nil {
		return notApplied(action.ID), writerCallError(callCtx, acquireErr)
	}
	commitmentJSON, marshalErr := json.Marshal(action)
	if marshalErr != nil {
		_ = s.closeActionLease(lease)
		return notApplied(action.ID), coreError("invalid_request")
	}
	commitmentInput := sha256.Sum256(commitmentJSON)
	commitment, commitmentErr := writer.BindingCommitment(s.writerKey, commitmentInput[:])
	if commitmentErr != nil {
		_ = s.closeActionLease(lease)
		return notApplied(action.ID), coreError("internal_error")
	}
	ticket, previous, beginErr := lease.Begin(action.ID, commitment)
	if beginErr != nil {
		_ = s.closeActionLease(lease)
		if errors.Is(beginErr, writer.ErrBindingMismatch) {
			return notApplied(action.ID), coreError("policy_refused")
		}
		if errors.Is(beginErr, writer.ErrDirty) {
			return unknownResult(action.ID), coreError("unknown_outcome")
		}
		return notApplied(action.ID), writerCallError(callCtx, beginErr)
	}
	if previous != nil {
		closeErr := s.closeActionLease(lease)
		result := replayResult(action.ID, *previous)
		if closeErr != nil && !errors.Is(closeErr, writer.ErrDirty) {
			return unknownResult(action.ID), coreError("backend_unavailable")
		}
		if previous.Metadata.ErrorCode != "" {
			return result, coreError(previous.Metadata.ErrorCode)
		}
		if result.Execution == ExecutionUnknown || result.Cleanup != CleanupComplete {
			return result, coreError("unknown_outcome")
		}
		return result, nil
	}

	s.mu.Lock()
	if s.actionsUsed >= s.maxActions {
		s.mu.Unlock()
		result := notApplied(action.ID)
		if err := finishLease(lease, ticket, action, result, "rate_limited"); err != nil {
			return s.quarantineLease(lease, action.ID, "journal_persistence_failed", err)
		}
		if err := s.closeActionLease(lease); err != nil {
			return result, coreError("backend_unavailable")
		}
		return result, coreError("rate_limited")
	}
	s.actionsUsed++
	s.mu.Unlock()
	observedAt := current.ObservedAt
	request := ApprovalRequest{
		SessionID:     s.sessionID,
		Action:        action,
		Process:       window.Process,
		ObservedAt:    observedAt,
		PolicyVersion: actionPolicyVersion,
		ExpiresAt:     s.now().Add(maxApprovalAge),
	}
	approval, approvalErr := s.approvalProvider.Approve(callCtx, request)
	if approvalErr == nil {
		approvalErr = validateApprovalBinding(request, approval, s.now())
	}
	if approvalErr == nil {
		approvalErr = s.consumeApproval(action.ID)
	}
	if approvalErr != nil {
		result := notApplied(action.ID)
		if err := finishLease(lease, ticket, action, result, "approval_required"); err != nil {
			return s.quarantineLease(lease, action.ID, "journal_persistence_failed", err)
		}
		if err := s.closeActionLease(lease); err != nil {
			return result, coreError("backend_unavailable")
		}
		if errors.Is(approvalErr, context.Canceled) || errors.Is(approvalErr, context.DeadlineExceeded) {
			return result, stableCallError(approvalErr)
		}
		return result, coreError("approval_required")
	}
	if err := callCtx.Err(); err != nil {
		result := notApplied(action.ID)
		if finishErr := finishLease(lease, ticket, action, result, "cancelled"); finishErr != nil {
			return s.quarantineLease(lease, action.ID, "journal_persistence_failed", finishErr)
		}
		if closeErr := s.closeActionLease(lease); closeErr != nil {
			return result, coreError("backend_unavailable")
		}
		return result, stableCallError(err)
	}

	nativeAction := action
	nativeAction.StateID = binding.nativeState
	result, executeErr := s.backend.Execute(callCtx, nativeAction)
	if executeErr != nil {
		unknown := unknownResult(action.ID)
		if finishErr := finishLease(lease, ticket, action, unknown, "unknown_outcome"); finishErr != nil {
			return s.quarantineLease(lease, action.ID, "journal_persistence_failed", errors.Join(executeErr, finishErr))
		}
		return s.quarantineLease(lease, action.ID, "native_outcome_unknown", executeErr)
	}
	if !validResult(action.ID, result) {
		unknown := unknownResult(action.ID)
		if finishErr := finishLease(lease, ticket, action, unknown, "unknown_outcome"); finishErr != nil {
			return s.quarantineLease(lease, action.ID, "journal_persistence_failed", finishErr)
		}
		return s.quarantineLease(lease, action.ID, "native_outcome_unknown", invalidResultError())
	}
	result.Verification.Reason = safeVerificationReason(result.Verification.Reason)
	if result.Execution == ExecutionUnknown || result.Cleanup != CleanupComplete {
		if err := finishLease(lease, ticket, action, result, "unknown_outcome"); err != nil {
			return s.quarantineLease(lease, action.ID, "journal_persistence_failed", err)
		}
		return s.quarantineLeaseResult(lease, action.ID, result, "native_outcome_unknown", coreError("unknown_outcome"))
	}
	if err := finishLease(lease, ticket, action, result, ""); err != nil {
		return s.quarantineLease(lease, action.ID, "journal_persistence_failed", err)
	}
	if err := s.closeActionLease(lease); err != nil {
		return unknownResult(action.ID), coreError("backend_unavailable")
	}
	return result, nil
}

func (s *Session) closeActionLease(lease *writer.Lease) error {
	err := lease.Close(context.Background())
	if errors.Is(err, writer.ErrLeaseRetained) {
		s.mu.Lock()
		s.quarantined = append(s.quarantined, lease)
		s.mu.Unlock()
	}
	return err
}

func (s *Session) consumeApproval(actionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.usedApprovals[actionID]; exists {
		return coreError("policy_refused")
	}
	s.usedApprovals[actionID] = struct{}{}
	return nil
}

func (s *Session) quarantineLease(lease *writer.Lease, actionID, reason string, cause error) (ActionResult, error) {
	if err := lease.Quarantine(reason); err != nil {
		cause = errors.Join(cause, err)
	}
	s.mu.Lock()
	s.quarantined = append(s.quarantined, lease)
	s.mu.Unlock()
	return unknownResult(actionID), coreError("unknown_outcome")
}

func (s *Session) quarantineLeaseResult(lease *writer.Lease, actionID string, result ActionResult, reason string, cause error) (ActionResult, error) {
	if err := lease.Quarantine(reason); err != nil {
		cause = errors.Join(cause, err)
	}
	s.mu.Lock()
	s.quarantined = append(s.quarantined, lease)
	s.mu.Unlock()
	return result, coreError("unknown_outcome")
}

func finishLease(lease *writer.Lease, ticket *writer.Ticket, action Action, result ActionResult, code string) error {
	outcome := writer.OutcomeUnknown
	switch result.Execution {
	case ExecutionApplied:
		outcome = writer.OutcomeApplied
	case ExecutionNotApplied:
		outcome = writer.OutcomeNotApplied
	case ExecutionPartiallyApplied:
		outcome = writer.OutcomePartial
	}
	return lease.FinishWithMetadata(ticket, outcome, actionMetadata(action, result, code))
}

func writerCallError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return stableCallError(ctx.Err())
	}
	if errors.Is(err, writer.ErrUnsupportedLock) || errors.Is(err, writer.ErrClosed) {
		return coreError("desktop_busy")
	}
	return coreError("backend_unavailable")
}

func hasAdvertisedAction(snapshot Observation, elementRef, kind string) bool {
	for _, element := range snapshot.Elements {
		if element.Ref != elementRef || element.Enabled == nil || !*element.Enabled {
			continue
		}
		for _, advertised := range element.Actions {
			if advertised == kind {
				return true
			}
		}
	}
	return false
}

func notApplied(actionID string) ActionResult {
	return ActionResult{ActionID: actionID, Execution: ExecutionNotApplied, Verification: Verification{Status: VerificationUnavailable}, StateStatus: StateUnavailable, Cleanup: CleanupComplete}
}

func unknownResult(actionID string) ActionResult {
	return ActionResult{ActionID: actionID, Execution: ExecutionUnknown, Verification: Verification{Status: VerificationUnavailable}, StateStatus: StateUnavailable, Cleanup: CleanupUnknown}
}

func replayResult(actionID string, prior writer.PriorOutcome) ActionResult {
	metadata := prior.Metadata
	result := ActionResult{ActionID: actionID, Execution: ExecutionUnknown, Verification: Verification{Status: VerificationUnavailable}, StateStatus: StateUnavailable, Cleanup: CleanupUnknown}
	switch metadata.Execution {
	case "not_applied":
		result.Execution = ExecutionNotApplied
	case "applied":
		result.Execution = ExecutionApplied
	case "partial":
		result.Execution = ExecutionPartiallyApplied
	case "unknown":
		result.Execution = ExecutionUnknown
	}
	if metadata.Verification != "" {
		result.Verification.Status = VerificationStatus(metadata.Verification)
	}
	if metadata.StateStatus != "" {
		result.StateStatus = StateStatus(metadata.StateStatus)
	}
	switch metadata.Cleanup {
	case "released", "not_required":
		result.Cleanup = CleanupComplete
	case "failed":
		result.Cleanup = CleanupDirty
	case "unknown":
		result.Cleanup = CleanupUnknown
	}
	if result.Execution == ExecutionUnknown {
		return unknownResult(actionID)
	}
	return result
}

func validPublicStateStatus(status StateStatus) StateStatus {
	if status == StateAvailable || status == StateUnavailable {
		return status
	}
	return StateUnavailable
}
