// Package inputprobe is the private, default-deny composition boundary for
// guarded fixture actions. It is not wired to CLI/MCP or the read-only
// bridgeclient. A production constructor must provide durable host services.
package inputprobe

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/sirerun/comuse/spikes/bridgeclient"
	"github.com/sirerun/comuse/spikes/policyprobe"
)

var (
	ErrNilGate      = errors.New("policy gate is required")
	ErrNilWriter    = errors.New("desktop writer provider is required")
	ErrNilJournal   = errors.New("durable action journal is required")
	ErrNilBackend   = errors.New("private native input backend is required")
	ErrShortKey     = errors.New("commitment key must be at least 32 bytes")
	ErrInvalidInput = errors.New("input action is invalid")
)

const (
	maxNativeRequest  = 32 * 1024
	maxNativeResponse = 64 * 1024
	maxTextBytes      = 4096
)

// desktopWriter and writerLease are deliberately package-private. The lease
// must be OS-user/GUI scoped and cross-process; a Go mutex does not qualify.
type desktopWriter interface {
	Acquire(context.Context) (writerLease, error)
}

type writerLease interface {
	Release(context.Context) error
	Quarantine(context.Context, error) error
}

// durableJournal stores only the private keyed commitment, never a bare text
// hash. Begin must be durable before policy approval consumption or dispatch.
type durableJournal interface {
	Lookup(context.Context, string) (JournalRecord, bool, error)
	Begin(context.Context, JournalRecord) (created bool, err error)
	Finish(context.Context, JournalRecord) error
}

// nativeBackend is private so callers cannot expose an arbitrary mutation Call.
// Its implementation is owned by the integrated coordinator, not bridgeclient.
type nativeBackend interface {
	Inspect(context.Context, NativeTargetRequest) (NativeClassification, error)
	InputCall(context.Context, bridgeclient.HostInputRequest) ([]byte, error)
}

type NativeTargetRequest struct {
	PID                                                                             int32
	BundleID, FixtureNonce, ProcessStartRef, WindowRef, ElementRef, ExpectedStateID string
}

type NativeClassification struct {
	Complete                                        bool
	StateID, ProcessStartRef, WindowRef, ElementRef string
	Role, Identifier, InputClass, ValueStatus       string
}

type JournalRecord struct {
	ActionID     string
	Commitment   [32]byte
	StateID      string
	Action       string
	Execution    string
	Verification string
	StateStatus  string
	Cleanup      string
	ErrorCode    string
	UpdatedAt    time.Time
}

// Request is assembled by the trusted host from a normalized fixture
// observation. Do not populate its policy fields directly from model arguments.
type Request struct {
	ActionID string
	Scope    policyprobe.Scope
	Action   policyprobe.Action
	Approval policyprobe.Approval
}

type Executor struct {
	gate      *policyprobe.Gate
	writer    desktopWriter
	journal   durableJournal
	backend   nativeBackend
	commitKey []byte
	clock     func() time.Time
}

// newExecutor is package-private until a durable journal, writer lease, and
// mutation-only native ABI are integrated by the coordinator.
func newExecutor(gate *policyprobe.Gate, writer desktopWriter, journal durableJournal, backend nativeBackend, commitmentKey []byte, clock func() time.Time) (*Executor, error) {
	switch {
	case gate == nil:
		return nil, ErrNilGate
	case writer == nil:
		return nil, ErrNilWriter
	case journal == nil:
		return nil, ErrNilJournal
	case backend == nil:
		return nil, ErrNilBackend
	case len(commitmentKey) < 32:
		return nil, ErrShortKey
	case clock == nil:
		return nil, errors.New("clock is required")
	}
	return &Executor{gate: gate, writer: writer, journal: journal, backend: backend, commitKey: append([]byte(nil), commitmentKey...), clock: clock}, nil
}

func (executor *Executor) execute(ctx context.Context, request Request) ([]byte, error) {
	if executor == nil || ctx == nil {
		return nil, errors.New("input executor and context are required")
	}
	operation, err := operationFor(request.Action.Kind)
	if err != nil {
		return refusal(request.ActionID, "unsupported", "not_applied"), err
	}
	if err := validateRequestShape(request, operation); err != nil {
		return refusal(request.ActionID, "validation_error", "not_applied"), err
	}
	lease, err := executor.writer.Acquire(ctx)
	if err != nil {
		return refusal(request.ActionID, "desktop_busy", "not_applied"), err
	}
	nativeScope := NativeTargetRequest{PID: request.Scope.Process.PID, BundleID: request.Scope.Process.BundleID, FixtureNonce: request.Scope.FixtureNonce, ProcessStartRef: request.Scope.Process.LaunchGeneration, WindowRef: request.Scope.WindowRef, ElementRef: request.Scope.ElementRef, ExpectedStateID: request.Scope.StateID}
	classification, err := executor.backend.Inspect(ctx, nativeScope)
	if err != nil {
		_ = lease.Release(context.Background())
		return refusal(request.ActionID, "classification_unavailable", "not_applied"), err
	}
	target, err := classifyNativeTarget(request, operation, classification)
	if err != nil {
		_ = lease.Release(context.Background())
		return refusal(request.ActionID, "protected_or_unsupported_target", "not_applied"), err
	}
	request.Action.Target = target
	if err := validateRequest(request, operation); err != nil {
		_ = lease.Release(context.Background())
		return refusal(request.ActionID, "validation_error", "not_applied"), err
	}
	commitment, err := executor.commitment(request)
	if err != nil {
		_ = lease.Release(context.Background())
		return refusal(request.ActionID, "validation_error", "not_applied"), err
	}
	prior, found, err := executor.journal.Lookup(ctx, request.ActionID)
	if err != nil {
		_ = lease.Release(context.Background())
		return refusal(request.ActionID, "backend_unavailable", "not_applied"), err
	}
	if found {
		if !hmac.Equal(prior.Commitment[:], commitment[:]) {
			_ = lease.Release(context.Background())
			return refusal(request.ActionID, "policy_refused", "not_applied"), errors.New("action id replay binding mismatch")
		}
		response := replaySummary(prior)
		if err := lease.Release(ctx); err != nil {
			return unknown(request.ActionID, "backend_unavailable"), err
		}
		return response, nil
	}
	record := JournalRecord{ActionID: request.ActionID, Commitment: commitment, StateID: request.Scope.StateID, Execution: "pending", UpdatedAt: executor.clock()}
	created, err := executor.journal.Begin(ctx, record)
	if err != nil {
		_ = lease.Quarantine(context.Background(), err)
		return refusal(request.ActionID, "backend_unavailable", "not_applied"), err
	}
	if !created {
		prior, found, lookupErr := executor.journal.Lookup(ctx, request.ActionID)
		if lookupErr != nil || !found {
			cause := errors.Join(errors.New("action begin conflicted without a readable prior"), lookupErr)
			_ = lease.Quarantine(context.Background(), cause)
			return unknown(request.ActionID, "backend_unavailable"), cause
		}
		if !hmac.Equal(prior.Commitment[:], commitment[:]) {
			_ = lease.Release(context.Background())
			return refusal(request.ActionID, "policy_refused", "not_applied"), errors.New("action id replay binding mismatch")
		}
		response := replaySummary(prior)
		if err := lease.Release(ctx); err != nil {
			return unknown(request.ActionID, "backend_unavailable"), err
		}
		return response, nil
	}

	decision := executor.gate.Admit(request.Scope, request.Action, request.Approval)
	if decision.Code != policyprobe.DecisionAllowed {
		response := refusal(request.ActionID, "policy_refused", "not_applied")
		finishErr := executor.finish(ctx, lease, record, "not_applied", response)
		if finishErr != nil {
			return unknown(request.ActionID, "backend_unavailable"), finishErr
		}
		return response, nil
	}
	if err := ctx.Err(); err != nil {
		response := refusal(request.ActionID, "cancelled_before_dispatch", "not_applied")
		finishErr := executor.finish(context.Background(), lease, record, "not_applied", response)
		return response, errors.Join(err, finishErr)
	}

	payload := makeHostRequest(request, operation)

	// Exactly one private backend call. Any error after dispatch is unknown; no retry.
	response, callErr := executor.backend.InputCall(ctx, payload)
	if callErr != nil {
		unknownResponse := unknown(request.ActionID, "backend_unavailable")
		if err := executor.finishUnknown(context.Background(), lease, record, unknownResponse); err != nil {
			return unknownResponse, errors.Join(callErr, err)
		}
		return unknownResponse, callErr
	}
	if err := validateNativeResponse(response, request, operation); err != nil {
		unknownResponse := unknown(request.ActionID, "backend_outcome_invalid")
		if finishErr := executor.finishUnknown(context.Background(), lease, record, unknownResponse); finishErr != nil {
			return unknownResponse, errors.Join(err, finishErr)
		}
		return unknownResponse, err
	}

	var native nativeEnvelope
	_ = json.Unmarshal(response, &native)
	if native.Execution == "unknown" || native.Cleanup.Status == "unknown" || native.Cleanup.Status == "failed" {
		if err := executor.finishUnknown(context.Background(), lease, record, response); err != nil {
			return unknown(request.ActionID, "backend_unavailable"), err
		}
		return append([]byte(nil), response...), nil
	}
	if err := executor.finish(ctx, lease, record, native.Execution, response); err != nil {
		return unknown(request.ActionID, "backend_unavailable"), err
	}
	return append([]byte(nil), response...), nil
}

func (executor *Executor) finish(ctx context.Context, lease writerLease, record JournalRecord, execution string, response []byte) error {
	record.Execution = execution
	var native nativeEnvelope
	if json.Unmarshal(response, &native) == nil {
		setTerminalMetadata(&record, native)
	}
	if record.Execution == "" {
		record.Execution = execution
	}
	record.UpdatedAt = executor.clock()
	if err := executor.journal.Finish(ctx, record); err != nil {
		_ = lease.Quarantine(context.Background(), err)
		return err
	}
	if err := lease.Release(ctx); err != nil {
		_ = lease.Quarantine(context.Background(), err)
		return err
	}
	return nil
}

func (executor *Executor) finishUnknown(ctx context.Context, lease writerLease, record JournalRecord, response []byte) error {
	var native nativeEnvelope
	if json.Unmarshal(response, &native) == nil {
		setTerminalMetadata(&record, native)
	}
	record.Execution = "unknown"
	record.UpdatedAt = executor.clock()
	journalErr := executor.journal.Finish(ctx, record)
	quarantineCause := journalErr
	if quarantineCause == nil {
		quarantineCause = errors.New("native execution outcome requires quarantine")
	}
	quarantineErr := lease.Quarantine(context.Background(), quarantineCause)
	return errors.Join(journalErr, quarantineErr)
}

func setTerminalMetadata(record *JournalRecord, native nativeEnvelope) {
	record.Action = native.Action
	record.Execution = native.Execution
	record.Verification = native.Verification.Status
	record.StateStatus = native.StateStatus
	record.Cleanup = native.Cleanup.Status
	if native.Error != nil {
		if safeErrorCode(*native.Error) {
			record.ErrorCode = *native.Error
		} else {
			record.ErrorCode = "native_error"
		}
	}
}

func replaySummary(record JournalRecord) []byte {
	if record.Execution == "pending" || record.Execution == "" {
		record.Execution, record.ErrorCode = "unknown", "outcome_unknown"
	}
	if record.Execution == "unknown" && record.ErrorCode == "" {
		record.ErrorCode = "outcome_unknown"
	}
	return marshalReplay(record)
}

func safeErrorCode(code string) bool {
	if len(code) == 0 || len(code) > 64 {
		return false
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

func (executor *Executor) commitment(request Request) ([32]byte, error) {
	canonical, err := json.Marshal(struct {
		ActionID string
		Scope    policyprobe.Scope
		Action   policyprobe.Action
	}{request.ActionID, request.Scope, request.Action})
	if err != nil {
		return [32]byte{}, err
	}
	mac := hmac.New(sha256.New, executor.commitKey)
	_, _ = mac.Write(canonical)
	var commitment [32]byte
	copy(commitment[:], mac.Sum(nil))
	return commitment, nil
}

func makeHostRequest(request Request, operation string) bridgeclient.HostInputRequest {
	return bridgeclient.HostInputRequest{
		RequestID: request.ActionID, ActionID: request.ActionID, Operation: operation,
		PID: request.Scope.Process.PID, BundleID: request.Scope.Process.BundleID,
		FixtureNonce: request.Scope.FixtureNonce, ProcessStartRef: request.Scope.Process.LaunchGeneration,
		WindowRef: request.Scope.WindowRef, ElementRef: request.Scope.ElementRef,
		ExpectedStateID: request.Scope.StateID, Text: request.Action.Text,
	}
}

func validateRequestShape(request Request, operation string) error {
	if request.ActionID == "" || len(request.ActionID) > 128 || request.Scope.StateID == "" ||
		request.Scope.Process.PID <= 0 || request.Scope.Process.BundleID != "com.sirerun.comuse.fixture" ||
		request.Scope.Process.LaunchGeneration == "" || request.Scope.FixtureNonce == "" ||
		request.Scope.WindowRef == "" || request.Scope.ElementRef == "" || request.Action.ElementRef != request.Scope.ElementRef {
		return ErrInvalidInput
	}
	if len(request.Scope.StateID) != 64 {
		return ErrInvalidInput
	}
	if _, err := hex.DecodeString(request.Scope.StateID); err != nil {
		return ErrInvalidInput
	}
	if operation == "replace" && len(request.Action.Text) > maxTextBytes {
		return ErrInvalidInput
	}
	if operation == "press" && request.Action.Text != "" {
		return ErrInvalidInput
	}
	switch operation {
	case "read_value", "replace", "press":
	default:
		return ErrInvalidInput
	}
	return nil
}

func validateRequest(request Request, operation string) error {
	if err := validateRequestShape(request, operation); err != nil {
		return err
	}
	if operation == "press" && request.Action.Target != policyprobe.TargetButton {
		return ErrInvalidInput
	}
	if (operation == "read_value" || operation == "replace") && request.Action.Target != policyprobe.TargetTextField {
		return ErrInvalidInput
	}
	return nil
}

func classifyNativeTarget(request Request, operation string, target NativeClassification) (policyprobe.TargetKind, error) {
	if !target.Complete || target.StateID != request.Scope.StateID || target.ProcessStartRef != request.Scope.Process.LaunchGeneration || target.WindowRef != request.Scope.WindowRef || target.ElementRef != request.Scope.ElementRef {
		return 0, errors.New("fresh complete native observation does not match approved state and scope")
	}
	switch operation {
	case "read_value", "replace":
		if target.InputClass != "fixture_normal_text_field" || target.Role != "AXTextField" || target.Identifier != "textfield" || target.ValueStatus != "included_synthetic_normal" {
			return 0, errors.New("native target is protected, uncertain, or unsupported")
		}
		return policyprobe.TargetTextField, nil
	case "press":
		if target.InputClass != "fixture_button" || target.Role != "AXButton" || target.Identifier != "buttoncounter" {
			return 0, errors.New("native button target is uncertain or unsupported")
		}
		return policyprobe.TargetButton, nil
	default:
		return 0, errors.New("native operation is unsupported")
	}
}

func operationFor(kind policyprobe.ActionKind) (string, error) {
	switch kind {
	case policyprobe.ActionReadValue:
		return "read_value", nil
	case policyprobe.ActionReplaceText:
		return "replace", nil
	case policyprobe.ActionPress:
		return "press", nil
	default:
		return "", fmt.Errorf("unsupported input operation %q", kind.String())
	}
}

type nativeEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	RequestID     string `json:"request_id"`
	ActionID      string `json:"action_id"`
	Action        string `json:"action"`
	Execution     string `json:"execution"`
	Verification  struct {
		Status string `json:"status"`
	} `json:"verification"`
	StateStatus string `json:"state_status"`
	Cleanup     struct {
		Status string `json:"status"`
	} `json:"cleanup"`
	Error  *string         `json:"error"`
	Result json.RawMessage `json:"result"`
}

func validateNativeResponse(response []byte, request Request, operation string) error {
	if len(response) == 0 || len(response) > maxNativeResponse {
		return errors.New("native response size is invalid")
	}
	var envelope nativeEnvelope
	decoder := json.NewDecoder(bytes.NewReader(response))
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("decode native response: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return err
	}
	if envelope.SchemaVersion != "fixture.v0" || envelope.RequestID != request.ActionID ||
		envelope.ActionID != request.ActionID || envelope.Action != operation {
		return errors.New("native response envelope does not match admitted request")
	}
	if envelope.Error != nil {
		if *envelope.Error == "" || len(envelope.Result) != 0 && string(envelope.Result) != "null" {
			return errors.New("native failure response has invalid error/result fields")
		}
	} else if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return errors.New("native success response has no result object")
	}
	switch envelope.Execution {
	case "not_applied", "applied", "partial", "unknown":
	default:
		return errors.New("native response execution state is invalid")
	}
	switch envelope.Verification.Status {
	case "verified", "failed", "unavailable":
	default:
		return errors.New("native response verification state is invalid")
	}
	switch envelope.StateStatus {
	case "available", "partial", "unavailable":
	default:
		return errors.New("native response state status is invalid")
	}
	switch envelope.Cleanup.Status {
	case "released", "not_required", "failed", "unknown":
	default:
		return errors.New("native response cleanup state is invalid")
	}
	if envelope.Execution == "unknown" && envelope.Error == nil {
		return errors.New("unknown native execution requires typed error")
	}
	if envelope.Error != nil && *envelope.Error == "" {
		return errors.New("native error code is empty")
	}
	return nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("unexpected trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func refusal(actionID, code, execution string) []byte {
	return marshalEnvelope(actionID, execution, "unavailable", "unavailable", "not_required", code)
}

func unknown(actionID, code string) []byte {
	return marshalEnvelope(actionID, "unknown", "unavailable", "unavailable", "unknown", code)
}

func marshalEnvelope(actionID, execution, verification, state, cleanup, code string) []byte {
	var responseError any
	if code != "" {
		responseError = code
	}
	payload, _ := json.Marshal(map[string]any{
		"schema_version": "fixture.v0", "ok": code == "", "request_id": actionID,
		"action_id": actionID, "action": "input", "execution": execution,
		"verification": map[string]any{"status": verification}, "state_status": state,
		"cleanup": map[string]any{"status": cleanup}, "error": responseError, "result": nil,
	})
	return payload
}

func marshalReplay(record JournalRecord) []byte {
	if record.Execution == "pending" || record.Execution == "" {
		record.Execution = "unknown"
		if record.ErrorCode == "" {
			record.ErrorCode = "outcome_unknown"
		}
	}
	var replayError any
	if record.ErrorCode != "" {
		replayError = record.ErrorCode
	}
	ok := (record.Execution == "applied" || record.Execution == "partial") && record.ErrorCode == ""
	payload, _ := json.Marshal(map[string]any{
		"schema_version": "fixture.v0", "ok": ok, "request_id": record.ActionID,
		"action_id": record.ActionID, "action": record.Action, "execution": record.Execution,
		"verification": map[string]any{"status": record.Verification}, "state_status": record.StateStatus,
		"cleanup": map[string]any{"status": record.Cleanup}, "error": replayError, "result": nil, "replayed": true,
	})
	return payload
}
