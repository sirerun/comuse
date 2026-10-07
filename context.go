package comuse

import (
	"context"
	"unicode/utf8"

	"github.com/sirerun/comuse/internal/backend"
	"github.com/sirerun/comuse/internal/semantic"
)

type DesktopContext = backend.DesktopContext

const maxDesktopGeneration = semantic.MaxJSONInteger

func cloneDesktopContext(value *DesktopContext) *DesktopContext {
	if value == nil {
		return nil
	}
	copy := *value
	if value.FocusedWindow != nil {
		window := *value.FocusedWindow
		copy.FocusedWindow = &window
	}
	return &copy
}

func (s *Session) validateDesktopContext(value *DesktopContext) (*DesktopContext, error) {
	if value == nil {
		return nil, nil
	}
	if !opaqueASCII(value.DisplayID) || value.DisplayGeneration == 0 || value.DisplayGeneration > maxDesktopGeneration {
		return nil, coreError("backend_unavailable")
	}
	copy := cloneDesktopContext(value)
	if copy.FocusedWindow != nil {
		window := *copy.FocusedWindow
		if !opaqueASCII(window.Ref) || !scopeContains(s.scope, window.Process) || !utf8.ValidString(window.Title) || len(window.Title) > 4096 || !validText(window.Title, s.budget.MaxBytes) {
			return nil, coreError("backend_unavailable")
		}
	}
	return copy, nil
}

// bindContextFocus makes a freshly inspected, exactly scoped focused reference
// usable without requiring a redundant enumeration. Epoch checks prevent an
// older Doctor response from restoring invalidated authority.
func (s *Session) bindContextFocus(value *DesktopContext, permissionEpoch, contextEpoch uint64) error {
	if value == nil || value.FocusedWindow == nil {
		return nil
	}
	window := *value.FocusedWindow
	if exceedsJSONBudget(window, s.budget.MaxBytes) {
		return coreError("budget_exceeded")
	}
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.permissionEpoch != permissionEpoch {
		return coreError("permission_denied")
	}
	if s.contextEpoch != contextEpoch {
		return coreError("state_expired")
	}
	if !s.hasDesktopContext || s.desktopContext.DisplayID != value.DisplayID || s.desktopContext.DisplayGeneration != value.DisplayGeneration {
		return coreError("state_expired")
	}
	if _, exists := s.windows[window.Ref]; !exists && len(s.windows) >= s.budget.MaxNodes {
		return coreError("budget_exceeded")
	}
	s.windows[window.Ref] = window
	return nil
}

// reconcileDesktopContext atomically installs inspected context evidence.
// A display identity/generation transition invalidates every retained binding
// and advances a distinct authority epoch; it is never a permission failure.
func (s *Session) reconcileDesktopContextAt(value *DesktopContext, expectedEpoch *uint64) (changed bool, current *DesktopContext, err error) {
	validated, err := s.validateDesktopContext(value)
	if err != nil {
		return false, nil, err
	}
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedEpoch != nil && s.contextEpoch != *expectedEpoch {
		return false, cloneDesktopContext(s.desktopContext), coreError("state_expired")
	}
	displayChanged := false
	switch {
	case validated == nil && s.hasDesktopContext:
		displayChanged = true
	case validated != nil && !s.hasDesktopContext:
		// Establishing context after windows or snapshots were retained also
		// rotates authority because those bindings predate the trusted facts.
		displayChanged = len(s.windows) != 0 || len(s.snapshots) != 0
	case validated != nil && s.hasDesktopContext:
		displayChanged = s.desktopContext.DisplayID != validated.DisplayID ||
			s.desktopContext.DisplayGeneration != validated.DisplayGeneration
	}
	if displayChanged {
		s.contextEpoch++
		s.clearSemanticStateLocked()
	}
	s.desktopContext = validated
	s.hasDesktopContext = validated != nil
	return displayChanged, cloneDesktopContext(validated), nil
}

func (s *Session) contextEpochNow() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contextEpoch
}

func compactState(value *DesktopContext) (*MetadataCompactState, StateStatus, error) {
	if value == nil {
		return nil, StateUnavailable, nil
	}
	copy := &MetadataCompactState{
		DisplayID:         value.DisplayID,
		DisplayGeneration: value.DisplayGeneration,
	}
	if value.FocusedWindow != nil {
		copy.FocusedWindow = &MetadataFocusedWindow{Ref: value.FocusedWindow.Ref, Title: value.FocusedWindow.Title}
	}
	return copy, StateAvailable, nil
}

// revalidateDesktopAuthority is the stored-state seam: it performs one fresh
// Doctor read and no Snapshot/Observe call, then checks the captured authority.
func (s *Session) revalidateDesktopAuthority(ctx context.Context, expectedEpoch uint64) (*MetadataCompactState, StateStatus, error) {
	if observed, _ := ctx.Value(stateAccountingKey{}).(bool); observed {
		s.account(ctx, CounterObservationState, 1)
	}
	report, err := s.backend.Doctor(ctx)
	if err != nil {
		return nil, StateUnavailable, s.stableBackendError(ctx, err)
	}
	if _, _, err = s.reconcileDesktopContextAt(report.DesktopContext, &expectedEpoch); err != nil {
		return nil, StateUnavailable, err
	}
	s.invalidateIfPermissionDenied(report)
	if !report.Capabilities.Accessibility || report.Permissions["accessibility"] == "denied" {
		return nil, StateUnavailable, coreError("permission_denied")
	}
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	if s.contextEpochNow() != expectedEpoch {
		return nil, StateUnavailable, coreError("state_expired")
	}
	s.mu.Lock()
	value := cloneDesktopContext(s.desktopContext)
	s.mu.Unlock()
	return compactState(value)
}

func desktopScopeBinding(value *DesktopContext) any {
	if value == nil {
		return struct {
			Known bool `json:"known"`
		}{false}
	}
	return struct {
		Known      bool   `json:"known"`
		DisplayID  string `json:"display_id"`
		Generation uint64 `json:"display_generation"`
	}{true, value.DisplayID, value.DisplayGeneration}
}
