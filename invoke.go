package comuse

import (
	"context"
	"time"

	"github.com/sirerun/comuse/internal/semantic"
)

// Call is the shared typed entry point used by adapters. It records counters
// in a per-call ledger snapshot while the existing Session APIs retain
// ownership of authorization, validation, journaling, and native access.
func (s *Session) Call(ctx context.Context, request Request) (ResultEnvelope, error) {
	call := s.ledgerCall()
	if call == nil {
		call = NewLedger().BeginCall()
	}
	if !knownOperation(request.Operation) {
		return ResultEnvelope{}, coreError("invalid_request")
	}
	if ctx == nil {
		return finishCall(s, call, request.Operation, nil, nil, nil, coreError("invalid_request"))
	}
	ctx = context.WithValue(ctx, accountingContextKey{}, call)
	if s == nil {
		return finishCall(s, call, request.Operation, nil, nil, nil, coreError("invalid_request"))
	}
	if err := request.Validate(); err != nil {
		return finishCall(s, call, request.Operation, nil, nil, nil, coreError("invalid_request"))
	}
	if unsupportedOperation(request.Operation) {
		if actionID := requestActionID(request); actionID != "" {
			outcome := notApplied(actionID)
			return finishCall(s, call, request.Operation, outcome, nil, &outcome, coreError("unsupported"))
		}
		return finishCall(s, call, request.Operation, nil, nil, nil, coreError("unsupported"))
	}

	var result any
	var observation any
	var actionResult *ActionResult
	var err error
	switch request.Operation {
	case OperationDoctor, OperationState:
		var report DoctorReport
		report, err = s.Doctor(ctx)
		result = report
	case OperationWindows:
		result, err = s.Windows(ctx)
	case OperationLedger:
		_, done, enterErr := s.enter(ctx)
		if enterErr != nil {
			err = enterErr
			break
		}
		defer done()
		s.mu.Lock()
		s.ledger.SetRetainedBytes(uint64(max(s.snapshotBytes, 0)))
		s.mu.Unlock()
		snapshot := s.ledger.Snapshot(s.sessionID)
		if _, err = call.ChargeLedgerSnapshot(snapshot); err == nil {
			result = snapshot
		}
	case OperationObserve:
		if request.Observe.Mode != "" && request.Observe.Mode != "full" {
			err = coreError("unsupported")
			break
		}
		var observed Observation
		observed, err = s.Observe(ctx, request.Observe.WindowRef)
		if err == nil {
			var full semantic.Snapshot
			full, err = projectFullObservation(s, observed)
			if err == nil {
				_, _ = call.Add(CounterSemanticSnapshot, 1)
				observed.StateID = full.StateID
				result, observation = observed, full
			}
		}
	case OperationWait:
		if request.Wait.Condition != "" {
			err = coreError("unsupported")
			break
		}
		var observed Observation
		observed, err = s.Wait(ctx, request.Wait.WindowRef, time.Duration(request.Wait.TimeoutMS)*time.Millisecond)
		if err == nil {
			var full semantic.Snapshot
			full, err = projectFullObservation(s, observed)
			if err == nil {
				_, _ = call.Add(CounterSemanticSnapshot, 1)
				observed.StateID = full.StateID
				result, observation = observed, full
			}
		}
	case OperationClickElement, OperationElementAction, OperationWriteElement, OperationScrollElement:
		var action Action
		switch request.Operation {
		case OperationClickElement:
			t := request.ClickElement
			action = Action{ID: t.ActionID, WindowRef: t.WindowRef, ElementRef: t.ElementRef, StateID: t.StateID, Kind: ActionPress}
		case OperationElementAction:
			t := request.ElementAction
			action = Action{ID: t.ActionID, WindowRef: t.WindowRef, ElementRef: t.ElementRef, StateID: t.StateID, Kind: t.Kind}
		case OperationWriteElement:
			t := request.WriteElement
			action = Action{ID: t.ActionID, WindowRef: t.WindowRef, ElementRef: t.ElementRef, StateID: t.StateID, Kind: t.Mode, Text: t.Text}
		case OperationScrollElement:
			t := request.ScrollElement
			action = Action{ID: t.ActionID, WindowRef: t.WindowRef, ElementRef: t.ElementRef, StateID: t.StateID, Kind: ActionScroll, Direction: t.Direction, Amount: t.Amount}
		}
		var actionOutcome ActionResult
		if request.Operation == OperationScrollElement {
			t := request.ScrollElement
			actionOutcome, err = s.ScrollElement(ctx, t.ActionID, t.WindowRef, t.ElementRef, t.StateID, t.Direction, t.Amount)
		} else {
			actionOutcome, err = s.Do(ctx, action)
		}
		actionResult = &actionOutcome
		result = actionOutcome
	default:
		err = coreError("invalid_request")
	}
	return finishCall(s, call, request.Operation, result, observation, actionResult, err)
}

func (s *Session) ledgerCall() *CallSnapshot {
	if s == nil || s.ledger == nil {
		return nil
	}
	return s.ledger.BeginCall()
}

func unsupportedOperation(operation Operation) bool {
	switch operation {
	case OperationReadElement, OperationClick, OperationTypeText, OperationPressKey, OperationScroll, OperationDrag, OperationFocusWindow:
		return true
	default:
		return false
	}
}

func knownOperation(operation Operation) bool {
	switch operation {
	case OperationDoctor, OperationState, OperationWindows, OperationObserve, OperationReadElement, OperationWait,
		OperationLedger, OperationClickElement, OperationElementAction, OperationWriteElement, OperationScrollElement,
		OperationClick, OperationTypeText, OperationPressKey, OperationScroll, OperationDrag, OperationFocusWindow:
		return true
	default:
		return false
	}
}

func requestActionID(request Request) string {
	switch request.Operation {
	case OperationClick:
		return request.Click.ActionID
	case OperationTypeText:
		return request.TypeText.ActionID
	case OperationPressKey:
		return request.PressKey.ActionID
	case OperationScroll:
		return request.Scroll.ActionID
	case OperationDrag:
		return request.Drag.ActionID
	case OperationFocusWindow:
		return request.FocusWindow.ActionID
	default:
		return ""
	}
}

func finishCall(s *Session, call *CallSnapshot, operation Operation, result, observation any, actionResult *ActionResult, domainErr error) (ResultEnvelope, error) {
	code := ""
	if domainErr != nil {
		code = ErrorCode(domainErr)
	}
	resultPayload, payloadErr := projectResult(result)
	if payloadErr != nil {
		resultPayload, _ = NewResultPayload(nil)
		if code == "" {
			code = "internal_error"
		}
	}
	observationPayload, observationErr := projectObservation(observation)
	if observationErr != nil {
		observationPayload, _ = NewObservationPayload(nil)
		if code == "" {
			code = "internal_error"
		}
	}
	state, _ := NewStatePayload(nil)
	metadata := EnvelopeMetadata{
		Status: "ok", OK: true, Action: string(operation), Execution: string(ExecutionNotApplied),
		StateStatus: "unavailable", Verification: MetadataVerification{Status: string(VerificationUnavailable)},
		Cleanup: string(CleanupComplete), CompletedSteps: []string{},
	}
	if code != "" {
		metadata.Status, metadata.OK = "error", false
		metadata.Error, _ = NewSafeError(code)
	}
	if actionResult != nil {
		metadata.ActionID = &actionResult.ActionID
		metadata.Execution = string(actionResult.Execution)
		metadata.Verification = MetadataVerification{Status: string(actionResult.Verification.Status), Reason: actionResult.Verification.Reason}
		metadata.StateStatus = string(StateUnavailable)
		metadata.Cleanup = string(actionResult.Cleanup)
		metadata.CompletedSteps = append([]string{}, actionResult.CompletedSteps...)
		if actionResult.Method != "" {
			metadata.Method = &actionResult.Method
		}
	}
	if operation == OperationClickElement || operation == OperationElementAction || operation == OperationWriteElement || operation == OperationScrollElement {
		if metadata.ActionID == nil {
			metadata.ActionID = mutationActionID(operation, result)
		}
	}
	if code != "" && metadata.Error == nil {
		metadata.Error, _ = NewSafeError("internal_error")
	}
	envelope, err := NewResultEnvelope(metadata, state, observationPayload, resultPayload)
	if err != nil {
		// Invalid opaque mutation IDs cannot be repaired without inventing identity.
		return ResultEnvelope{}, coreError("internal_error")
	}
	if s != nil && s.ledger != nil {
		s.mu.Lock()
		_ = s.ledger.SetRetainedBytes(uint64(max(s.snapshotBytes, 0)))
		s.mu.Unlock()
	}
	finished, err := call.FinishCall(envelope)
	if err != nil {
		return ResultEnvelope{}, coreError("internal_error")
	}
	if code != "" {
		return finished, coreError(code)
	}
	return finished, nil
}

func mutationActionID(operation Operation, result any) *string {
	switch operation {
	case OperationClickElement, OperationElementAction, OperationWriteElement, OperationScrollElement:
		if p, ok := result.(ActionResult); ok {
			return &p.ActionID
		}
	}
	return nil
}
