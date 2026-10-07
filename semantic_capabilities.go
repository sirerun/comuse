package comuse

import "context"

func qualifiedActionKind(capabilities Capabilities, kind string) bool {
	for _, allowed := range capabilities.ActionKinds {
		if allowed == kind {
			return true
		}
	}
	return false
}

func qualifiedDeveloperActionKind(capabilities Capabilities, kind string) bool {
	switch kind {
	case developerActionClick, developerActionTypeText, developerActionPressKey, developerActionCoordinateScroll, developerActionDrag, developerActionFocusWindow:
		return qualifiedActionKind(capabilities, kind)
	default:
		return false
	}
}

// SemanticOperations reports only per-operation qualified backend capability
// combined with the trusted host's mutation composition. JSON cannot set it.
// Read-only default hosts perform no capability probe here.
func (s *Session) SemanticOperations(ctx context.Context) ([]Operation, error) {
	operations := []Operation{}
	if s == nil || ctx == nil {
		return operations, coreError("invalid_request")
	}
	if !s.mutationEnabled {
		return operations, nil
	}
	report, err := s.Doctor(ctx)
	if err != nil {
		return operations, err
	}
	c := report.Capabilities
	if !c.Accessibility || !c.Input || !c.QualifiedInput {
		return operations, nil
	}
	if qualifiedActionKind(c, ActionPress) {
		operations = append(operations, OperationClickElement)
	}
	if qualifiedActionKind(c, ActionPress) || qualifiedActionKind(c, ActionPick) || qualifiedActionKind(c, ActionFocus) {
		operations = append(operations, OperationElementAction)
	}
	if qualifiedActionKind(c, ActionReplace) || qualifiedActionKind(c, ActionInsert) {
		operations = append(operations, OperationWriteElement)
	}
	if qualifiedActionKind(c, ActionScroll) {
		operations = append(operations, OperationScrollElement)
	}
	return operations, nil
}
