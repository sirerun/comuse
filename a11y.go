package comuse

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sirerun/comuse/internal/semantic"
)

type snapshotBinding struct {
	public      Observation
	nativeState string
	canonical   []byte
	storedAt    time.Time
	storageSize int
}

// Doctor returns bounded, sanitized backend capability and permission status.
func (s *Session) Doctor(ctx context.Context) (DoctorReport, error) {
	callCtx, done, err := s.enter(ctx)
	if err != nil {
		return DoctorReport{}, err
	}
	defer done()
	if observed, _ := callCtx.Value(stateAccountingKey{}).(bool); observed {
		s.account(callCtx, CounterObservationState, 1)
	}
	report, callErr := s.backend.Doctor(callCtx)
	if callErr != nil {
		return DoctorReport{}, s.stableBackendError(callCtx, callErr)
	}
	// Reasons and permission values are implementation diagnostics. Retain only
	// the closed capability booleans and known, stable permission statuses.
	report.Capabilities.Reasons = nil
	report.Capabilities.ActionKinds = allowedActions(report.Capabilities.ActionKinds)
	permissions := make(map[string]string)
	for name, state := range report.Permissions {
		if !knownPermission(name) {
			continue
		}
		if safePermissionState(state) {
			permissions[name] = state
		}
	}
	report.Permissions = permissions
	s.invalidateIfPermissionDenied(report)
	return report, nil
}

// State returns the same bounded runtime status as Doctor.
func (s *Session) State(ctx context.Context) (DoctorReport, error) {
	if ctx == nil {
		return DoctorReport{}, coreError("invalid_request")
	}
	return s.Doctor(context.WithValue(ctx, stateAccountingKey{}, true))
}

// Windows lists only windows whose exact process identity is in the session scope.
func (s *Session) Windows(ctx context.Context) ([]Window, error) {
	callCtx, done, err := s.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	epoch := s.currentPermissionEpoch()
	s.account(callCtx, CounterObservationState, 1)
	windows, callErr := s.backend.Windows(callCtx, s.budget)
	if callErr != nil {
		return nil, s.stableBackendError(callCtx, callErr)
	}
	if len(windows) > s.budget.MaxNodes {
		return nil, coreError("budget_exceeded")
	}
	seen := make(map[string]struct{}, len(windows))
	filtered := make([]Window, 0, len(windows))
	for _, window := range windows {
		if !opaqueASCII(window.Ref) || !scopeContains(s.scope, window.Process) || !validText(window.Title, s.budget.MaxBytes) {
			continue
		}
		if _, exists := seen[window.Ref]; exists {
			return nil, coreError("backend_unavailable")
		}
		seen[window.Ref] = struct{}{}
		filtered = append(filtered, window)
	}
	if exceedsJSONBudget(filtered, s.budget.MaxBytes) {
		return nil, coreError("budget_exceeded")
	}
	s.mu.Lock()
	if s.permissionEpoch != epoch {
		s.mu.Unlock()
		return nil, coreError("permission_denied")
	}
	s.windows = make(map[string]Window, len(filtered))
	for _, window := range filtered {
		s.windows[window.Ref] = window
	}
	s.mu.Unlock()
	return filtered, nil
}

// Observe returns a bounded snapshot after conservative filtering and redaction.
func (s *Session) Observe(ctx context.Context, windowRef string) (Observation, error) {
	if !opaqueASCII(windowRef) {
		return Observation{}, coreError("invalid_request")
	}
	callCtx, done, err := s.enter(ctx)
	if err != nil {
		return Observation{}, err
	}
	defer done()
	epoch := s.currentPermissionEpoch()
	s.mu.Lock()
	_, exists := s.windows[windowRef]
	s.mu.Unlock()
	if !exists {
		return Observation{}, coreError("element_stale")
	}
	s.account(callCtx, CounterObservationA11y, 1)
	native, callErr := s.backend.Observe(callCtx, windowRef, s.budget)
	if callErr != nil {
		return Observation{}, s.stableBackendError(callCtx, callErr)
	}
	projection, binding, normalizeErr := s.normalizeObservation(windowRef, native)
	if normalizeErr != nil {
		return Observation{}, normalizeErr
	}
	if exceedsJSONBudget(projection, s.budget.MaxBytes) {
		return Observation{}, coreError("budget_exceeded")
	}
	if !s.rememberSnapshotAtEpoch(binding, epoch) {
		return Observation{}, coreError("permission_denied")
	}
	return cloneObservation(projection), nil
}

// ReadElement returns explicitly requested text only after fresh state validation.
func (s *Session) ReadElement(ctx context.Context, windowRef, elementRef, stateID string) (ElementContent, error) {
	if !opaqueASCII(windowRef) || !opaqueASCII(elementRef) || !opaqueASCII(stateID) {
		return ElementContent{}, coreError("invalid_request")
	}
	callCtx, done, err := s.enter(ctx)
	if err != nil {
		return ElementContent{}, err
	}
	defer done()
	epoch := s.currentPermissionEpoch()
	prior, ok := s.findSnapshot(windowRef, stateID)
	if !ok {
		return ElementContent{}, coreError("state_expired")
	}
	if !normalTarget(prior.public, elementRef) {
		return ElementContent{}, coreError("element_stale")
	}
	if _, ok := s.window(windowRef); !ok {
		return ElementContent{}, coreError("element_stale")
	}
	s.account(callCtx, CounterObservationA11y, 1)
	current, callErr := s.backend.Observe(callCtx, windowRef, s.budget)
	if callErr != nil {
		return ElementContent{}, s.stableBackendError(callCtx, callErr)
	}
	projection, binding, normalizeErr := s.normalizeObservation(windowRef, current)
	if normalizeErr != nil {
		return ElementContent{}, normalizeErr
	}
	if !s.rememberSnapshotAtEpoch(binding, epoch) {
		return ElementContent{}, coreError("permission_denied")
	}
	if projection.StateID != stateID {
		return ElementContent{}, coreError("element_stale")
	}
	s.account(callCtx, CounterObservationA11y, 1)
	content, callErr := s.backend.ReadElement(callCtx, windowRef, elementRef, binding.nativeState, s.budget)
	if callErr != nil {
		return ElementContent{}, s.stableBackendError(callCtx, callErr)
	}
	if s.currentPermissionEpoch() != epoch {
		return ElementContent{}, coreError("permission_denied")
	}
	if content.WindowRef != windowRef || content.ElementRef != elementRef || content.StateID != binding.nativeState || !validText(content.Text, s.budget.MaxBytes) {
		return ElementContent{}, coreError("backend_unavailable")
	}
	content.StateID = projection.StateID
	content.ObservedAt = projection.ObservedAt
	if len(content.Text) > 8192 {
		return ElementContent{}, coreError("budget_exceeded")
	}
	if exceedsJSONBudget(content, s.budget.MaxBytes) {
		return ElementContent{}, coreError("budget_exceeded")
	}
	return content, nil
}

// Wait performs one fresh bounded observation within the requested timeout.
func (s *Session) Wait(ctx context.Context, windowRef string, timeout time.Duration) (Observation, error) {
	if s == nil || ctx == nil {
		return Observation{}, coreError("invalid_request")
	}
	if timeout <= 0 || timeout > s.budget.Timeout {
		return Observation{}, coreError("budget_exceeded")
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Wait is one fresh, bounded observation. It does not imply that a
	// condition changed or retry an operation until it appears to succeed.
	return s.Observe(waitCtx, windowRef)
}

func (s *Session) normalizeObservation(windowRef string, native Observation) (Observation, snapshotBinding, error) {
	if native.WindowRef != windowRef || !opaqueASCII(native.StateID) || len(native.Elements) > s.budget.MaxNodes {
		return Observation{}, snapshotBinding{}, coreError("backend_unavailable")
	}
	filtered := make([]Element, 0, len(native.Elements))
	seen := make(map[string]struct{}, len(native.Elements))
	for _, element := range native.Elements {
		if element.Classification != "normal" {
			continue
		}
		if !opaqueASCII(element.Ref) || (element.ParentRef != "" && !opaqueASCII(element.ParentRef)) || element.Order < 0 || element.Role == "" || !validText(element.Role, s.budget.MaxBytes) || !validText(element.Label, s.budget.MaxBytes) || len(element.Actions) > 64 {
			return Observation{}, snapshotBinding{}, coreError("backend_unavailable")
		}
		if _, exists := seen[element.Ref]; exists {
			return Observation{}, snapshotBinding{}, coreError("backend_unavailable")
		}
		seen[element.Ref] = struct{}{}
		copyElement := Element{Ref: element.Ref, ParentRef: element.ParentRef, Order: element.Order, Role: element.Role, Label: element.Label, Enabled: cloneBool(element.Enabled), Classification: "normal"}
		if s.allowValues && element.Value != nil {
			if !validText(*element.Value, s.budget.MaxBytes) {
				return Observation{}, snapshotBinding{}, coreError("backend_unavailable")
			}
			copyElement.Value = cloneString(element.Value)
		}
		copyElement.Actions = allowedActions(element.Actions)
		filtered = append(filtered, copyElement)
	}
	filtered = retainValidParentTree(filtered)
	if len(filtered) > s.budget.MaxNodes || topologyTooDeep(filtered, s.budget.MaxDepth) {
		return Observation{}, snapshotBinding{}, coreError("budget_exceeded")
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Ref < filtered[j].Ref })
	coverage := Coverage{Complete: native.Coverage.Complete}
	if !native.Coverage.Complete {
		coverage.Reason = safeCoverageReason(native.Coverage.Reason)
	}
	public := Observation{WindowRef: windowRef, ObservedAt: s.now().UTC(), Elements: filtered, Coverage: coverage}
	full, projectionErr := buildFullObservation(s, public)
	if projectionErr != nil {
		return Observation{}, snapshotBinding{}, projectionErr
	}
	canonical, err := semantic.CanonicalBytes(full)
	if err != nil || len(canonical) > s.budget.MaxBytes {
		return Observation{}, snapshotBinding{}, coreError("budget_exceeded")
	}
	public.StateID = full.StateID
	publicJSON, err := json.Marshal(public)
	if err != nil || len(publicJSON) > s.budget.MaxBytes {
		return Observation{}, snapshotBinding{}, coreError("budget_exceeded")
	}
	now := s.now()
	storageSize := len(canonical) + len(publicJSON) + semantic.BindingCharge*(len(public.Elements)+2)
	if storageSize > maxSnapshotStorage {
		return Observation{}, snapshotBinding{}, coreError("budget_exceeded")
	}
	return public, snapshotBinding{public: public, nativeState: native.StateID, canonical: canonical, storedAt: now, storageSize: storageSize}, nil
}

func (s *Session) rememberSnapshotAtEpoch(binding snapshotBinding, epoch uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.permissionEpoch != epoch {
		return false
	}
	s.pruneSnapshotsLocked(s.now())
	rows := s.snapshots[binding.public.WindowRef]
	for i, row := range rows {
		if row.public.StateID == binding.public.StateID {
			s.snapshotBytes -= row.storageSize
			rows = append(rows[:i], rows[i+1:]...)
			break
		}
	}
	binding.public = cloneObservation(binding.public)
	binding.canonical = append([]byte(nil), binding.canonical...)
	rows = append(rows, binding)
	s.snapshotBytes += binding.storageSize
	for len(rows) > maxSnapshotGenerations {
		s.snapshotBytes -= rows[0].storageSize
		rows = rows[1:]
	}
	s.snapshots[binding.public.WindowRef] = rows
	for s.snapshotBytes > maxSnapshotStorage {
		oldWindow := ""
		oldIndex := -1
		var oldest time.Time
		for windowRef, candidates := range s.snapshots {
			for i, candidate := range candidates {
				if oldIndex < 0 || candidate.storedAt.Before(oldest) {
					oldWindow, oldIndex, oldest = windowRef, i, candidate.storedAt
				}
			}
		}
		if oldIndex < 0 {
			break
		}
		rows := s.snapshots[oldWindow]
		s.snapshotBytes -= rows[oldIndex].storageSize
		rows = append(rows[:oldIndex], rows[oldIndex+1:]...)
		if len(rows) == 0 {
			delete(s.snapshots, oldWindow)
		} else {
			s.snapshots[oldWindow] = rows
		}
	}
	return true
}

func (s *Session) findSnapshot(windowRef, stateID string) (snapshotBinding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneSnapshotsLocked(s.now())
	for _, binding := range s.snapshots[windowRef] {
		if binding.public.StateID == stateID {
			binding.public = cloneObservation(binding.public)
			binding.canonical = append([]byte(nil), binding.canonical...)
			return binding, true
		}
	}
	return snapshotBinding{}, false
}

func (s *Session) pruneSnapshotsLocked(now time.Time) {
	for windowRef, rows := range s.snapshots {
		kept := rows[:0]
		for _, row := range rows {
			if now.Sub(row.storedAt) > snapshotTTL {
				s.snapshotBytes -= row.storageSize
				continue
			}
			kept = append(kept, row)
		}
		if len(kept) == 0 {
			delete(s.snapshots, windowRef)
		} else {
			s.snapshots[windowRef] = kept
		}
	}
}

func (s *Session) window(windowRef string) (Window, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	window, ok := s.windows[windowRef]
	return window, ok
}

func normalTarget(snapshot Observation, ref string) bool {
	for _, element := range snapshot.Elements {
		if element.Ref == ref && element.Classification == "normal" {
			return true
		}
	}
	return false
}

func allowedActions(actions []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(actions))
	for _, action := range actions {
		switch action {
		case ActionPress, ActionReplace, ActionInsert, ActionScroll, ActionPick, ActionFocus:
			if _, exists := seen[action]; exists {
				continue
			}
			seen[action] = struct{}{}
			result = append(result, action)
		}
	}
	sort.Strings(result)
	return result
}

func retainValidParentTree(elements []Element) []Element {
	byRef := make(map[string]Element, len(elements))
	for _, element := range elements {
		byRef[element.Ref] = element
	}
	keep := make(map[string]bool, len(elements))
	for _, element := range elements {
		if element.ParentRef == "" {
			keep[element.Ref] = true
		} else if _, exists := byRef[element.ParentRef]; exists {
			keep[element.Ref] = true
		}
	}
	changed := true
	for changed {
		changed = false
		for ref := range keep {
			element := byRef[ref]
			if element.ParentRef != "" && !keep[element.ParentRef] {
				delete(keep, ref)
				changed = true
			}
		}
	}
	result := make([]Element, 0, len(keep))
	for _, element := range elements {
		if keep[element.Ref] {
			result = append(result, element)
		}
	}
	return result
}

func topologyTooDeep(elements []Element, limit int) bool {
	parents := make(map[string]string, len(elements))
	for _, element := range elements {
		parents[element.Ref] = element.ParentRef
	}
	for ref := range parents {
		depth := 1
		seen := map[string]struct{}{ref: {}}
		for parent := parents[ref]; parent != ""; parent = parents[parent] {
			if _, exists := seen[parent]; exists {
				return true
			}
			seen[parent] = struct{}{}
			depth++
			if depth > limit {
				return true
			}
		}
	}
	return false
}

func safeCoverageReason(reason string) string {
	switch strings.ToLower(reason) {
	case "depth_limit", "node_limit", "byte_limit", "deadline", "unsupported", "permission_denied":
		return strings.ToLower(reason)
	default:
		return "partial"
	}
}

func knownPermission(name string) bool {
	switch name {
	case "accessibility", "input", "screen_capture":
		return true
	default:
		return false
	}
}

func safePermissionState(state string) bool {
	switch state {
	case "granted", "denied", "not_determined", "unknown":
		return true
	default:
		return false
	}
}

func validText(value string, limit int) bool {
	return utf8.ValidString(value) && len(value) <= limit
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneObservation(value Observation) Observation {
	clone := value
	clone.Elements = make([]Element, len(value.Elements))
	for i, element := range value.Elements {
		clone.Elements[i] = element
		clone.Elements[i].Value = cloneString(element.Value)
		clone.Elements[i].Enabled = cloneBool(element.Enabled)
		clone.Elements[i].Actions = append([]string(nil), element.Actions...)
	}
	return clone
}

func exceedsJSONBudget(value any, limit int) bool {
	encoded, err := json.Marshal(value)
	return err != nil || len(encoded) > limit
}

func stableContextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return stableCallError(ctx.Err())
	}
	return sanitizedBackendError(err)
}
