package comuse

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"

	"github.com/sirerun/comuse/internal/writer"
)

const actionPolicyVersion uint64 = 1

// Do admits one host-approved action against a fresh complete scoped snapshot.
func (s *Session) Do(ctx context.Context, action Action) (result ActionResult, returnedErr error) {
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
	epoch := s.currentPermissionEpoch()

	callCtx, cancel := context.WithTimeout(callCtx, s.budget.Timeout)
	defer cancel()
	lease, acquireErr := writer.Acquire(callCtx, s.writerDirectory)
	if acquireErr != nil {
		return notApplied(action.ID), writerCallError(callCtx, acquireErr)
	}
	admissionStarted := false
	defer func() {
		if !admissionStarted {
			if closeErr := s.closeActionLease(lease); closeErr != nil {
				result.Cleanup = CleanupUnknown
				returnedErr = coreError("backend_unavailable")
			}
		}
	}()
	commitmentJSON, marshalErr := json.Marshal(action)
	if marshalErr != nil {
		return notApplied(action.ID), coreError("invalid_request")
	}
	commitmentInput := sha256.Sum256(commitmentJSON)
	commitment, commitmentErr := writer.BindingCommitment(s.writerKey, commitmentInput[:])
	if commitmentErr != nil {
		return notApplied(action.ID), coreError("internal_error")
	}
	priorOutcome, lookupErr := lease.Lookup(action.ID, commitment)
	if lookupErr != nil {
		switch {
		case errors.Is(lookupErr, writer.ErrBindingMismatch):
			return notApplied(action.ID), coreError("policy_refused")
		case errors.Is(lookupErr, writer.ErrReplayExpired):
			return notApplied(action.ID), coreError("replay_result_expired")
		case errors.Is(lookupErr, writer.ErrDirty):
			return unknownResult(action.ID), coreError("unknown_outcome")
		default:
			return notApplied(action.ID), writerCallError(callCtx, lookupErr)
		}
	}
	if priorOutcome != nil {
		result := replayResult(action.ID, *priorOutcome)
		if priorOutcome.Metadata.ErrorCode != "" {
			return result, coreError(priorOutcome.Metadata.ErrorCode)
		}
		if result.Execution == ExecutionUnknown || result.Cleanup != CleanupComplete {
			return result, coreError("unknown_outcome")
		}
		return result, nil
	}

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

	desktop, desktopErr := s.acquireDesktop(callCtx)
	if desktopErr != nil {
		if errors.Is(desktopErr, writer.ErrDirty) {
			return unknownResult(action.ID), coreError("unknown_outcome")
		}
		if errors.Is(desktopErr, writer.ErrDesktopIdentityUnavailable) {
			return notApplied(action.ID), coreError("unsupported")
		}
		return notApplied(action.ID), writerCallError(callCtx, desktopErr)
	}
	intentStarted := false
	defer func() {
		if intentStarted {
			if result.Execution != ExecutionUnknown && result.Cleanup == CleanupComplete && !s.journalQuarantined(lease) {
				if err := desktop.Complete(); err != nil {
					result.Cleanup = CleanupUnknown
					returnedErr = coreError("unknown_outcome")
					s.mu.Lock()
					s.quarantinedDesktops = append(s.quarantinedDesktops, desktop)
					s.mu.Unlock()
					return
				}
			} else {
				_ = desktop.MarkDirty()
				s.mu.Lock()
				s.quarantinedDesktops = append(s.quarantinedDesktops, desktop)
				s.mu.Unlock()
				return
			}
		}
		if err := desktop.Close(); err != nil {
			result.Cleanup = CleanupUnknown
			returnedErr = coreError("backend_unavailable")
			s.mu.Lock()
			s.quarantinedDesktops = append(s.quarantinedDesktops, desktop)
			s.mu.Unlock()
		}
	}()
	journalBinding, bindingErr := s.journalBinding(s.writerDirectory, s.writerKey)
	if bindingErr != nil {
		return notApplied(action.ID), coreError("backend_unavailable")
	}

	// Refresh the observation and capability immediately before admission. A
	// caller's public state hash remains stable across hidden native state, but
	// the exact private native state ID is always passed to the backend.
	s.account(callCtx, CounterObservationA11y, 1)
	nativeSnapshot, callErr := s.backend.Observe(callCtx, action.WindowRef, s.budget)
	if callErr != nil {
		return notApplied(action.ID), s.stableBackendError(callCtx, callErr)
	}
	current, binding, normalizeErr := s.normalizeObservation(action.WindowRef, nativeSnapshot)
	if normalizeErr != nil {
		return notApplied(action.ID), normalizeErr
	}
	if !s.rememberSnapshotAtEpoch(binding, epoch) {
		return notApplied(action.ID), coreError("permission_denied")
	}
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
		return notApplied(action.ID), s.stableBackendError(callCtx, callErr)
	}
	s.invalidateIfPermissionDenied(doctor)
	if !doctor.Capabilities.Accessibility {
		return notApplied(action.ID), coreError("permission_denied")
	}
	if s.currentPermissionEpoch() != epoch {
		return notApplied(action.ID), coreError("permission_denied")
	}
	if !doctor.Capabilities.Input || !doctor.Capabilities.QualifiedInput {
		return notApplied(action.ID), coreError("policy_refused")
	}
	if !qualifiedActionKind(doctor.Capabilities, action.Kind) {
		return notApplied(action.ID), coreError("unsupported")
	}
	if s.approvalProvider == nil {
		return notApplied(action.ID), coreError("approval_required")
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

	admissionStarted = true
	if err := desktop.Begin(journalBinding); err != nil {
		return s.quarantineLease(lease, action.ID, "journal_persistence_failed")
	}
	intentStarted = true
	// Each NEW durable intent gets a unique admission commitment, so changing
	// journal/key cannot deduplicate distinct native attempts in the UID quota.
	var admission [32]byte
	if _, err := rand.Read(admission[:]); err != nil {
		return s.quarantineLease(lease, action.ID, "journal_persistence_failed")
	}
	if quotaErr := s.reserveQuota(callCtx, admission); quotaErr != nil {
		code := "backend_unavailable"
		if errors.Is(quotaErr, writer.ErrQuotaExhausted) {
			code = "rate_limited"
		}
		result := notApplied(action.ID)
		if err := finishLease(lease, ticket, action, result, code); err != nil {
			return s.quarantineLease(lease, action.ID, "journal_persistence_failed")
		}
		if err := s.closeActionLease(lease); err != nil {
			return result, coreError("backend_unavailable")
		}
		return result, coreError(code)
	}

	s.mu.Lock()
	if s.actionsUsed >= s.maxActions {
		s.mu.Unlock()
		result := notApplied(action.ID)
		if err := finishLease(lease, ticket, action, result, "rate_limited"); err != nil {
			return s.quarantineLease(lease, action.ID, "journal_persistence_failed")
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
		approvalCode := approvalErrorCode(approvalErr)
		result := notApplied(action.ID)
		if err := finishLease(lease, ticket, action, result, approvalCode); err != nil {
			return s.quarantineLease(lease, action.ID, "journal_persistence_failed")
		}
		if err := s.closeActionLease(lease); err != nil {
			return result, coreError("backend_unavailable")
		}
		return result, coreError(approvalCode)
	}
	if s.currentPermissionEpoch() != epoch {
		return s.finishPermissionDeniedAction(lease, ticket, action)
	}
	if err := callCtx.Err(); err != nil {
		result := notApplied(action.ID)
		callError := stableCallError(err)
		code := "cancelled"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "budget_exceeded"
		}
		if finishErr := finishLease(lease, ticket, action, result, code); finishErr != nil {
			return s.quarantineLease(lease, action.ID, "journal_persistence_failed")
		}
		if closeErr := s.closeActionLease(lease); closeErr != nil {
			return result, coreError("backend_unavailable")
		}
		return result, callError
	}

	nativeAction := action
	nativeAction.StateID = binding.nativeState
	if s.currentPermissionEpoch() != epoch {
		return s.finishPermissionDeniedAction(lease, ticket, action)
	}
	s.mu.Lock()
	s.actionSequence = saturatingAdd(s.actionSequence, 1)
	s.mu.Unlock()
	s.account(callCtx, CounterActions, 1)
	result, executeErr := s.backend.Execute(callCtx, nativeAction)
	if executeErr != nil {
		s.invalidateOnBackendError(executeErr)
		unknown := unknownResult(action.ID)
		return s.persistUnknownBeforeTerminal(lease, ticket, action, unknown, "native_outcome_unknown")
	}
	if !validResult(action.ID, result) {
		unknown := unknownResult(action.ID)
		return s.persistUnknownBeforeTerminal(lease, ticket, action, unknown, "native_outcome_unknown")
	}
	result.CompletedSteps = append([]string(nil), result.CompletedSteps...)
	result.Verification.Reason = safeVerificationReason(result.Verification.Reason)
	if result.Execution == ExecutionUnknown || result.Cleanup != CleanupComplete {
		return s.persistUnknownBeforeTerminal(lease, ticket, action, result, "native_outcome_unknown")
	}
	if err := finishLease(lease, ticket, action, result, ""); err != nil {
		return s.quarantineLease(lease, action.ID, "journal_persistence_failed")
	}
	if err := s.closeActionLease(lease); err != nil {
		return unknownResult(action.ID), coreError("backend_unavailable")
	}
	return result, nil
}

func (s *Session) finishPermissionDeniedAction(lease *writer.Lease, ticket *writer.Ticket, action Action) (ActionResult, error) {
	result := notApplied(action.ID)
	if err := finishLease(lease, ticket, action, result, "permission_denied"); err != nil {
		return s.quarantineLease(lease, action.ID, "journal_persistence_failed")
	}
	if err := s.closeActionLease(lease); err != nil {
		return result, coreError("backend_unavailable")
	}
	return result, coreError("permission_denied")
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

func approvalErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "budget_exceeded"
	}
	var safe *Error
	if errors.As(err, &safe) {
		if _, ok := safeMessages[safe.Code]; ok {
			return safe.Code
		}
	}
	return "approval_required"
}

func (s *Session) persistUnknownBeforeTerminal(lease *writer.Lease, ticket *writer.Ticket, action Action, result ActionResult, reason string) (ActionResult, error) {
	if err := persistUnknownOutcome(func() error {
		return lease.Quarantine(reason)
	}, func() error {
		return finishLease(lease, ticket, action, result, "unknown_outcome")
	}); err != nil {
		s.retainQuarantinedLease(lease)
		return unknownResult(action.ID), coreError("unknown_outcome")
	}
	s.retainQuarantinedLease(lease)
	return result, coreError("unknown_outcome")
}

func persistUnknownOutcome(quarantine, finish func() error) error {
	if err := quarantine(); err != nil {
		return err
	}
	return finish()
}

func (s *Session) retainQuarantinedLease(lease *writer.Lease) {
	s.mu.Lock()
	s.quarantined = append(s.quarantined, lease)
	s.mu.Unlock()
}

func (s *Session) quarantineLease(lease *writer.Lease, actionID, reason string) (ActionResult, error) {
	if err := lease.Quarantine(reason); err != nil {
		s.retainQuarantinedLease(lease)
		return unknownResult(actionID), coreError("unknown_outcome")
	}
	s.retainQuarantinedLease(lease)
	return unknownResult(actionID), coreError("unknown_outcome")
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
	result.Method = metadata.Method
	if metadata.CompletedSteps != "" {
		result.CompletedSteps = strings.Split(metadata.CompletedSteps, ",")
	}
	result.Verification.Reason = safeVerificationReason(metadata.VerificationReason)
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
