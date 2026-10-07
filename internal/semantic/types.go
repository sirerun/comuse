// Package semantic owns the pure, bounded redacted state contract. It does not
// perform native reads, resolve authority, or admit input.
package semantic

import "time"

const (
	SchemaVersion  = 1
	MaxNodes       = 10000
	MaxDepth       = 128
	MaxStateBytes  = 4 * 1024 * 1024
	MaxGenerations = 8
	StateTTL       = 2 * time.Minute
	MaxJSONInteger = uint64(9007199254740991)
)

type Coverage struct {
	Status      string   `json:"status"`
	Truncated   bool     `json:"truncated"`
	Limitations []string `json:"limitations"`
}
type Context struct {
	RootRefs          []string `json:"root_refs"`
	FocusedElementRef *string  `json:"focused_element_ref"`
}
type Node struct {
	Role           string   `json:"role"`
	Classification string   `json:"classification"`
	Label          *string  `json:"label,omitempty"`
	Value          *string  `json:"value,omitempty"`
	ParentRef      *string  `json:"parent_ref"`
	ChildRefs      []string `json:"child_refs"`
	Enabled        *bool    `json:"enabled,omitempty"`
	Checked        *bool    `json:"checked,omitempty"`
	Selected       *bool    `json:"selected,omitempty"`
	Actions        []string `json:"actions"`
}
type Metadata struct {
	SchemaVersion  int       `json:"schema_version"`
	StateID        string    `json:"state_id"`
	ScopeID        string    `json:"scope_id"`
	WindowRef      string    `json:"window_ref"`
	ObservedAt     time.Time `json:"observed_at"`
	ActionSequence uint64    `json:"action_sequence"`
	Coverage       Coverage  `json:"coverage"`
	Context        Context   `json:"context"`
}
type Snapshot struct {
	Metadata
	Kind        string          `json:"kind"`
	Nodes       map[string]Node `json:"nodes"`
	Historical  bool            `json:"historical"`
	ResetReason string          `json:"reset_reason,omitempty"`
}
type Delta struct {
	Metadata
	Kind        string          `json:"kind"`
	BaseStateID string          `json:"base_state_id"`
	Upsert      map[string]Node `json:"upsert"`
	Removed     []string        `json:"removed"`
}
type Unchanged struct {
	Metadata
	Kind        string `json:"kind"`
	BaseStateID string `json:"base_state_id"`
}

// BindingCharge is a constant retention charge per opaque native reference.
// Actual binding contents never affect public retention accounting.
const BindingCharge = 256

// CloneSnapshot separates every mutable record and metadata container.
func CloneSnapshot(s Snapshot) Snapshot {
	s.Coverage.Limitations = append([]string{}, s.Coverage.Limitations...)
	s.Context.RootRefs = append([]string{}, s.Context.RootRefs...)
	s.Context.FocusedElementRef = copyPointer(s.Context.FocusedElementRef)
	nodes := make(map[string]Node, len(s.Nodes))
	for ref, n := range s.Nodes {
		n.Label = copyPointer(n.Label)
		n.Value = copyPointer(n.Value)
		n.ParentRef = copyPointer(n.ParentRef)
		n.Enabled = copyPointer(n.Enabled)
		n.Checked = copyPointer(n.Checked)
		n.Selected = copyPointer(n.Selected)
		n.ChildRefs = append([]string{}, n.ChildRefs...)
		n.Actions = append([]string{}, n.Actions...)
		nodes[ref] = n
	}
	s.Nodes = nodes
	return s
}
func copyPointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
