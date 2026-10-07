package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalidRequest = errors.New("invalid_request")
	ErrBudgetExceeded = errors.New("budget_exceeded")
	ErrPolicyRefused  = errors.New("policy_refused")
	ErrStateExpired   = errors.New("state_expired")
	ErrUnsupported    = errors.New("unsupported")
)

// Normalize returns a detached, redacted snapshot with canonical action order
// and a verified semantic identity. Redaction occurs before limits or hashes.
func Normalize(s Snapshot) (Snapshot, error) {
	s = CloneSnapshot(s)
	if s.Coverage.Limitations == nil {
		s.Coverage.Limitations = []string{}
	}
	if s.Context.RootRefs == nil {
		s.Context.RootRefs = []string{}
	}
	sort.Strings(s.Context.RootRefs)
	for ref, n := range s.Nodes {
		if n.Classification == "secure" || n.Classification == "unknown" {
			n.Label, n.Value = nil, nil
			n.Enabled, n.Checked, n.Selected = nil, nil, nil
			n.Actions = nil
		}
		if n.ChildRefs == nil {
			n.ChildRefs = []string{}
		}
		if n.Actions == nil {
			n.Actions = []string{}
		}
		sort.Strings(n.Actions)
		n.Actions = compactStrings(n.Actions)
		s.Nodes[ref] = n
	}
	if err := validateSnapshot(s); err != nil {
		return Snapshot{}, err
	}
	full, err := canonicalSnapshot(s)
	if err != nil {
		return Snapshot{}, ErrInvalidRequest
	}
	if len(full) > MaxStateBytes {
		return Snapshot{}, ErrBudgetExceeded
	}
	identity, err := identityBytes(s)
	if err != nil {
		return Snapshot{}, ErrInvalidRequest
	}
	h := sha256.Sum256(identity)
	computed := hex.EncodeToString(h[:])
	if s.StateID != "" && s.StateID != computed {
		return Snapshot{}, ErrInvalidRequest
	}
	s.StateID = computed
	return s, nil
}

// Validate checks the normalized public record without mutating the caller.
func Validate(s Snapshot) error {
	_, err := Normalize(s)
	return err
}

// CanonicalBytes serializes the semantic identity projection. Temporal,
// historical, reset, and action-sequence metadata is intentionally excluded.
func CanonicalBytes(s Snapshot) ([]byte, error) {
	n, err := Normalize(s)
	if err != nil {
		return nil, err
	}
	return identityBytes(n)
}

// StateID returns the lowercase SHA-256 commitment to normalized semantics.
func StateID(s Snapshot) (string, error) {
	n, err := Normalize(s)
	if err != nil {
		return "", err
	}
	return n.StateID, nil
}

func validateSnapshot(s Snapshot) error {
	if s.SchemaVersion != SchemaVersion || s.Kind != "snapshot" {
		return ErrInvalidRequest
	}
	if s.StateID != "" && !validStateID(s.StateID) {
		return ErrInvalidRequest
	}
	if s.ResetReason != "" && s.ResetReason != "missing_baseline" && s.ResetReason != "expired_baseline" && s.ResetReason != "incompatible_baseline" && s.ResetReason != "partial_coverage" && s.ResetReason != "identity_uncertain" && s.ResetReason != "delta_not_smaller" {
		return ErrInvalidRequest
	}
	if !opaque(s.ScopeID) || !opaque(s.WindowRef) || !boundedTime(s.ObservedAt) || s.ActionSequence > MaxJSONInteger {
		return ErrInvalidRequest
	}
	if s.Coverage.Status != "complete" && s.Coverage.Status != "partial" && s.Coverage.Status != "unavailable" {
		return ErrInvalidRequest
	}
	if len(s.Coverage.Limitations) > 32 {
		return ErrBudgetExceeded
	}
	for _, code := range s.Coverage.Limitations {
		if !safeCode(code) {
			return ErrInvalidRequest
		}
	}
	if len(s.Nodes) > MaxNodes {
		return ErrBudgetExceeded
	}
	if len(s.Nodes) == 0 || s.Nodes == nil {
		return ErrInvalidRequest
	}
	for ref, n := range s.Nodes {
		if !opaque(ref) || !utf8.ValidString(n.Role) || len(n.Role) == 0 {
			return ErrInvalidRequest
		}
		if len(n.Role) > 128 {
			return ErrBudgetExceeded
		}
		if n.Classification != "normal" && n.Classification != "secure" && n.Classification != "unknown" {
			return ErrInvalidRequest
		}
		if n.Classification != "normal" && (n.Label != nil || n.Value != nil || n.Enabled != nil || n.Checked != nil || n.Selected != nil || len(n.Actions) != 0) {
			return ErrInvalidRequest
		}
		if n.Label != nil {
			if !utf8.ValidString(*n.Label) {
				return ErrInvalidRequest
			}
			if len(*n.Label) > 4096 {
				return ErrBudgetExceeded
			}
		}
		if n.Value != nil {
			if !utf8.ValidString(*n.Value) {
				return ErrInvalidRequest
			}
			if len(*n.Value) > 8192 {
				return ErrBudgetExceeded
			}
		}
		if n.ParentRef != nil && !opaque(*n.ParentRef) {
			return ErrInvalidRequest
		}
		if len(n.ChildRefs) > MaxNodes || len(n.Actions) > 16 {
			return ErrBudgetExceeded
		}
		if !uniqueStrings(n.ChildRefs) || !uniqueStrings(n.Actions) {
			return ErrInvalidRequest
		}
		for _, c := range n.ChildRefs {
			if !opaque(c) {
				return ErrInvalidRequest
			}
		}
		for _, a := range n.Actions {
			if !validAction(a) {
				return ErrInvalidRequest
			}
		}
	}
	if len(s.Context.RootRefs) > MaxNodes || !uniqueStrings(s.Context.RootRefs) {
		return ErrInvalidRequest
	}
	for _, r := range s.Context.RootRefs {
		if !opaque(r) {
			return ErrInvalidRequest
		}
	}
	if s.Context.FocusedElementRef != nil && !opaque(*s.Context.FocusedElementRef) {
		return ErrInvalidRequest
	}
	if err := validateTopology(s); err != nil {
		return err
	}
	return nil
}

func validateTopology(s Snapshot) error {
	roots := make([]string, 0)
	for ref, n := range s.Nodes {
		if n.ParentRef == nil {
			roots = append(roots, ref)
		} else {
			p, ok := s.Nodes[*n.ParentRef]
			if !ok || !contains(p.ChildRefs, ref) {
				return ErrInvalidRequest
			}
		}
		for _, child := range n.ChildRefs {
			c, ok := s.Nodes[child]
			if !ok || c.ParentRef == nil || *c.ParentRef != ref {
				return ErrInvalidRequest
			}
		}
	}
	sort.Strings(roots)
	want := append([]string(nil), s.Context.RootRefs...)
	sort.Strings(want)
	if !equalStrings(roots, want) || (s.Context.FocusedElementRef != nil && s.Nodes[*s.Context.FocusedElementRef].Role == "") {
		return ErrInvalidRequest
	}
	color := make(map[string]uint8, len(s.Nodes))
	var visit func(string, int) error
	visit = func(ref string, depth int) error {
		if depth > MaxDepth {
			return ErrBudgetExceeded
		}
		if color[ref] == 1 {
			return ErrInvalidRequest
		}
		if color[ref] == 2 {
			return ErrInvalidRequest
		}
		color[ref] = 1
		for _, child := range s.Nodes[ref].ChildRefs {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		color[ref] = 2
		return nil
	}
	for _, root := range roots {
		if err := visit(root, 1); err != nil {
			return err
		}
	}
	if len(color) != len(s.Nodes) {
		return ErrInvalidRequest
	}
	return nil
}

func identityBytes(s Snapshot) ([]byte, error) {
	coverage := map[string]any{"status": s.Coverage.Status, "truncated": s.Coverage.Truncated, "limitations": stringSlice(s.Coverage.Limitations)}
	context := map[string]any{"root_refs": stringSlice(s.Context.RootRefs), "focused_element_ref": pointerString(s.Context.FocusedElementRef)}
	nodes := make(map[string]any, len(s.Nodes))
	for ref, n := range s.Nodes {
		nodes[ref] = nodeObject(n)
	}
	return marshalCanonical(map[string]any{"schema_version": s.SchemaVersion, "scope_id": s.ScopeID, "window_ref": s.WindowRef, "coverage": coverage, "context": context, "nodes": nodes})
}

func canonicalSnapshot(s Snapshot) ([]byte, error) {
	coverage := map[string]any{"status": s.Coverage.Status, "truncated": s.Coverage.Truncated, "limitations": stringSlice(s.Coverage.Limitations)}
	context := map[string]any{"root_refs": stringSlice(s.Context.RootRefs), "focused_element_ref": pointerString(s.Context.FocusedElementRef)}
	nodes := make(map[string]any, len(s.Nodes))
	for ref, n := range s.Nodes {
		nodes[ref] = nodeObject(n)
	}
	return marshalCanonical(map[string]any{"schema_version": s.SchemaVersion, "state_id": s.StateID, "scope_id": s.ScopeID, "window_ref": s.WindowRef, "observed_at": s.ObservedAt, "action_sequence": s.ActionSequence, "coverage": coverage, "context": context, "kind": s.Kind, "historical": s.Historical, "reset_reason": s.ResetReason, "nodes": nodes})
}

func nodeObject(n Node) map[string]any {
	m := map[string]any{"role": n.Role, "classification": n.Classification, "parent_ref": pointerString(n.ParentRef), "child_refs": stringSlice(n.ChildRefs), "actions": stringSlice(n.Actions)}
	if n.Label != nil {
		m["label"] = *n.Label
	}
	if n.Value != nil {
		m["value"] = *n.Value
	}
	if n.Enabled != nil {
		m["enabled"] = *n.Enabled
	}
	if n.Checked != nil {
		m["checked"] = *n.Checked
	}
	if n.Selected != nil {
		m["selected"] = *n.Selected
	}
	return m
}

func marshalCanonical(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}

func opaque(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '!' || s[i] > '~' {
			return false
		}
	}
	return true
}
func safeCode(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func validStateID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
func boundedTime(t time.Time) bool {
	if t.IsZero() {
		return false
	}
	b, err := t.MarshalJSON()
	return err == nil && len(b) <= 42 && strings.Contains(string(b), "T")
}
func validAction(a string) bool {
	switch a {
	case "press", "pick", "focus", "replace", "insert", "scroll", "click":
		return true
	}
	return false
}
func compactStrings(a []string) []string {
	if len(a) == 0 {
		return []string{}
	}
	out := a[:1]
	for _, x := range a[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}
func uniqueStrings(a []string) bool {
	seen := make(map[string]struct{}, len(a))
	for _, x := range a {
		if _, ok := seen[x]; ok {
			return false
		}
		seen[x] = struct{}{}
	}
	return true
}
func contains(a []string, x string) bool {
	for _, v := range a {
		if v == x {
			return true
		}
	}
	return false
}
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func stringSlice(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}
func pointerString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
