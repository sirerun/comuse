package comuse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/sirerun/comuse/internal/semantic"
)

// projectResult converts only public typed results into the frozen closed
// metadata DTO vocabulary. Arbitrary maps and raw JSON are never accepted.
func projectResult(value any) (ResultPayload, error) {
	switch v := value.(type) {
	case nil:
		return NewResultPayload(nil)
	case DoctorReport:
		return NewResultPayload(projectDoctor(v))
	case []Window:
		return NewResultPayload(projectWindows(v))
	case LedgerSnapshot:
		return NewResultPayload(v)
	case ActionResult:
		return NewResultPayload(MetadataActionResult{ActionID: v.ActionID, Execution: string(v.Execution), Verification: MetadataVerification{Status: string(v.Verification.Status), Reason: v.Verification.Reason}, StateStatus: string(StateUnavailable), Cleanup: string(v.Cleanup)})
	case Observation:
		return NewResultPayload(projectLegacyObservation(v))
	case ReadResult:
		return NewResultPayload(projectReadResult(v))
	case WaitResult:
		return NewResultPayload(MetadataWaitResult{Condition: v.Condition, Satisfied: v.Satisfied, ElapsedMS: v.ElapsedMS, Reason: v.Reason, FinalStateID: cloneString(v.FinalStateID), WindowRef: cloneString(v.WindowRef)})
	default:
		return ResultPayload{}, ErrInvalidPayload
	}
}

// projectObservation is the closed counterpart used for exact observation
// records. Its input must already have passed the core's sanitization.
func projectObservation(value any) (ObservationPayload, error) {
	switch v := value.(type) {
	case nil:
		return NewObservationPayload(nil)
	case Observation:
		return NewObservationPayload(projectLegacyObservation(v))
	case semantic.Snapshot:
		return NewObservationPayload(projectSemanticSnapshot(v))
	case semantic.Delta:
		return NewObservationPayload(projectSemanticDelta(v))
	case semantic.Unchanged:
		return NewObservationPayload(projectSemanticUnchanged(v))
	case ReadResult:
		return NewObservationPayload(projectReadResult(v))
	default:
		return ObservationPayload{}, ErrInvalidPayload
	}
}

func projectDoctor(value DoctorReport) MetadataDoctor {
	permissions := make(map[string]string, len(value.Permissions))
	for key, permission := range value.Permissions {
		permissions[key] = permission
	}
	if permissions == nil {
		permissions = map[string]string{}
	}
	return MetadataDoctor{Capabilities: MetadataCapabilities{
		Accessibility: value.Capabilities.Accessibility, Input: value.Capabilities.Input,
		ScreenCapture: value.Capabilities.ScreenCapture, QualifiedInput: value.Capabilities.QualifiedInput,
		Reasons: append([]string{}, value.Capabilities.Reasons...),
	}, Permissions: permissions}
}

func projectWindows(values []Window) []MetadataWindow {
	result := make([]MetadataWindow, 0, len(values))
	for _, value := range values {
		result = append(result, MetadataWindow{Ref: value.Ref, Title: value.Title, Process: MetadataProcessIdentity{PID: value.Process.PID, BundleID: value.Process.BundleID, LaunchID: value.Process.LaunchID}})
	}
	return result
}

func projectLegacyObservation(value Observation) MetadataLegacySnapshot {
	elements := make([]MetadataLegacyElement, 0, len(value.Elements))
	for _, element := range value.Elements {
		elements = append(elements, MetadataLegacyElement{
			Ref: element.Ref, ParentRef: element.ParentRef, Order: element.Order, Role: element.Role,
			Label: element.Label, Value: cloneString(element.Value), Enabled: cloneBool(element.Enabled),
			Actions: append([]string{}, element.Actions...), Classification: element.Classification,
		})
	}
	return MetadataLegacySnapshot{WindowRef: value.WindowRef, StateID: value.StateID, ObservedAt: value.ObservedAt.Format(time.RFC3339Nano),
		Coverage: MetadataLegacyCoverage{Complete: value.Coverage.Complete, Reason: value.Coverage.Reason}, Elements: elements}
}

func projectReadResult(value ReadResult) MetadataElementContent {
	return MetadataElementContent{Kind: value.Kind, WindowRef: value.WindowRef, ElementRef: value.ElementRef, StateID: value.StateID,
		ObservedAt: value.ObservedAt.Format(time.RFC3339Nano), Text: value.Text, Truncated: value.Truncated}
}

func projectSemanticSnapshot(value semantic.Snapshot) MetadataSnapshot {
	nodes := make(map[string]MetadataNode, len(value.Nodes))
	for ref, node := range value.Nodes {
		nodes[ref] = MetadataNode{ParentRef: cloneString(node.ParentRef), ChildRefs: append([]string{}, node.ChildRefs...), Role: node.Role,
			Label: cloneString(node.Label), Value: cloneString(node.Value), Enabled: cloneBool(node.Enabled), Checked: cloneBool(node.Checked),
			Selected: cloneBool(node.Selected), Actions: append([]string{}, node.Actions...), Classification: node.Classification}
	}
	return MetadataSnapshot{SchemaVersion: value.SchemaVersion, ScopeID: value.ScopeID, WindowRef: value.WindowRef, StateID: value.StateID,
		ObservedAt: value.ObservedAt.Format(time.RFC3339Nano), ActionSequence: value.ActionSequence,
		Coverage: MetadataCoverage{Status: value.Coverage.Status, Truncated: value.Coverage.Truncated, Limitations: append([]string{}, value.Coverage.Limitations...)},
		Context:  MetadataContext{RootRefs: append([]string{}, value.Context.RootRefs...), FocusedElementRef: cloneString(value.Context.FocusedElementRef)},
		Nodes:    nodes, Kind: value.Kind, Historical: value.Historical, ResetReason: optionalString(value.ResetReason)}
}

func projectSemanticDelta(value semantic.Delta) MetadataDelta {
	upsert := make(map[string]MetadataNode, len(value.Upsert))
	for ref, node := range value.Upsert {
		upsert[ref] = MetadataNode{ParentRef: cloneString(node.ParentRef), ChildRefs: append([]string{}, node.ChildRefs...), Role: node.Role,
			Label: cloneString(node.Label), Value: cloneString(node.Value), Enabled: cloneBool(node.Enabled), Checked: cloneBool(node.Checked),
			Selected: cloneBool(node.Selected), Actions: append([]string{}, node.Actions...), Classification: node.Classification}
	}
	return MetadataDelta{SchemaVersion: value.SchemaVersion, ScopeID: value.ScopeID, WindowRef: value.WindowRef, StateID: value.StateID,
		BaseStateID: value.BaseStateID, ObservedAt: value.ObservedAt.Format(time.RFC3339Nano), ActionSequence: value.ActionSequence,
		Coverage: MetadataCoverage{Status: value.Coverage.Status, Truncated: value.Coverage.Truncated, Limitations: append([]string{}, value.Coverage.Limitations...)},
		Context:  MetadataContext{RootRefs: append([]string{}, value.Context.RootRefs...), FocusedElementRef: cloneString(value.Context.FocusedElementRef)},
		Upsert:   upsert, Removed: append([]string{}, value.Removed...), Kind: value.Kind}
}

func projectSemanticUnchanged(value semantic.Unchanged) MetadataUnchanged {
	return MetadataUnchanged{SchemaVersion: value.SchemaVersion, ScopeID: value.ScopeID, WindowRef: value.WindowRef, StateID: value.StateID,
		BaseStateID: value.BaseStateID, ObservedAt: value.ObservedAt.Format(time.RFC3339Nano), ActionSequence: value.ActionSequence,
		Coverage: MetadataCoverage{Status: value.Coverage.Status, Truncated: value.Coverage.Truncated, Limitations: append([]string{}, value.Coverage.Limitations...)},
		Context:  MetadataContext{RootRefs: append([]string{}, value.Context.RootRefs...), FocusedElementRef: cloneString(value.Context.FocusedElementRef)}, Kind: value.Kind}
}

type projectionBinding struct {
	SessionID     string `json:"session_id"`
	Scope         Scope  `json:"scope"`
	Budget        Budget `json:"budget"`
	AllowValues   bool   `json:"allow_values"`
	PolicyVersion uint64 `json:"policy_version"`
	Epoch         uint64 `json:"permission_epoch"`
}

// projectFullObservation binds a sanitized legacy record to this immutable
// session configuration, then delegates redaction and identity to Normalize.
func projectFullObservation(session *Session, observation Observation) (semantic.Snapshot, error) {
	if session == nil {
		return semantic.Snapshot{}, coreError("invalid_request")
	}
	session.mu.Lock()
	bound := false
	for _, item := range session.snapshots[observation.WindowRef] {
		if item.public.StateID == observation.StateID {
			bound = true
			break
		}
	}
	session.mu.Unlock()
	if !bound {
		return semantic.Snapshot{}, coreError("state_expired")
	}
	return buildFullObservation(session, observation)
}

// buildFullObservation is used only after core redaction and topology policy.
func buildFullObservation(session *Session, observation Observation) (semantic.Snapshot, error) {
	session.mu.Lock()
	metadata := projectionBinding{SessionID: session.sessionID, Scope: session.scope, Budget: session.budget, AllowValues: session.allowValues, PolicyVersion: actionPolicyVersion, Epoch: session.permissionEpoch}
	actionSequence := session.actionSequence
	session.mu.Unlock()
	trusted, err := json.Marshal(metadata)
	if err != nil {
		return semantic.Snapshot{}, coreError("internal_error")
	}
	scopeHash := sha256.Sum256(trusted)
	nodes := make(map[string]semantic.Node, len(observation.Elements))
	type orderedRef struct {
		ref   string
		order int
	}
	children := make(map[string][]orderedRef)
	roots := make([]orderedRef, 0)
	for _, element := range observation.Elements {
		var parent *string
		if element.ParentRef != "" {
			parent = cloneString(&element.ParentRef)
			children[element.ParentRef] = append(children[element.ParentRef], orderedRef{element.Ref, element.Order})
		} else {
			roots = append(roots, orderedRef{element.Ref, element.Order})
		}
		nodes[element.Ref] = semantic.Node{Role: element.Role, Classification: element.Classification, Label: nonemptyString(element.Label),
			Value: cloneString(element.Value), ParentRef: parent, ChildRefs: []string{}, Enabled: cloneBool(element.Enabled),
			Actions: append([]string{}, element.Actions...)}
	}
	byOrder := func(refs []orderedRef) {
		sort.SliceStable(refs, func(i, j int) bool {
			if refs[i].order == refs[j].order {
				return refs[i].ref < refs[j].ref
			}
			return refs[i].order < refs[j].order
		})
	}
	byOrder(roots)
	rootRefs := make([]string, 0, len(roots))
	for _, root := range roots {
		rootRefs = append(rootRefs, root.ref)
	}
	for ref, refs := range children {
		byOrder(refs)
		node := nodes[ref]
		for _, child := range refs {
			node.ChildRefs = append(node.ChildRefs, child.ref)
		}
		nodes[ref] = node
	}
	coverage := semantic.Coverage{Status: "complete", Limitations: []string{}}
	if !observation.Coverage.Complete {
		coverage.Status = "partial"
		if observation.Coverage.Reason != "" {
			coverage.Limitations = append(coverage.Limitations, observation.Coverage.Reason)
		}
	}
	candidate := semantic.Snapshot{Metadata: semantic.Metadata{SchemaVersion: semantic.SchemaVersion, ScopeID: hex.EncodeToString(scopeHash[:]),
		WindowRef: observation.WindowRef, ObservedAt: observation.ObservedAt, ActionSequence: actionSequence,
		Coverage: coverage, Context: semantic.Context{RootRefs: rootRefs}}, Kind: "snapshot", Nodes: nodes, Historical: false}
	normalized, err := semantic.Normalize(candidate)
	if err != nil {
		return semantic.Snapshot{}, coreError("internal_error")
	}
	return normalized, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func nonemptyString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
