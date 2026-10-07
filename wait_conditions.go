package comuse

import (
	"context"
	"time"
)

const defaultWaitPollInterval = 100 * time.Millisecond

var errAmbiguousWaitWindow = coreError("unsupported")

// WaitCondition polls fresh scoped semantic state until params' condition is
// satisfied or its bounded deadline expires. It never synthesizes pixels or
// holds input. WaitResult timestamps are intentionally limited to the frozen
// DTO; observation time remains attached to the internal fresh snapshot.
func (s *Session) WaitCondition(ctx context.Context, params WaitParams) (WaitResult, error) {
	result := WaitResult{Condition: params.Condition, Reason: "unavailable"}
	if s == nil || ctx == nil {
		return result, coreError("invalid_request")
	}
	if err := (Request{Operation: OperationWait, Wait: &params}).Validate(); err != nil {
		return result, err
	}
	callCtx, done, enterErr := s.enter(ctx)
	if enterErr != nil {
		return result, enterErr
	}
	defer done()
	ctx = callCtx
	if time.Duration(params.TimeoutMS)*time.Millisecond > s.budget.Timeout {
		return result, coreError("budget_exceeded")
	}

	permissionEpoch := s.currentPermissionEpoch()
	contextEpoch := s.contextEpochNow()
	var boundProcess *ProcessIdentity
	var boundElement *Element
	if params.Condition == "window_closed" || isElementWait(params.Condition) {
		window, ok := s.window(params.WindowRef)
		if !ok {
			return result, coreError("element_stale")
		}
		process := window.Process
		boundProcess = &process
	}
	if isElementWait(params.Condition) {
		prior, ok := s.findSnapshot(params.WindowRef, params.StateID)
		if !ok {
			return result, coreError("state_expired")
		}
		target := findNormalElement(prior.public, params.ElementRef)
		if target == nil {
			return result, coreError("element_stale")
		}
		boundElement = &Element{Ref: target.Ref, Role: target.Role, Classification: target.Classification}
	}
	if params.WindowRef != "" {
		ref := params.WindowRef
		result.WindowRef = &ref
	}
	if params.Condition == "window_appears" {
		if err := s.validateWaitProcessRef(params.ProcessRef); err != nil {
			return result, err
		}
	}
	if s.currentPermissionEpoch() != permissionEpoch {
		return result, coreError("permission_denied")
	}
	if s.contextEpochNow() != contextEpoch {
		return result, coreError("state_expired")
	}

	pollInterval := time.Duration(params.PollIntervalMS) * time.Millisecond
	if pollInterval == 0 {
		pollInterval = defaultWaitPollInterval
	}
	started := time.Now()
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(params.TimeoutMS)*time.Millisecond)
	defer cancel()
	for {
		matched, stateID, windowRef, pollErr := s.pollWaitCondition(waitCtx, params, boundProcess, boundElement)
		if stateID != nil {
			result.FinalStateID = stateID
		}
		if windowRef != nil {
			result.WindowRef = windowRef
		}
		if waitCtx.Err() != nil {
			result.ElapsedMS = elapsedMilliseconds(started)
			if ctx.Err() != nil {
				result.Reason = "cancelled"
				return result, stableCallError(ctx.Err())
			}
			result.Reason = "timeout"
			return result, coreError("budget_exceeded")
		}
		if err := s.revalidateWaitIdentity(params, boundProcess); err != nil {
			result.ElapsedMS = elapsedMilliseconds(started)
			return result, err
		}
		if s.currentPermissionEpoch() != permissionEpoch {
			result.ElapsedMS = elapsedMilliseconds(started)
			return result, coreError("permission_denied")
		}
		if s.contextEpochNow() != contextEpoch {
			result.ElapsedMS = elapsedMilliseconds(started)
			return result, coreError("state_expired")
		}
		if pollErr != nil {
			result.ElapsedMS = elapsedMilliseconds(started)
			if params.Condition == "window_appears" && pollErr == errAmbiguousWaitWindow {
				result.Reason = "ambiguous"
			} else if ErrorCode(pollErr) == "cancelled" {
				result.Reason = "cancelled"
			}
			return result, pollErr
		}
		if matched {
			result.Satisfied = true
			result.Reason = "satisfied"
			result.ElapsedMS = elapsedMilliseconds(started)
			result.FinalStateID = stateID
			result.WindowRef = windowRef
			return result, nil
		}
		if waitCtx.Err() != nil {
			result.Reason = "timeout"
			result.ElapsedMS = elapsedMilliseconds(started)
			return result, coreError("budget_exceeded")
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if ctx.Err() != nil {
				result.Reason = "cancelled"
				result.ElapsedMS = elapsedMilliseconds(started)
				return result, stableCallError(ctx.Err())
			}
			result.Reason = "timeout"
			result.ElapsedMS = elapsedMilliseconds(started)
			return result, coreError("budget_exceeded")
		case <-timer.C:
		}
	}
}

func (s *Session) pollWaitCondition(ctx context.Context, params WaitParams, boundProcess *ProcessIdentity, boundElement *Element) (bool, *string, *string, error) {
	switch params.Condition {
	case "window_appears":
		windows, err := s.Windows(ctx)
		if err != nil {
			return false, nil, nil, err
		}
		matches := make([]Window, 0, 1)
		for _, window := range windows {
			ref, err := s.ProcessRef(window.Process)
			if err != nil {
				return false, nil, nil, err
			}
			if ref == params.ProcessRef && (params.Title == "" || window.Title == params.Title) {
				matches = append(matches, window)
			}
		}
		if len(matches) > 1 {
			return false, nil, nil, errAmbiguousWaitWindow
		}
		if len(matches) == 1 {
			ref := matches[0].Ref
			return true, nil, &ref, nil
		}
		return false, nil, nil, nil
	case "window_closed":
		windows, err := s.Windows(ctx)
		if err != nil {
			return false, nil, nil, err
		}
		for _, window := range windows {
			if window.Ref != params.WindowRef {
				continue
			}
			if boundProcess == nil || window.Process != *boundProcess {
				return false, nil, nil, coreError("element_stale")
			}
			ref := params.WindowRef
			return false, nil, &ref, nil
		}
		ref := params.WindowRef
		return true, nil, &ref, nil
	case "element_exists", "element_enabled", "element_checked":
		if boundProcess == nil {
			return false, nil, nil, coreError("element_stale")
		}
		windows, err := s.Windows(ctx)
		if err != nil {
			return false, nil, nil, err
		}
		windowFound := false
		for _, window := range windows {
			if window.Ref == params.WindowRef {
				if window.Process != *boundProcess {
					return false, nil, nil, coreError("element_stale")
				}
				windowFound = true
				break
			}
		}
		if !windowFound {
			return false, nil, nil, coreError("element_stale")
		}
		observation, err := s.Observe(ctx, params.WindowRef)
		if err != nil {
			return false, nil, nil, err
		}
		stateID := observation.StateID
		state := &stateID
		if !observation.Coverage.Complete {
			return false, state, nil, coreError("backend_unavailable")
		}
		element := findNormalElement(observation, params.ElementRef)
		if element == nil || boundElement == nil || element.Ref != boundElement.Ref || element.Role != boundElement.Role || element.Classification != boundElement.Classification {
			return false, state, nil, coreError("element_stale")
		}
		if params.Condition == "element_exists" {
			windowRef := params.WindowRef
			return true, state, &windowRef, nil
		}
		var value *bool
		if params.Condition == "element_enabled" {
			value = element.Enabled
		} else {
			value = element.Checked
		}
		if value == nil {
			return false, state, nil, coreError("backend_unavailable")
		}
		windowRef := params.WindowRef
		return *value == *params.Expected, state, &windowRef, nil
	default:
		return false, nil, nil, coreError("invalid_request")
	}
}

func (s *Session) revalidateWaitIdentity(params WaitParams, boundProcess *ProcessIdentity) error {
	if params.Condition == "window_appears" {
		return s.validateWaitProcessRef(params.ProcessRef)
	}
	if boundProcess != nil {
		_, err := s.ProcessRef(*boundProcess)
		return err
	}
	return nil
}

func (s *Session) validateWaitProcessRef(processRef string) error {
	for _, process := range s.scope.Processes {
		ref, err := s.ProcessRef(process)
		if err != nil {
			return err
		}
		if ref == processRef {
			return nil
		}
	}
	return coreError("policy_refused")
}

func isElementWait(condition string) bool {
	return condition == "element_exists" || condition == "element_enabled" || condition == "element_checked"
}

func findNormalElement(observation Observation, ref string) *Element {
	for i := range observation.Elements {
		element := &observation.Elements[i]
		if element.Ref == ref && element.Classification == "normal" {
			return element
		}
	}
	return nil
}

func elapsedMilliseconds(started time.Time) uint64 {
	elapsed := time.Since(started)
	if elapsed < 0 {
		return 0
	}
	return uint64(elapsed / time.Millisecond)
}
