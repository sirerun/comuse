// Package semanticprobe normalizes bounded native AX result envelopes into a
// semantic-only fixture projection. It performs no native I/O or mutation.
package semanticprobe

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	SchemaVersion    = 1
	FixtureBundleID  = "com.sirerun.comuse.fixture"
	MaxEnvelopeBytes = 64 * 1024
	MaxElements      = 256
	MaxDepth         = 16
	MaxTextBytes     = 16 * 1024
	MaxDeadlineMS    = 250
)

var (
	ErrMalformedEnvelope = errors.New("native envelope is malformed")
	ErrResponseLimit     = errors.New("native envelope exceeds byte limit")
	ErrRequestMismatch   = errors.New("native request identity mismatch")
	ErrScopeMismatch     = errors.New("native result does not match trusted fixture scope")
	ErrExpiredReference  = errors.New("native reference expired")
	ErrStaleReference    = errors.New("native reference is stale")
	ErrNativeOperation   = errors.New("native operation failed")
	ErrTopology          = errors.New("native element topology is invalid")
	ErrCoverage          = errors.New("native coverage report is invalid")
	ErrLimitExceeded     = errors.New("native result exceeds semantic limits")
	ErrValueStatus       = errors.New("native value status is invalid")
)

type ExpectedScope struct {
	RequestID               string
	PID                     int32
	BundleID                string
	FixtureNonce            string
	ProcessLaunchGeneration string
	ProcessStartRef         string
	WindowRef               string
}

type Options struct {
	IncludeSyntheticNormalValue bool
}

type SnapshotStatus uint8

const (
	SnapshotComplete SnapshotStatus = iota + 1
	SnapshotPartial
)

func (status SnapshotStatus) String() string {
	switch status {
	case SnapshotComplete:
		return "complete"
	case SnapshotPartial:
		return "partial"
	default:
		return "unknown"
	}
}

// MarshalJSON keeps the public snapshot wire status stable and readable.
func (status SnapshotStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(status.String())
}

type Element struct {
	Ref         string   `json:"ref"`
	Role        string   `json:"role"`
	Identifier  string   `json:"identifier,omitempty"`
	ValueStatus string   `json:"value_status"`
	Value       *string  `json:"value,omitempty"`
	ParentRef   *string  `json:"parent_ref"`
	ChildRefs   []string `json:"child_refs"`
}

type Coverage struct {
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	DepthLimit    int    `json:"depth_limit"`
	NodeLimit     int    `json:"node_limit"`
	TextByteLimit int    `json:"text_byte_limit"`
	DeadlineMS    int    `json:"deadline_ms"`
	Visited       int    `json:"visited"`
	TextBytes     int    `json:"text_bytes"` // projected bytes after redaction
	Truncated     bool   `json:"truncated"`
}

type Snapshot struct {
	RequestID        string         `json:"request_id"`
	ObservationID    string         `json:"observation_id"`
	Status           SnapshotStatus `json:"status"`
	BundleID         string         `json:"bundle_id"`
	FixtureNonce     string         `json:"fixture_nonce"`
	ProcessStartRef  string         `json:"process_start_ref"`
	WindowRef        string         `json:"window_ref"`
	RootRefs         []string       `json:"root_refs"`
	Elements         []Element      `json:"elements"`
	Coverage         Coverage       `json:"coverage"`
	NativeStateID    string         `json:"-"` // exact native precondition; never serialize as ordinary JSON
	CanonicalStateID string         `json:"canonical_state_id,omitempty"`
	CanonicalJSON    []byte         `json:"-"`
}

type envelope struct {
	SchemaVersion int             `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Status        string          `json:"status"`
	Result        json.RawMessage `json:"result"`
	Error         string          `json:"error,omitempty"`
}

type nativeResult struct {
	ObservationID   string          `json:"observation_id"`
	StateID         string          `json:"state_id"`
	ProcessStartRef string          `json:"process_start_ref"`
	WindowRef       string          `json:"window_ref"`
	RootRefs        []string        `json:"root_refs"`
	Elements        []nativeElement `json:"elements"`
	Coverage        nativeCoverage  `json:"coverage"`
}

type nativeElement struct {
	Ref         string          `json:"ref"`
	Role        string          `json:"role"`
	ValueStatus string          `json:"value_status"`
	ChildRefs   []string        `json:"child_refs"`
	ParentRef   json.RawMessage `json:"parent_ref"`
	Identifier  *string         `json:"identifier,omitempty"`
	Value       *string         `json:"value,omitempty"`
}

type nativeCoverage struct {
	Status        string          `json:"status"`
	Reason        json.RawMessage `json:"reason"`
	DepthLimit    int             `json:"depth_limit"`
	NodeLimit     int             `json:"node_limit"`
	TextByteLimit int             `json:"text_byte_limit"`
	DeadlineMS    int             `json:"deadline_ms"`
	Visited       int             `json:"visited"`
	TextBytes     int             `json:"text_bytes"`
	Truncated     bool            `json:"truncated"`
}

type canonicalDocument struct {
	SchemaVersion int             `json:"schema_version"`
	BundleID      string          `json:"bundle_id"`
	FixtureNonce  string          `json:"fixture_nonce"`
	RootIndexes   []int           `json:"root_indexes"`
	Elements      []canonicalNode `json:"elements"`
	Coverage      Coverage        `json:"coverage"`
}

type canonicalNode struct {
	Role         string  `json:"role"`
	Identifier   string  `json:"identifier,omitempty"`
	ValueStatus  string  `json:"value_status"`
	Value        *string `json:"value,omitempty"`
	ParentIndex  *int    `json:"parent_index"`
	ChildIndexes []int   `json:"child_indexes"`
}

// NormalizeEnvelope checks a native v1 response against trusted caller scope,
// validates topology and coverage, then redacts before constructing canonical
// bytes or a semantic hash. PID, bundle ID, nonce, and launch generation are
// supplied by trusted host code; the native result itself only confirms the
// opaque process_start_ref and window_ref fields present in its envelope.
func NormalizeEnvelope(raw []byte, expected ExpectedScope, options Options) (Snapshot, error) {
	if len(raw) > MaxEnvelopeBytes {
		return Snapshot{}, ErrResponseLimit
	}
	if err := validateExpectedScope(expected); err != nil {
		return Snapshot{}, err
	}
	var response envelope
	if err := decodeStrict(raw, &response); err != nil {
		return Snapshot{}, ErrMalformedEnvelope
	}
	if response.SchemaVersion != SchemaVersion {
		return Snapshot{}, ErrMalformedEnvelope
	}
	if response.RequestID != expected.RequestID {
		return Snapshot{}, ErrRequestMismatch
	}
	switch response.Status {
	case "error":
		if len(response.Result) != 0 || response.Error == "" {
			return Snapshot{}, ErrMalformedEnvelope
		}
		switch response.Error {
		case "reference_expired":
			return Snapshot{}, ErrExpiredReference
		case "reference_stale":
			return Snapshot{}, ErrStaleReference
		default:
			return Snapshot{}, ErrNativeOperation
		}
	case "completed", "partial":
		if len(response.Result) == 0 || response.Error != "" {
			return Snapshot{}, ErrMalformedEnvelope
		}
	default:
		return Snapshot{}, ErrMalformedEnvelope
	}

	var result nativeResult
	if err := decodeStrict(response.Result, &result); err != nil {
		return Snapshot{}, ErrMalformedEnvelope
	}
	if result.ProcessStartRef != expected.ProcessStartRef || result.WindowRef != expected.WindowRef {
		return Snapshot{}, ErrScopeMismatch
	}
	if result.ObservationID == "" || len(result.ObservationID) > 128 || !isSHA256Hex(result.StateID) {
		return Snapshot{}, ErrMalformedEnvelope
	}
	coverage, err := normalizeCoverage(result.Coverage)
	if err != nil {
		return Snapshot{}, err
	}
	if len(result.Elements) == 0 || len(result.Elements) > MaxElements ||
		len(result.RootRefs) == 0 || len(result.RootRefs) > MaxElements ||
		coverage.Visited < len(result.Elements) || coverage.Visited > MaxElements {
		return Snapshot{}, ErrLimitExceeded
	}

	nodes := make(map[string]Element, len(result.Elements))
	nativeIDEligible := true
	projectionIncomplete := false
	projectedTextBytes := 0
	for _, rawElement := range result.Elements {
		element, valueBytes, eligible, err := projectElement(rawElement, options)
		if err != nil {
			return Snapshot{}, err
		}
		if _, exists := nodes[element.Ref]; exists {
			return Snapshot{}, ErrTopology
		}
		nodes[element.Ref] = element
		projectedTextBytes += valueBytes
		nativeIDEligible = nativeIDEligible && eligible
		projectionIncomplete = projectionIncomplete || rawElement.ValueStatus == "omitted_limit" || rawElement.Role == "unknown"
	}
	if projectedTextBytes > MaxTextBytes {
		return Snapshot{}, ErrLimitExceeded
	}
	if result.Coverage.TextBytes < 0 || result.Coverage.TextBytes > MaxTextBytes {
		return Snapshot{}, ErrCoverage
	}
	coverage.TextBytes = projectedTextBytes
	if projectionIncomplete {
		coverage.Status = "truncated"
		coverage.Truncated = true
		if coverage.Reason == "" {
			coverage.Reason = "bounded_or_ax_uncertainty"
		}
	}

	ordered, rootIndexes, err := validateAndOrder(nodes, result.RootRefs)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateSourceCoverage(response.Status, result.Coverage, len(result.Elements)); err != nil {
		return Snapshot{}, err
	}
	complete := response.Status == "completed" && result.Coverage.Status == "complete" && !projectionIncomplete &&
		!result.Coverage.Truncated && len(result.Elements) == result.Coverage.Visited &&
		coverage.Reason == ""
	status := SnapshotPartial
	if complete {
		status = SnapshotComplete
	}

	snapshot := Snapshot{
		RequestID: response.RequestID, ObservationID: result.ObservationID,
		Status: status, BundleID: expected.BundleID, FixtureNonce: expected.FixtureNonce,
		ProcessStartRef: expected.ProcessStartRef, WindowRef: expected.WindowRef,
		RootRefs: append([]string(nil), result.RootRefs...), Elements: ordered, Coverage: coverage,
	}
	canonical, err := canonicalize(snapshot, rootIndexes)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.CanonicalJSON = canonical
	if status == SnapshotComplete {
		digest := sha256.Sum256(canonical)
		snapshot.CanonicalStateID = hex.EncodeToString(digest[:])
		if nativeIDEligible {
			snapshot.NativeStateID = result.StateID
		}
	}
	return snapshot, nil
}

func validateExpectedScope(scope ExpectedScope) error {
	if scope.RequestID == "" || len(scope.RequestID) > 128 || scope.PID <= 0 ||
		scope.BundleID != FixtureBundleID || !validNonce(scope.FixtureNonce) ||
		scope.ProcessLaunchGeneration == "" || len(scope.ProcessLaunchGeneration) > 128 ||
		scope.ProcessStartRef == "" || len(scope.ProcessStartRef) > 128 ||
		scope.WindowRef == "" || len(scope.WindowRef) > 128 {
		return ErrScopeMismatch
	}
	return nil
}

func normalizeCoverage(raw nativeCoverage) (Coverage, error) {
	if raw.DepthLimit != MaxDepth || raw.NodeLimit != MaxElements ||
		raw.TextByteLimit != MaxTextBytes || raw.DeadlineMS != MaxDeadlineMS ||
		raw.Visited < 0 || raw.TextBytes < 0 {
		return Coverage{}, ErrCoverage
	}
	if raw.Status != "complete" && raw.Status != "truncated" {
		return Coverage{}, ErrCoverage
	}
	reason := ""
	if len(raw.Reason) != 0 && !bytes.Equal(raw.Reason, []byte("null")) {
		if err := json.Unmarshal(raw.Reason, &reason); err != nil {
			return Coverage{}, ErrCoverage
		}
		if reason != "concurrent_change" && reason != "bounded_or_ax_uncertainty" {
			return Coverage{}, ErrCoverage
		}
	}
	if raw.Status == "complete" && (raw.Truncated || reason != "") {
		return Coverage{}, ErrCoverage
	}
	if raw.Status == "truncated" && !raw.Truncated && reason == "" {
		return Coverage{}, ErrCoverage
	}
	return Coverage{
		Status: raw.Status, Reason: reason, DepthLimit: raw.DepthLimit,
		NodeLimit: raw.NodeLimit, TextByteLimit: raw.TextByteLimit,
		DeadlineMS: raw.DeadlineMS, Visited: raw.Visited, Truncated: raw.Truncated,
	}, nil
}

func validateSourceCoverage(envelopeStatus string, coverage nativeCoverage, rowCount int) error {
	if envelopeStatus == "completed" && coverage.Status == "complete" &&
		(!coverage.Truncated && coverage.Visited != rowCount) {
		return ErrCoverage
	}
	if envelopeStatus == "partial" && coverage.Status == "complete" && !coverage.Truncated {
		return ErrCoverage
	}
	return nil
}

func projectElement(raw nativeElement, options Options) (Element, int, bool, error) {
	if raw.Ref == "" || len(raw.Ref) > 128 || raw.Role == "" || len(raw.Role) > 128 ||
		len(raw.ChildRefs) > MaxElements || len(raw.ParentRef) == 0 {
		return Element{}, 0, false, ErrTopology
	}
	if raw.Identifier != nil && len(*raw.Identifier) > 256 {
		return Element{}, 0, false, ErrLimitExceeded
	}
	switch raw.ValueStatus {
	case "omitted", "included_synthetic_normal", "included_synthetic_counter", "omitted_limit", "omitted_protected_or_unavailable":
	default:
		return Element{}, 0, false, ErrValueStatus
	}
	var parent *string
	if !bytes.Equal(raw.ParentRef, []byte("null")) {
		var value string
		if err := json.Unmarshal(raw.ParentRef, &value); err != nil || value == "" || len(value) > 128 {
			return Element{}, 0, false, ErrTopology
		}
		parent = &value
	}
	identifier := ""
	if raw.Identifier != nil {
		identifier = *raw.Identifier
	}
	projected := Element{
		Ref: raw.Ref, Role: raw.Role, Identifier: identifier,
		ValueStatus: raw.ValueStatus, ParentRef: parent,
		ChildRefs: append([]string(nil), raw.ChildRefs...),
	}
	eligible := true
	protected := identifier == "securefield" || raw.ValueStatus == "omitted_protected_or_unavailable"
	if protected {
		projected.Value = nil
		projected.ValueStatus = "omitted_protected_or_unavailable"
		if raw.Value != nil {
			eligible = false
		}
	} else if raw.Value != nil {
		allowed := false
		switch {
		case identifier == "textfield" && raw.Role == "AXTextField" && raw.ValueStatus == "included_synthetic_normal":
			allowed = true
		case identifier == "counter-value" && raw.Role == "AXStaticText" && raw.ValueStatus == "included_synthetic_counter":
			allowed = true
		}
		if allowed && options.IncludeSyntheticNormalValue {
			projected.Value = raw.Value
		} else {
			projected.ValueStatus = "omitted"
			eligible = false
		}
	} else if raw.ValueStatus == "included_synthetic_normal" || raw.ValueStatus == "included_synthetic_counter" {
		return Element{}, 0, false, ErrValueStatus
	}
	if !protected && (raw.ValueStatus == "included_synthetic_normal" || raw.ValueStatus == "included_synthetic_counter") {
		allowedStatus := (identifier == "textfield" && raw.Role == "AXTextField" && raw.ValueStatus == "included_synthetic_normal") ||
			(identifier == "counter-value" && raw.Role == "AXStaticText" && raw.ValueStatus == "included_synthetic_counter")
		if !allowedStatus {
			projected.Value = nil
			projected.ValueStatus = "omitted"
			eligible = false
		}
		if !options.IncludeSyntheticNormalValue {
			eligible = false
		}
	}
	textBytes := len(identifier)
	if projected.Value != nil {
		textBytes += len(*projected.Value)
	}
	return projected, textBytes, eligible, nil
}

func validateAndOrder(nodes map[string]Element, rootRefs []string) ([]Element, []int, error) {
	rootIndexes := make([]int, 0, len(rootRefs))
	rootSeen := make(map[string]bool, len(rootRefs))
	for _, ref := range rootRefs {
		if ref == "" || rootSeen[ref] {
			return nil, nil, ErrTopology
		}
		rootSeen[ref] = true
		root, exists := nodes[ref]
		if !exists || root.ParentRef != nil {
			return nil, nil, ErrTopology
		}
	}
	for ref, node := range nodes {
		if node.ParentRef == nil && !rootSeen[ref] {
			return nil, nil, ErrTopology
		}
		if node.ParentRef != nil {
			parent, exists := nodes[*node.ParentRef]
			if !exists || countRef(parent.ChildRefs, ref) != 1 {
				return nil, nil, ErrTopology
			}
		}
		childrenSeen := make(map[string]bool, len(node.ChildRefs))
		for _, childRef := range node.ChildRefs {
			if childRef == "" || childrenSeen[childRef] {
				return nil, nil, ErrTopology
			}
			childrenSeen[childRef] = true
			child, exists := nodes[childRef]
			if !exists || child.ParentRef == nil || *child.ParentRef != ref {
				return nil, nil, ErrTopology
			}
		}
	}
	ordered := make([]Element, 0, len(nodes))
	indexByRef := make(map[string]int, len(nodes))
	visiting := make(map[string]bool, len(nodes))
	visited := make(map[string]bool, len(nodes))
	var walk func(string, int) error
	walk = func(ref string, depth int) error {
		if depth > MaxDepth {
			return ErrLimitExceeded
		}
		if visiting[ref] || visited[ref] {
			return ErrTopology
		}
		visiting[ref] = true
		node := nodes[ref]
		indexByRef[ref] = len(ordered)
		ordered = append(ordered, node)
		for _, childRef := range node.ChildRefs {
			if err := walk(childRef, depth+1); err != nil {
				return err
			}
		}
		delete(visiting, ref)
		visited[ref] = true
		return nil
	}
	for _, rootRef := range rootRefs {
		if err := walk(rootRef, 0); err != nil {
			return nil, nil, err
		}
	}
	if len(visited) != len(nodes) {
		return nil, nil, ErrTopology
	}
	for _, ref := range rootRefs {
		rootIndexes = append(rootIndexes, indexByRef[ref])
	}
	return ordered, rootIndexes, nil
}

func canonicalize(snapshot Snapshot, rootIndexes []int) ([]byte, error) {
	indexByRef := make(map[string]int, len(snapshot.Elements))
	for index, element := range snapshot.Elements {
		indexByRef[element.Ref] = index
	}
	canonicalElements := make([]canonicalNode, len(snapshot.Elements))
	for index, element := range snapshot.Elements {
		var parentIndex *int
		if element.ParentRef != nil {
			value, exists := indexByRef[*element.ParentRef]
			if !exists {
				return nil, ErrTopology
			}
			parentIndex = &value
		}
		childIndexes := make([]int, len(element.ChildRefs))
		for childIndex, ref := range element.ChildRefs {
			value, exists := indexByRef[ref]
			if !exists {
				return nil, ErrTopology
			}
			childIndexes[childIndex] = value
		}
		canonicalElements[index] = canonicalNode{
			Role: element.Role, Identifier: element.Identifier,
			ValueStatus: element.ValueStatus, Value: element.Value,
			ParentIndex: parentIndex, ChildIndexes: childIndexes,
		}
	}
	document := canonicalDocument{
		SchemaVersion: SchemaVersion, BundleID: snapshot.BundleID, FixtureNonce: snapshot.FixtureNonce,
		RootIndexes: rootIndexes, Elements: canonicalElements, Coverage: snapshot.Coverage,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encoding canonical fixture projection: %w", err)
	}
	if len(encoded) > MaxEnvelopeBytes {
		return nil, ErrLimitExceeded
	}
	return encoded, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrMalformedEnvelope
	}
	return nil
}

func isSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validNonce(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') &&
			!(char >= '0' && char <= '9') && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func countRef(refs []string, target string) int {
	count := 0
	for _, ref := range refs {
		if ref == target {
			count++
		}
	}
	return count
}
