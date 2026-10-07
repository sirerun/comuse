package comuse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const metadataSchemaVersion = 1

var (
	ErrInvalidMetadata = errors.New("invalid response metadata")
	ErrInvalidPayload  = errors.New("invalid closed result payload")
	decimalCostPattern = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,8})?$`)
)

type ActualUsageRecord struct {
	Kind         string  `json:"kind"`
	Source       string  `json:"source"`
	Model        string  `json:"model"`
	InputTokens  uint64  `json:"input_tokens"`
	OutputTokens uint64  `json:"output_tokens"`
	ImageTokens  uint64  `json:"image_tokens"`
	CostUSD      *string `json:"cost_usd"`
}

type EstimateUsageRecord struct {
	Kind            string  `json:"kind"`
	Source          string  `json:"source"`
	Model           string  `json:"model"`
	Version         string  `json:"version"`
	Detail          string  `json:"detail"`
	Method          string  `json:"method"`
	InputTokensEst  *uint64 `json:"input_tokens_est"`
	OutputTokensEst *uint64 `json:"output_tokens_est"`
	ImageTokensEst  *uint64 `json:"image_tokens_est"`
	CostUSDEst      *string `json:"cost_usd_est"`
}

// ActualModelUsage is a validated, host-reported model usage record. The
// toolkit never derives these values from response bytes or action content.
type ActualModelUsage struct {
	Source       string
	Model        string
	InputTokens  uint64
	OutputTokens uint64
	ImageTokens  uint64
	CostUSD      *string
}

// EstimateModelUsage is a host-labeled estimate with explicit provenance.
type EstimateModelUsage struct {
	Source          string
	Model           string
	Version         string
	Detail          string
	Method          string
	InputTokensEst  *uint64
	OutputTokensEst *uint64
	ImageTokensEst  *uint64
	CostUSDEst      *string
}

// ModelUsage preserves actual and estimated provenance as separate records.
// A zero ModelUsage serializes both records as null.
type ModelUsage struct {
	Actual   *ActualUsageRecord   `json:"actual"`
	Estimate *EstimateUsageRecord `json:"estimate"`
}

func (m *ModelUsage) clone() *ModelUsage {
	if m == nil {
		return nil
	}
	out := ModelUsage{}
	if m.Actual != nil {
		actual := *m.Actual
		actual.CostUSD = cloneString(actual.CostUSD)
		out.Actual = &actual
	}
	if m.Estimate != nil {
		estimate := *m.Estimate
		estimate.InputTokensEst = cloneUint64(estimate.InputTokensEst)
		estimate.OutputTokensEst = cloneUint64(estimate.OutputTokensEst)
		estimate.ImageTokensEst = cloneUint64(estimate.ImageTokensEst)
		estimate.CostUSDEst = cloneString(estimate.CostUSDEst)
		out.Estimate = &estimate
	}
	return &out
}

func (c *CallSnapshot) ReportActual(report ActualModelUsage) error {
	if c == nil {
		return ErrInvalidMetadata
	}
	usage, err := newModelUsage(&report, nil)
	if err != nil {
		return ErrInvalidMetadata
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return ErrCallFinished
	}
	if c.modelUsage == nil {
		c.modelUsage = usage
	} else {
		c.modelUsage.Actual = usage.Actual
	}
	return nil
}

func (c *CallSnapshot) ReportEstimate(report EstimateModelUsage) error {
	if c == nil {
		return ErrInvalidMetadata
	}
	usage, err := newModelUsage(nil, &report)
	if err != nil {
		return ErrInvalidMetadata
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return ErrCallFinished
	}
	if c.modelUsage == nil {
		c.modelUsage = usage
	} else {
		c.modelUsage.Estimate = usage.Estimate
	}
	return nil
}

func newModelUsage(actualReport *ActualModelUsage, estimateReport *EstimateModelUsage) (*ModelUsage, error) {
	usage := &ModelUsage{}
	if actualReport != nil {
		actual := ActualUsageRecord{Kind: "actual", Source: actualReport.Source, Model: actualReport.Model,
			InputTokens: actualReport.InputTokens, OutputTokens: actualReport.OutputTokens, ImageTokens: actualReport.ImageTokens,
			CostUSD: cloneString(actualReport.CostUSD)}
		if !validIdentifier(actual.Source) || !validIdentifier(actual.Model) ||
			!jsonSafe(actual.InputTokens) || !jsonSafe(actual.OutputTokens) || !jsonSafe(actual.ImageTokens) || !validCost(actual.CostUSD) {
			return nil, ErrInvalidMetadata
		}
		usage.Actual = &actual
	}
	if estimateReport != nil {
		estimate := EstimateUsageRecord{Kind: "estimate", Source: estimateReport.Source, Model: estimateReport.Model,
			Version: estimateReport.Version, Detail: estimateReport.Detail, Method: estimateReport.Method,
			InputTokensEst: cloneUint64(estimateReport.InputTokensEst), OutputTokensEst: cloneUint64(estimateReport.OutputTokensEst),
			ImageTokensEst: cloneUint64(estimateReport.ImageTokensEst), CostUSDEst: cloneString(estimateReport.CostUSDEst)}
		if !validIdentifier(estimate.Source) || !validIdentifier(estimate.Model) || !validIdentifier(estimate.Version) ||
			!validIdentifier(estimate.Detail) || !validIdentifier(estimate.Method) ||
			!validOptionalJSONSafe(estimate.InputTokensEst) || !validOptionalJSONSafe(estimate.OutputTokensEst) ||
			!validOptionalJSONSafe(estimate.ImageTokensEst) || !validCost(estimate.CostUSDEst) {
			return nil, ErrInvalidMetadata
		}
		usage.Estimate = &estimate
	}
	return usage, nil
}

func validIdentifier(value string) bool {
	if !utf8.ValidString(value) || len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("._:/-", c) {
			return false
		}
	}
	return true
}
func validCost(value *string) bool             { return value == nil || decimalCostPattern.MatchString(*value) }
func validOptionalJSONSafe(value *uint64) bool { return value == nil || jsonSafe(*value) }
func jsonSafe(value uint64) bool               { return value <= JSONSafeIntegerMax }
func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// ResultPayload can only be made by the closed payload constructors below.
// Its JSON representation is private so arbitrary maps or RawMessage values
// cannot be injected into the authoritative envelope builder.
type ResultPayload struct{ encoded json.RawMessage }

// NewResultPayload accepts only the closed schema DTOs declared by this file.
func NewResultPayload(value any) (ResultPayload, error) {
	if value == nil {
		return ResultPayload{encoded: json.RawMessage("null")}, nil
	}
	if !allowedResultPayload(value) {
		return ResultPayload{}, ErrInvalidPayload
	}
	if err := validateResultValue(value); err != nil {
		return ResultPayload{}, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ResultPayload{}, fmt.Errorf("encode closed result payload: %w", err)
	}
	return ResultPayload{encoded: append(json.RawMessage(nil), encoded...)}, nil
}

func validateResultValue(value any) error {
	switch v := value.(type) {
	case MetadataDoctor:
		if v.Permissions == nil {
			return ErrInvalidPayload
		}
		for key, permission := range v.Permissions {
			if key != "accessibility" && key != "input" && key != "screen_capture" {
				return ErrInvalidPayload
			}
			switch permission {
			case "granted", "denied", "unavailable", "not_requested":
			default:
				return ErrInvalidPayload
			}
		}
		if len(v.Capabilities.Reasons) > 32 {
			return ErrInvalidPayload
		}
		for _, reason := range v.Capabilities.Reasons {
			if !validReason(reason) {
				return ErrInvalidPayload
			}
		}
	case MetadataActionResult:
		if !validOpaque(v.ActionID) || (v.Execution != "not_applied" && v.Execution != "applied" && v.Execution != "partially_applied" && v.Execution != "unknown") ||
			(v.StateStatus != "available" && v.StateStatus != "unavailable") || (v.Cleanup != "complete" && v.Cleanup != "dirty" && v.Cleanup != "unknown") || !validVerification(v.Verification) {
			return ErrInvalidPayload
		}
	case MetadataElementContent:
		if !validObservationValue(v) {
			return ErrInvalidPayload
		}
	case MetadataWaitResult:
		if !validWaitResult(v) {
			return ErrInvalidPayload
		}
	case LedgerSnapshot:
		if v.Session == "" || !validOpaque(v.Session) || v.RetainedBytes > maxRetainedBytes || !ledgerValuesSafe(v) || !validModelUsage(v.ModelUsage) {
			return ErrInvalidPayload
		}
	case MetadataLegacySnapshot:
		if v.Elements == nil || !validOpaque(v.WindowRef) || !validStateID(v.StateID) || !validTimestamp(v.ObservedAt) {
			return ErrInvalidPayload
		}
		if len(v.Elements) > 10000 {
			return ErrInvalidPayload
		}
		for _, element := range v.Elements {
			if !validOpaque(element.Ref) || len(element.Role) == 0 || len([]rune(element.Role)) > 128 || !utf8.ValidString(element.Role) || !utf8.ValidString(element.Label) || len(element.Label) > 4096 || element.Order < 0 || element.Order > 9999 {
				return ErrInvalidPayload
			}
			if element.ParentRef != "" && !validOpaque(element.ParentRef) {
				return ErrInvalidPayload
			}
			if element.Classification != "normal" && element.Classification != "secure" && element.Classification != "unknown" {
				return ErrInvalidPayload
			}
			if element.Value != nil && (element.Classification != "normal" || !utf8.ValidString(*element.Value) || len(*element.Value) > 8192) {
				return ErrInvalidPayload
			}
			if len(element.Actions) > 16 || !validUniqueStrings(element.Actions, validActionKinds) {
				return ErrInvalidPayload
			}
		}
		if v.Coverage.Reason != "" && !validReason(v.Coverage.Reason) {
			return ErrInvalidPayload
		}
	case []MetadataWindow:
		if v == nil || len(v) > 10000 {
			return ErrInvalidPayload
		}
		for _, window := range v {
			if !validOpaque(window.Ref) || len(window.Title) > 4096 || !utf8.ValidString(window.Title) || window.Process.PID < 0 {
				return ErrInvalidPayload
			}
		}
	default:
		return ErrInvalidPayload
	}
	return nil
}

func validVerification(v MetadataVerification) bool {
	if v.Status != "verified" && v.Status != "failed" && v.Status != "unavailable" {
		return false
	}
	return v.Reason == "" || validReason(v.Reason)
}
func validReason(v string) bool {
	if len(v) == 0 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}
func validWaitResult(v MetadataWaitResult) bool {
	switch v.Condition {
	case "window_appears", "window_closed", "element_exists", "element_enabled", "element_checked":
	default:
		return false
	}
	if v.ElapsedMS > JSONSafeIntegerMax {
		return false
	}
	switch v.Reason {
	case "satisfied", "timeout", "cancelled", "ambiguous", "unavailable":
	default:
		return false
	}
	if v.WindowRef != nil && !validOpaque(*v.WindowRef) {
		return false
	}
	if v.FinalStateID != nil && !validStateID(*v.FinalStateID) {
		return false
	}
	if v.Satisfied != (v.Reason == "satisfied") {
		return false
	}
	if v.Reason == "ambiguous" && (v.Condition != "window_appears" || v.WindowRef != nil || v.Satisfied) {
		return false
	}
	if v.Reason == "satisfied" && v.Condition == "window_appears" && v.WindowRef == nil {
		return false
	}
	return true
}
func ledgerValuesSafe(v LedgerSnapshot) bool {
	values := []uint64{v.Actions, v.Observations.Screenshot, v.Observations.A11y, v.Observations.State,
		v.SemanticResults.Snapshot, v.SemanticResults.Delta, v.SemanticResults.Unchanged, v.BaselineResets,
		v.EncodedImageBytes, v.SerializedTextBytes, v.Images, v.ElapsedMS}
	for _, value := range values {
		if !jsonSafe(value) {
			return false
		}
	}
	return true
}

func allowedResultPayload(value any) bool {
	switch value.(type) {
	case MetadataDoctor, MetadataActionResult, MetadataElementContent, MetadataWaitResult,
		LedgerSnapshot, MetadataLegacySnapshot, []MetadataWindow:
		return true
	default:
		return false
	}
}

// StatePayload and ObservationPayload are private-encoding wrappers around
// closed schema DTOs. Callers cannot inject arbitrary JSON into these fields.
type StatePayload struct{ encoded json.RawMessage }
type ObservationPayload struct{ encoded json.RawMessage }

func NewStatePayload(value *MetadataCompactState) (StatePayload, error) {
	if value == nil {
		return StatePayload{encoded: json.RawMessage("null")}, nil
	}
	if !validOpaque(value.DisplayID) || value.DisplayGeneration == 0 || value.DisplayGeneration > JSONSafeIntegerMax ||
		(value.FocusedWindow != nil && (!validOpaque(value.FocusedWindow.Ref) || !utf8.ValidString(value.FocusedWindow.Title) || len(value.FocusedWindow.Title) > 4096)) {
		return StatePayload{}, ErrInvalidPayload
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return StatePayload{}, fmt.Errorf("encode compact state: %w", err)
	}
	return StatePayload{encoded: append(json.RawMessage(nil), encoded...)}, nil
}

func NewObservationPayload(value any) (ObservationPayload, error) {
	if value == nil {
		return ObservationPayload{encoded: json.RawMessage("null")}, nil
	}
	if !validObservationValue(value) {
		return ObservationPayload{}, ErrInvalidPayload
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ObservationPayload{}, fmt.Errorf("encode observation payload: %w", err)
	}
	return ObservationPayload{encoded: append(json.RawMessage(nil), encoded...)}, nil
}

type MetadataCompactState struct {
	FocusedWindow     *MetadataFocusedWindow `json:"focused_window"`
	DisplayID         string                 `json:"display_id"`
	DisplayGeneration uint64                 `json:"display_generation"`
}
type MetadataFocusedWindow struct {
	Ref   string `json:"ref"`
	Title string `json:"title"`
}
type MetadataSnapshot struct {
	SchemaVersion  int                     `json:"schema_version"`
	ScopeID        string                  `json:"scope_id"`
	WindowRef      string                  `json:"window_ref"`
	StateID        string                  `json:"state_id"`
	ObservedAt     string                  `json:"observed_at"`
	ActionSequence uint64                  `json:"action_sequence"`
	Coverage       MetadataCoverage        `json:"coverage"`
	Context        MetadataContext         `json:"context"`
	Nodes          map[string]MetadataNode `json:"nodes"`
	Kind           string                  `json:"kind"`
	Historical     bool                    `json:"historical"`
	ResetReason    *string                 `json:"reset_reason,omitempty"`
}
type MetadataDelta struct {
	SchemaVersion  int                     `json:"schema_version"`
	ScopeID        string                  `json:"scope_id"`
	WindowRef      string                  `json:"window_ref"`
	StateID        string                  `json:"state_id"`
	BaseStateID    string                  `json:"base_state_id"`
	ObservedAt     string                  `json:"observed_at"`
	ActionSequence uint64                  `json:"action_sequence"`
	Coverage       MetadataCoverage        `json:"coverage"`
	Context        MetadataContext         `json:"context"`
	Upsert         map[string]MetadataNode `json:"upsert"`
	Removed        []string                `json:"removed"`
	Kind           string                  `json:"kind"`
}
type MetadataUnchanged struct {
	SchemaVersion  int              `json:"schema_version"`
	ScopeID        string           `json:"scope_id"`
	WindowRef      string           `json:"window_ref"`
	StateID        string           `json:"state_id"`
	BaseStateID    string           `json:"base_state_id"`
	ObservedAt     string           `json:"observed_at"`
	ActionSequence uint64           `json:"action_sequence"`
	Coverage       MetadataCoverage `json:"coverage"`
	Context        MetadataContext  `json:"context"`
	Kind           string           `json:"kind"`
}
type MetadataCoverage struct {
	Status      string   `json:"status"`
	Truncated   bool     `json:"truncated"`
	Limitations []string `json:"limitations"`
}
type MetadataContext struct {
	RootRefs          []string `json:"root_refs"`
	FocusedElementRef *string  `json:"focused_element_ref"`
}
type MetadataNode struct {
	ParentRef      *string  `json:"parent_ref"`
	ChildRefs      []string `json:"child_refs"`
	Role           string   `json:"role"`
	Label          *string  `json:"label,omitempty"`
	Value          *string  `json:"value,omitempty"`
	Enabled        *bool    `json:"enabled,omitempty"`
	Checked        *bool    `json:"checked,omitempty"`
	Selected       *bool    `json:"selected,omitempty"`
	Actions        []string `json:"actions"`
	Classification string   `json:"classification"`
}

var validActionKinds = map[string]struct{}{"press": {}, "pick": {}, "focus": {}, "replace": {}, "insert": {}, "scroll": {}, "click": {}}

func validCoverage(v MetadataCoverage) bool {
	if v.Status != "complete" && v.Status != "partial" && v.Status != "unavailable" || v.Limitations == nil {
		return false
	}
	return len(v.Limitations) <= 32 && validUniqueStrings(v.Limitations, nil)
}
func validContext(v MetadataContext) bool {
	return v.RootRefs != nil && len(v.RootRefs) <= 10000 && validUniqueStrings(v.RootRefs, nil) && (v.FocusedElementRef == nil || validOpaque(*v.FocusedElementRef))
}
func validNodeMap(nodes map[string]MetadataNode) bool {
	if len(nodes) > 10000 {
		return false
	}
	for ref, node := range nodes {
		if !validOpaque(ref) || node.ChildRefs == nil || node.Actions == nil || (node.ParentRef != nil && !validOpaque(*node.ParentRef)) ||
			len(node.Role) == 0 || len([]rune(node.Role)) > 128 || !utf8.ValidString(node.Role) ||
			(node.Classification != "normal" && node.Classification != "secure" && node.Classification != "unknown") ||
			(node.Classification != "normal" && node.Value != nil) ||
			(node.Label != nil && (!utf8.ValidString(*node.Label) || len(*node.Label) > 4096)) ||
			(node.Value != nil && (!utf8.ValidString(*node.Value) || len(*node.Value) > 8192)) ||
			len(node.ChildRefs) > 10000 || !validUniqueStrings(node.ChildRefs, nil) ||
			len(node.Actions) > 16 || !validUniqueStrings(node.Actions, validActionKinds) {
			return false
		}
	}
	return true
}
func validUniqueStrings(values []string, allowed map[string]struct{}) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validOpaque(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
		if allowed != nil {
			if _, ok := allowed[value]; !ok {
				return false
			}
		}
	}
	return true
}
func validObservationValue(value any) bool {
	switch v := value.(type) {
	case MetadataSnapshot:
		if v.SchemaVersion != 1 || v.Kind != "snapshot" || v.Nodes == nil || !validSemanticIdentity(v.StateID, v.ScopeID, v.WindowRef, v.ObservedAt) || !jsonSafe(v.ActionSequence) || !validCoverage(v.Coverage) || !validContext(v.Context) || !validNodeMap(v.Nodes) {
			return false
		}
		if v.ResetReason != nil {
			switch *v.ResetReason {
			case "missing_baseline", "expired_baseline", "incompatible_baseline", "partial_coverage", "identity_uncertain", "delta_not_smaller":
			default:
				return false
			}
		}
		return true
	case MetadataDelta:
		return v.SchemaVersion == 1 && v.Kind == "delta" && v.Upsert != nil && v.Removed != nil && validSemanticIdentity(v.StateID, v.ScopeID, v.WindowRef, v.ObservedAt) && validStateID(v.BaseStateID) && jsonSafe(v.ActionSequence) && validCoverage(v.Coverage) && validContext(v.Context) && validNodeMap(v.Upsert) && len(v.Removed) <= 10000 && validUniqueStrings(v.Removed, nil)
	case MetadataUnchanged:
		return v.SchemaVersion == 1 && v.Kind == "unchanged" && validSemanticIdentity(v.StateID, v.ScopeID, v.WindowRef, v.ObservedAt) && validStateID(v.BaseStateID) && jsonSafe(v.ActionSequence) && validCoverage(v.Coverage) && validContext(v.Context)
	case MetadataElementContent:
		return v.Kind == "element_content" && validOpaque(v.WindowRef) && validOpaque(v.ElementRef) && validStateID(v.StateID) && validTimestamp(v.ObservedAt) && utf8.ValidString(v.Text) && len(v.Text) <= 8192
	default:
		return false
	}
}

func validSemanticIdentity(stateID, scopeID, windowRef, observedAt string) bool {
	return validStateID(stateID) && validOpaque(scopeID) && validOpaque(windowRef) && validTimestamp(observedAt)
}
func validTimestamp(value string) bool {
	if len(value) > 40 {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}
func validStateID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func validOpaque(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

func (p ResultPayload) MarshalJSON() ([]byte, error) {
	if len(p.encoded) == 0 || !json.Valid(p.encoded) {
		return nil, ErrInvalidPayload
	}
	return append([]byte(nil), p.encoded...), nil
}
func (p StatePayload) MarshalJSON() ([]byte, error) {
	if len(p.encoded) == 0 || !json.Valid(p.encoded) {
		return nil, ErrInvalidPayload
	}
	return append([]byte(nil), p.encoded...), nil
}
func (p ObservationPayload) MarshalJSON() ([]byte, error) {
	if len(p.encoded) == 0 || !json.Valid(p.encoded) {
		return nil, ErrInvalidPayload
	}
	return append([]byte(nil), p.encoded...), nil
}

type MetadataDoctor struct {
	Capabilities MetadataCapabilities `json:"capabilities"`
	Permissions  map[string]string    `json:"permissions"`
}
type MetadataCapabilities struct {
	Accessibility  bool     `json:"accessibility"`
	Input          bool     `json:"input"`
	ScreenCapture  bool     `json:"screen_capture"`
	QualifiedInput bool     `json:"qualified_input"`
	Reasons        []string `json:"reasons,omitempty"`
}
type MetadataWindow struct {
	Process MetadataProcessIdentity `json:"process"`
	Ref     string                  `json:"ref"`
	Title   string                  `json:"title"`
}
type MetadataProcessIdentity struct {
	PID      int32  `json:"pid"`
	BundleID string `json:"bundle_id"`
	LaunchID string `json:"launch_id"`
}
type MetadataActionResult struct {
	ActionID     string               `json:"action_id"`
	Execution    string               `json:"execution"`
	Verification MetadataVerification `json:"verification"`
	StateStatus  string               `json:"state_status"`
	Cleanup      string               `json:"cleanup"`
}
type MetadataVerification struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}
type MetadataElementContent struct {
	Kind       string `json:"kind"`
	WindowRef  string `json:"window_ref"`
	ElementRef string `json:"element_ref"`
	StateID    string `json:"state_id"`
	ObservedAt string `json:"observed_at"`
	Text       string `json:"text"`
	Truncated  bool   `json:"truncated"`
}
type MetadataWaitResult struct {
	Condition    string  `json:"condition"`
	Satisfied    bool    `json:"satisfied"`
	ElapsedMS    uint64  `json:"elapsed_ms"`
	Reason       string  `json:"reason"`
	WindowRef    *string `json:"window_ref"`
	FinalStateID *string `json:"final_state_id"`
}
type MetadataLegacySnapshot struct {
	WindowRef  string                  `json:"window_ref"`
	StateID    string                  `json:"state_id"`
	ObservedAt string                  `json:"observed_at"`
	Coverage   MetadataLegacyCoverage  `json:"coverage"`
	Elements   []MetadataLegacyElement `json:"elements"`
}
type MetadataLegacyCoverage struct {
	Complete bool   `json:"complete"`
	Reason   string `json:"reason,omitempty"`
}
type MetadataLegacyElement struct {
	Ref            string   `json:"ref"`
	ParentRef      string   `json:"parent_ref,omitempty"`
	Order          int      `json:"order"`
	Role           string   `json:"role"`
	Label          string   `json:"label,omitempty"`
	Value          *string  `json:"value,omitempty"`
	Enabled        *bool    `json:"enabled,omitempty"`
	Actions        []string `json:"actions,omitempty"`
	Classification string   `json:"classification"`
}

// ResultEnvelope is the schema-matched metadata envelope. Existing Envelope
// remains unchanged so old struct literals continue compiling until the
// coordinator integrates the shared API.
type ResultEnvelope struct {
	SchemaVersion    int                  `json:"schema_version"`
	Status           string               `json:"status"`
	OK               bool                 `json:"ok"`
	Action           string               `json:"action"`
	ActionID         *string              `json:"action_id,omitempty"`
	Execution        string               `json:"execution"`
	Method           *string              `json:"method"`
	DurationMS       uint64               `json:"duration_ms"`
	StateStatus      string               `json:"state_status"`
	State            StatePayload         `json:"state"`
	Verification     MetadataVerification `json:"verification"`
	Cleanup          string               `json:"cleanup"`
	Usage            Usage                `json:"usage"`
	Observation      ObservationPayload   `json:"observation"`
	Result           ResultPayload        `json:"result"`
	Error            *SafeError           `json:"error"`
	ObservationError *SafeError           `json:"observation_error"`
	CompletedSteps   []string             `json:"completed_steps"`
}

// EnvelopeMetadata carries only closed envelope metadata. Result and
// observation bodies must come from their typed constructors.
type EnvelopeMetadata struct {
	Status           string
	OK               bool
	Action           string
	ActionID         *string
	Execution        string
	Method           *string
	StateStatus      string
	Verification     MetadataVerification
	Cleanup          string
	Error            *SafeError
	ObservationError *SafeError
	CompletedSteps   []string
}

func NewResultEnvelope(metadata EnvelopeMetadata, state StatePayload, observation ObservationPayload, result ResultPayload) (ResultEnvelope, error) {
	e := ResultEnvelope{
		SchemaVersion: metadataSchemaVersion, Status: metadata.Status, OK: metadata.OK, Action: metadata.Action,
		ActionID: cloneString(metadata.ActionID), Execution: metadata.Execution, Method: cloneString(metadata.Method),
		StateStatus: metadata.StateStatus, State: state, Verification: metadata.Verification, Cleanup: metadata.Cleanup,
		Observation: observation, Result: result, Error: cloneSafeError(metadata.Error),
		ObservationError: cloneSafeError(metadata.ObservationError), CompletedSteps: append([]string{}, metadata.CompletedSteps...),
	}
	if err := e.Validate(); err != nil {
		return ResultEnvelope{}, err
	}
	return e, nil
}

func cloneSafeError(value *SafeError) *SafeError {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// SafeError contains only the frozen closed code vocabulary; messages always
// equal their code and never include backend details.
type SafeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var safeErrorCodes = map[string]struct{}{
	"invalid_request": {}, "policy_refused": {}, "approval_required": {}, "element_stale": {},
	"state_expired": {}, "permission_denied": {}, "unsupported": {}, "backend_unavailable": {},
	"desktop_busy": {}, "rate_limited": {}, "budget_exceeded": {}, "cancelled": {},
	"session_closed": {}, "unknown_outcome": {}, "internal_error": {}, "replay_result_expired": {},
}

func NewSafeError(code string) (*SafeError, error) {
	if _, ok := safeErrorCodes[code]; !ok {
		return nil, ErrInvalidMetadata
	}
	return &SafeError{Code: code, Message: code}, nil
}

var validActions = map[string]struct{}{
	"doctor": {}, "state": {}, "windows": {}, "a11y": {}, "read_element": {}, "wait": {}, "ledger": {},
	"click_element": {}, "element_action": {}, "write_element": {}, "scroll_element": {}, "click": {},
	"type_text": {}, "press_key": {}, "scroll": {}, "drag": {}, "focus_window": {},
}
var validMethods = map[string]struct{}{
	"ax_press": {}, "ax_pick": {}, "ax_focus": {}, "ax_set_value": {}, "ax_scroll": {},
	"ax_focus_window": {}, "cg_click": {}, "cg_unicode": {}, "cg_key": {}, "cg_scroll": {}, "cg_drag": {},
}
var validSteps = map[string]struct{}{
	"focus": {}, "press": {}, "pick": {}, "set_value": {}, "unicode": {}, "key_down": {}, "key_up": {},
	"mouse_down": {}, "mouse_up": {}, "mouse_move": {}, "scroll": {}, "cleanup": {},
}

// Validate checks the closed envelope vocabulary and rejects noncanonical
// error text or payloads. State/observation are coordinator-owned schema DTOs
// and must be supplied by the shared typed API before this builder is used.
func (e ResultEnvelope) Validate() error {
	if e.SchemaVersion != metadataSchemaVersion || (e.Status != "ok" && e.Status != "error") {
		return ErrInvalidMetadata
	}
	if _, ok := validActions[e.Action]; !ok {
		return ErrInvalidMetadata
	}
	switch e.Execution {
	case "not_applied", "applied", "partially_applied", "unknown":
	default:
		return ErrInvalidMetadata
	}
	switch e.StateStatus {
	case "available", "partial", "unavailable":
	default:
		return ErrInvalidMetadata
	}
	switch e.Cleanup {
	case "complete", "dirty", "unknown":
	default:
		return ErrInvalidMetadata
	}
	if e.Method != nil {
		if _, ok := validMethods[*e.Method]; !ok {
			return ErrInvalidMetadata
		}
	}
	if e.DurationMS > JSONSafeIntegerMax || len(e.CompletedSteps) > 128 || (e.ActionID != nil && !validOpaque(*e.ActionID)) {
		return ErrInvalidMetadata
	}
	if e.CompletedSteps == nil {
		return ErrInvalidMetadata
	}
	if !validVerification(e.Verification) {
		return ErrInvalidMetadata
	}
	if e.Error != nil {
		if _, ok := safeErrorCodes[e.Error.Code]; !ok || e.Error.Message != e.Error.Code {
			return ErrInvalidMetadata
		}
	}
	if e.ObservationError != nil {
		if _, ok := safeErrorCodes[e.ObservationError.Code]; !ok || e.ObservationError.Message != e.ObservationError.Code {
			return ErrInvalidMetadata
		}
	}
	for _, step := range e.CompletedSteps {
		if _, ok := validSteps[step]; !ok {
			return ErrInvalidMetadata
		}
	}
	if e.StateStatus == "unavailable" && string(e.State.encoded) != "null" {
		return ErrInvalidPayload
	}
	if e.StateStatus != "unavailable" && string(e.State.encoded) == "null" {
		return ErrInvalidPayload
	}
	if e.OK != (e.Status == "ok") {
		return ErrInvalidMetadata
	}
	if (e.Status == "ok" && e.Error != nil) || (e.Status == "error" && e.Error == nil) {
		return ErrInvalidMetadata
	}
	switch e.Action {
	case "click_element", "element_action", "write_element", "scroll_element", "click", "type_text", "press_key", "scroll", "drag", "focus_window":
		if e.ActionID == nil {
			return ErrInvalidMetadata
		}
	default:
		if e.ActionID != nil {
			return ErrInvalidMetadata
		}
	}
	if e.Result.encoded == nil {
		return ErrInvalidPayload
	}
	if !json.Valid(e.Result.encoded) || !json.Valid(e.State.encoded) || !json.Valid(e.Observation.encoded) {
		return ErrInvalidPayload
	}
	if !validUsage(e.Usage) {
		return ErrInvalidMetadata
	}
	return nil
}

func validUsage(v Usage) bool {
	values := []uint64{v.Actions, v.Observations.Screenshot, v.Observations.A11y, v.Observations.State,
		v.SemanticResults.Snapshot, v.SemanticResults.Delta, v.SemanticResults.Unchanged, v.BaselineResets,
		v.EncodedImageBytes, v.SerializedTextBytes, v.Images, v.ElapsedMS, v.DurationMS}
	for _, value := range values {
		if !jsonSafe(value) {
			return false
		}
	}
	if v.RetainedBytes > maxRetainedBytes {
		return false
	}
	return validModelUsage(v.ModelUsage)
}

func validModelUsage(modelUsage *ModelUsage) bool {
	if modelUsage == nil {
		return true
	}
	if modelUsage.Actual != nil {
		a := modelUsage.Actual
		if a.Kind != "actual" || !validIdentifier(a.Source) || !validIdentifier(a.Model) || !jsonSafe(a.InputTokens) || !jsonSafe(a.OutputTokens) || !jsonSafe(a.ImageTokens) || !validCost(a.CostUSD) {
			return false
		}
	}
	if modelUsage.Estimate != nil {
		e := modelUsage.Estimate
		if e.Kind != "estimate" || !validIdentifier(e.Source) || !validIdentifier(e.Model) || !validIdentifier(e.Version) || !validIdentifier(e.Detail) || !validIdentifier(e.Method) || !validOptionalJSONSafe(e.InputTokensEst) || !validOptionalJSONSafe(e.OutputTokensEst) || !validOptionalJSONSafe(e.ImageTokensEst) || !validCost(e.CostUSDEst) {
			return false
		}
	}
	return true
}

func envelopeWithoutUsage(e ResultEnvelope) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	data, err := e.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal response envelope: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("decode response envelope for accounting: %w", err)
	}
	delete(fields, "usage")
	return canonicalJSON(fields)
}

func canonicalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

// FinishCall measures one immutable envelope without its usage field, charges
// those bytes once, and returns the envelope with per-call accounting. A
// LedgerSnapshot result supplied by the caller remains its precharge view.
func (c *CallSnapshot) FinishCall(envelope ResultEnvelope) (ResultEnvelope, error) {
	if c == nil {
		return ResultEnvelope{}, ErrInvalidMetadata
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return ResultEnvelope{}, ErrCallFinished
	}
	encoded, err := envelopeWithoutUsage(envelope)
	if err != nil {
		return ResultEnvelope{}, err
	}
	byteCharge := uint64(len(encoded))
	if !jsonSafe(byteCharge) {
		byteCharge = JSONSafeIntegerMax
	}
	if c.ledger != nil {
		if _, err := c.ledger.Add(CounterSerializedTextBytes, byteCharge); err != nil {
			return ResultEnvelope{}, err
		}
	}
	values := c.baseline
	if c.ledger != nil {
		c.ledger.mu.Lock()
		c.ledger.advanceElapsedLocked(time.Since(c.ledger.started))
		values = c.ledger.valuesLocked()
		c.ledger.mu.Unlock()
	}
	duration := saturatingDuration(c.started)
	modelUsage := (*ModelUsage)(nil)
	if c.modelUsage != nil {
		modelUsage = c.modelUsage.clone()
	}
	envelope.DurationMS = duration
	envelope.Usage = Usage{
		Actions: c.callValues.actions,
		Observations: ObservationCounts{
			Screenshot: c.callValues.observations.Screenshot,
			A11y:       c.callValues.observations.A11y,
			State:      c.callValues.observations.State,
		},
		SemanticResults: SemanticResultCounts{
			Snapshot:  c.callValues.semantic.Snapshot,
			Delta:     c.callValues.semantic.Delta,
			Unchanged: c.callValues.semantic.Unchanged,
		},
		BaselineResets:      c.callValues.baselineResets,
		EncodedImageBytes:   c.callValues.encodedImageBytes,
		SerializedTextBytes: saturatingAdd(c.callValues.serializedTextBytes, byteCharge), Images: c.callValues.images, RetainedBytes: values.retainedBytes,
		ElapsedMS: values.elapsedMS, ModelUsage: modelUsage, DurationMS: duration,
	}
	c.finished = true
	return envelope, nil
}

// MarshalJSON ensures ResultPayload's private closed value is emitted as its
// schema JSON rather than as an implementation wrapper.
func (e ResultEnvelope) MarshalJSON() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	type wire struct {
		SchemaVersion    int                  `json:"schema_version"`
		Status           string               `json:"status"`
		OK               bool                 `json:"ok"`
		Action           string               `json:"action"`
		ActionID         *string              `json:"action_id,omitempty"`
		Execution        string               `json:"execution"`
		Method           *string              `json:"method"`
		DurationMS       uint64               `json:"duration_ms"`
		StateStatus      string               `json:"state_status"`
		State            StatePayload         `json:"state"`
		Verification     MetadataVerification `json:"verification"`
		Cleanup          string               `json:"cleanup"`
		Usage            Usage                `json:"usage"`
		Observation      ObservationPayload   `json:"observation"`
		Result           json.RawMessage      `json:"result"`
		Error            *SafeError           `json:"error"`
		ObservationError *SafeError           `json:"observation_error"`
		CompletedSteps   []string             `json:"completed_steps"`
	}
	return canonicalJSON(wire{e.SchemaVersion, e.Status, e.OK, e.Action, e.ActionID, e.Execution, e.Method, e.DurationMS, e.StateStatus, e.State, e.Verification, e.Cleanup, e.Usage, e.Observation, e.Result.encoded, e.Error, e.ObservationError, e.CompletedSteps})
}
