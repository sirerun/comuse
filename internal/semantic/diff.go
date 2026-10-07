package semantic

import "reflect"

// Diff computes complete-record upserts and unambiguous removals between
// compatible complete snapshots. No partial traversal can infer deletion.
func Diff(base, next Snapshot) (Delta, error) {
	b, err := Normalize(base)
	if err != nil {
		return Delta{}, err
	}
	n, err := Normalize(next)
	if err != nil {
		return Delta{}, err
	}
	if !compatible(b.Metadata, n.Metadata) {
		return Delta{}, ErrPolicyRefused
	}
	if !complete(b) || !complete(n) || b.Historical || n.Historical {
		return Delta{}, ErrUnsupported
	}
	if n.ActionSequence < b.ActionSequence {
		return Delta{}, ErrInvalidRequest
	}
	d := Delta{
		Metadata:    n.Metadata,
		Kind:        "delta",
		BaseStateID: b.StateID,
		Upsert:      make(map[string]Node),
		Removed:     []string{},
	}
	for ref, node := range n.Nodes {
		old, ok := b.Nodes[ref]
		if !ok || !reflect.DeepEqual(old, node) {
			d.Upsert[ref] = cloneNode(node)
		}
	}
	for ref := range b.Nodes {
		if _, ok := n.Nodes[ref]; !ok {
			d.Removed = append(d.Removed, ref)
		}
	}
	sortStrings(d.Removed)
	return d, nil
}

// Apply verifies the exact baseline, applies full-record changes to a private
// copy, then validates identity and topology before returning the new state.
func Apply(base Snapshot, delta Delta) (Snapshot, error) {
	b, err := Normalize(base)
	if err != nil {
		return Snapshot{}, err
	}
	if delta.Kind != "delta" {
		return Snapshot{}, ErrInvalidRequest
	}
	if !compatible(b.Metadata, delta.Metadata) {
		return Snapshot{}, ErrPolicyRefused
	}
	if delta.BaseStateID == "" || delta.BaseStateID != b.StateID {
		return Snapshot{}, ErrStateExpired
	}
	if !complete(b) || b.Historical || delta.Coverage.Status != "complete" || delta.Coverage.Truncated {
		return Snapshot{}, ErrUnsupported
	}
	if delta.ActionSequence < b.ActionSequence {
		return Snapshot{}, ErrInvalidRequest
	}
	if len(delta.Upsert) > MaxNodes || len(delta.Removed) > MaxNodes {
		return Snapshot{}, ErrBudgetExceeded
	}
	if delta.Upsert == nil || delta.Removed == nil || !uniqueStrings(delta.Removed) {
		return Snapshot{}, ErrInvalidRequest
	}
	for ref := range delta.Upsert {
		if !opaque(ref) {
			return Snapshot{}, ErrInvalidRequest
		}
		if contains(delta.Removed, ref) {
			return Snapshot{}, ErrInvalidRequest
		}
	}
	for _, ref := range delta.Removed {
		if !opaque(ref) {
			return Snapshot{}, ErrInvalidRequest
		}
		if _, ok := b.Nodes[ref]; !ok {
			return Snapshot{}, ErrInvalidRequest
		}
	}
	result := Snapshot{Metadata: delta.Metadata, Kind: "snapshot", Nodes: make(map[string]Node, len(b.Nodes)+len(delta.Upsert))}
	for ref, node := range b.Nodes {
		result.Nodes[ref] = cloneNode(node)
	}
	for _, ref := range delta.Removed {
		delete(result.Nodes, ref)
	}
	for ref, node := range delta.Upsert {
		result.Nodes[ref] = cloneNode(node)
	}
	result, err = Normalize(result)
	if err != nil {
		return Snapshot{}, err
	}
	if result.StateID != delta.StateID {
		return Snapshot{}, ErrInvalidRequest
	}
	return result, nil
}

func complete(s Snapshot) bool { return s.Coverage.Status == "complete" && !s.Coverage.Truncated }
func compatible(a, b Metadata) bool {
	return a.SchemaVersion == b.SchemaVersion && a.ScopeID == b.ScopeID && a.WindowRef == b.WindowRef
}
func cloneNode(n Node) Node {
	n.Label = copyPointer(n.Label)
	n.Value = copyPointer(n.Value)
	n.ParentRef = copyPointer(n.ParentRef)
	n.Enabled = copyPointer(n.Enabled)
	n.Checked = copyPointer(n.Checked)
	n.Selected = copyPointer(n.Selected)
	n.ChildRefs = append([]string{}, n.ChildRefs...)
	n.Actions = append([]string{}, n.Actions...)
	return n
}
func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
