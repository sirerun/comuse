package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	errHistoryInvalid = errors.New("invalid_request")
	errHistoryExpired = errors.New("state_expired")
	// ErrScopeRotationRequired prevents expired hashes from acquiring new metadata.
	// The trusted owner must rotate ScopeID before purging and republishing.
	ErrScopeRotationRequired = errors.New("state_expired")
)

const maxHistoryTombstones = 128
const historyTombstoneCharge uint64 = 64

type historyKey struct {
	scope  string
	window string
	state  string
}

type historyEntry struct {
	snapshot Snapshot
	admitted time.Time
	bytes    uint64
	order    uint64
}

// History retains immutable, normalized snapshots for one session. The owner
// must purge it whenever session authority or policy is invalidated.
type History struct {
	mu               sync.Mutex
	normalize        func(Snapshot) (Snapshot, error)
	entries          map[historyKey]historyEntry
	retired          map[[32]byte]struct{}
	rotationRequired bool
	bytes            uint64
	lastNow          time.Time
	order            uint64
}

func NewHistory(normalize func(Snapshot) (Snapshot, error)) (*History, error) {
	if normalize == nil {
		return nil, errHistoryInvalid
	}
	return &History{normalize: normalize, entries: make(map[historyKey]historyEntry), retired: make(map[[32]byte]struct{})}, nil
}

// Publish normalizes and validates a fresh snapshot before atomically admitting
// it. Repeated state IDs preserve the first stored snapshot and admission time.
func (h *History) Publish(snapshot Snapshot, now time.Time) (Snapshot, error) {
	if h == nil {
		return Snapshot{}, errHistoryInvalid
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.entries == nil || h.normalize == nil {
		return Snapshot{}, errHistoryInvalid
	}
	current, err := callNormalizer(h.normalize, CloneSnapshot(snapshot))
	if err != nil || validateHistorySnapshot(current) != nil {
		return Snapshot{}, errHistoryInvalid
	}
	current = CloneSnapshot(current)
	current.Historical = false
	charge, ok := retainedCharge(current)
	if !ok || charge > MaxStateBytes {
		return Snapshot{}, errHistoryInvalid
	}
	key := historyKey{scope: current.ScopeID, window: current.WindowRef, state: current.StateID}
	now = h.monotonicNow(now)
	h.expire(now)
	if _, exists := h.entries[key]; exists {
		// Preserve original stored metadata and its original TTL. Caller metadata
		// remains fresh and independent from the stored defensive copy.
		return CloneSnapshot(current), nil
	}
	if h.rotationRequired {
		return Snapshot{}, ErrScopeRotationRequired
	}
	if _, retired := h.retired[historyKeyDigest(key)]; retired {
		return Snapshot{}, ErrScopeRotationRequired
	}
	if !h.evictFor(charge) {
		return Snapshot{}, ErrScopeRotationRequired
	}
	h.order++
	h.entries[key] = historyEntry{snapshot: CloneSnapshot(current), admitted: now, bytes: charge, order: h.order}
	h.bytes += charge
	return CloneSnapshot(current), nil
}

// Lookup returns only an exact scoped historical state. Missing, foreign, and
// expired states share one safe error and never disclose retained data.
func (h *History) Lookup(scopeID, windowRef, stateID string, now time.Time) (Snapshot, error) {
	if h == nil || !validOpaque(scopeID) || !validOpaque(windowRef) || !validHistoryStateID(stateID) {
		return Snapshot{}, errHistoryExpired
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.entries == nil {
		return Snapshot{}, errHistoryExpired
	}
	now = h.monotonicNow(now)
	h.expire(now)
	entry, ok := h.entries[historyKey{scope: scopeID, window: windowRef, state: stateID}]
	if !ok {
		return Snapshot{}, errHistoryExpired
	}
	out := CloneSnapshot(entry.snapshot)
	out.Historical = true
	return out, nil
}

// Purge invalidates every retained state. Coordinator-owned authority changes
// must rotate the trusted scope generation before calling this method;
// this type does not infer revocation itself or permit reuse of an old scope.
func (h *History) Purge() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	clear(h.entries)
	clear(h.retired)
	h.rotationRequired = false
	h.bytes = 0
}

func (h *History) RetainedBytes() uint64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bytes
}

func callNormalizer(normalize func(Snapshot) (Snapshot, error), in Snapshot) (out Snapshot, err error) {
	defer func() {
		if recover() != nil {
			out = Snapshot{}
			err = errHistoryInvalid
		}
	}()
	out, err = normalize(in)
	if err != nil {
		return Snapshot{}, errHistoryInvalid
	}
	return out, nil
}

func (h *History) monotonicNow(now time.Time) time.Time {
	if now.Before(h.lastNow) {
		now = h.lastNow
	} else {
		h.lastNow = now
	}
	return now
}

func (h *History) expire(now time.Time) {
	for key, entry := range h.entries {
		if !now.Before(entry.admitted.Add(StateTTL)) {
			delete(h.entries, key)
			h.bytes -= entry.bytes
			h.retire(key)
		}
	}
}

func (h *History) evictFor(incoming uint64) bool {
	for len(h.entries) >= MaxGenerations || h.bytes > uint64(MaxStateBytes)-incoming {
		var oldestKey historyKey
		var oldest historyEntry
		first := true
		for key, entry := range h.entries {
			if first || entry.order < oldest.order {
				oldestKey, oldest, first = key, entry, false
			}
		}
		if first {
			return false
		}
		delete(h.entries, oldestKey)
		h.bytes -= oldest.bytes
		h.retire(oldestKey)
		if h.rotationRequired {
			return false
		}
	}
	return true
}

func historyKeyDigest(key historyKey) [32]byte {
	return sha256.Sum256([]byte(key.scope + "\x00" + key.window + "\x00" + key.state))
}

func (h *History) retire(key historyKey) {
	digest := historyKeyDigest(key)
	if _, ok := h.retired[digest]; ok {
		return
	}
	if len(h.retired) >= maxHistoryTombstones || h.bytes > uint64(MaxStateBytes)-historyTombstoneCharge {
		h.rotationRequired = true
		return
	}
	h.retired[digest] = struct{}{}
	h.bytes += historyTombstoneCharge
}

func validateHistorySnapshot(s Snapshot) error {
	if s.SchemaVersion != SchemaVersion || !validHistoryStateID(s.StateID) || !validOpaque(s.ScopeID) || !validOpaque(s.WindowRef) || s.Kind != "snapshot" || s.ActionSequence > MaxJSONInteger {
		return errHistoryInvalid
	}
	if s.Historical || s.ObservedAt.IsZero() || len(s.ObservedAt.Format(time.RFC3339Nano)) > 40 {
		return errHistoryInvalid
	}
	if s.Coverage.Status != "complete" && s.Coverage.Status != "partial" && s.Coverage.Status != "unavailable" {
		return errHistoryInvalid
	}
	if len(s.Coverage.Limitations) > 32 {
		return errHistoryInvalid
	}
	for _, limitation := range s.Coverage.Limitations {
		if !validCode(limitation) {
			return errHistoryInvalid
		}
	}
	if len(s.Nodes) > MaxNodes {
		return errHistoryInvalid
	}
	if s.Context.FocusedElementRef != nil && !validOpaque(*s.Context.FocusedElementRef) {
		return errHistoryInvalid
	}
	rootSet := make(map[string]bool, len(s.Context.RootRefs))
	for _, ref := range s.Context.RootRefs {
		if !validOpaque(ref) || rootSet[ref] {
			return errHistoryInvalid
		}
		rootSet[ref] = true
	}
	if s.Context.FocusedElementRef != nil {
		if _, ok := s.Nodes[*s.Context.FocusedElementRef]; !ok {
			return errHistoryInvalid
		}
	}
	for ref, node := range s.Nodes {
		if !validOpaque(ref) || len(node.Role) == 0 || len(node.Role) > 128 || (node.Classification != "normal" && node.Classification != "secure" && node.Classification != "unknown") {
			return errHistoryInvalid
		}
		if node.Label != nil && len(*node.Label) > MaxStateBytes || node.Value != nil && len(*node.Value) > MaxStateBytes {
			return errHistoryInvalid
		}
		if node.Classification != "normal" && node.Value != nil {
			return errHistoryInvalid
		}
		if node.ParentRef != nil && !validOpaque(*node.ParentRef) {
			return errHistoryInvalid
		}
		childSet := make(map[string]bool, len(node.ChildRefs))
		for _, child := range node.ChildRefs {
			if !validOpaque(child) || childSet[child] {
				return errHistoryInvalid
			}
			childSet[child] = true
			childNode, ok := s.Nodes[child]
			if !ok || childNode.ParentRef == nil || *childNode.ParentRef != ref {
				return errHistoryInvalid
			}
		}
		if node.ParentRef == nil {
			if !rootSet[ref] {
				return errHistoryInvalid
			}
		} else {
			parent, ok := s.Nodes[*node.ParentRef]
			if !ok || !historyContains(parent.ChildRefs, ref) {
				return errHistoryInvalid
			}
		}
	}
	for ref := range rootSet {
		node, ok := s.Nodes[ref]
		if !ok || node.ParentRef != nil {
			return errHistoryInvalid
		}
	}
	if len(rootSet) > len(s.Nodes) {
		return errHistoryInvalid
	}
	// Parent-chain traversal is iterative and bounded; malformed cycles never
	// reach recursive serializers or consume unbounded stack space.
	for ref := range s.Nodes {
		seen := make(map[string]bool, MaxDepth+1)
		current := ref
		for depth := 0; ; depth++ {
			if depth > MaxDepth || seen[current] {
				return errHistoryInvalid
			}
			seen[current] = true
			node := s.Nodes[current]
			if node.ParentRef == nil {
				break
			}
			current = *node.ParentRef
		}
	}
	return nil
}

func retainedCharge(s Snapshot) (uint64, bool) {
	// Charge the immutable public payload exactly; private reference metadata
	// has a fixed charge and never includes native IDs or protected content.
	encoded, err := canonicalSnapshot(CloneSnapshot(s))
	if err != nil {
		return 0, false
	}
	refs := uint64(len(s.Nodes) + 2)
	if refs > (^uint64(0)-uint64(len(encoded)))/BindingCharge {
		return 0, false
	}
	return uint64(len(encoded)) + refs*BindingCharge, true
}

func validOpaque(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func validHistoryStateID(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	var decoded [32]byte
	_, err := hex.Decode(decoded[:], []byte(value))
	return err == nil
}

func validCode(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func historyContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stringPointer(value string) *string { return &value }
