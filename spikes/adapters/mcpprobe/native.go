package mcpprobe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sirerun/comuse/spikes/bridgeclient"
	"github.com/sirerun/comuse/spikes/semanticprobe"
)

type NativeClient interface {
	Call(context.Context, []byte) ([]byte, error)
}

type NativeBackend struct {
	client NativeClient
}

var ErrNativeContract = errors.New("native response violated the fixture adapter contract")

func NewNativeBackend(client NativeClient) (*NativeBackend, error) {
	if client == nil {
		return nil, ErrInvalidConfig
	}
	return &NativeBackend{client: client}, nil
}

type nativeScope struct {
	PID          int    `json:"pid"`
	BundleID     string `json:"bundle_id"`
	FixtureNonce string `json:"fixture_nonce"`
}

type nativeRequest struct {
	SchemaVersion int          `json:"schema_version"`
	RequestID     string       `json:"request_id"`
	Operation     string       `json:"op"`
	Scope         *nativeScope `json:"scope,omitempty"`
	WindowRef     string       `json:"window_ref,omitempty"`
	IncludeValues bool         `json:"include_values,omitempty"`
}

type nativeEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Status        string          `json:"status"`
	Result        json.RawMessage `json:"result"`
	Error         json.RawMessage `json:"error"`
}

type nativeError struct{ status, code string }

func (err nativeError) Error() string { return "native " + err.status + " response" }

type nativeDoctor struct {
	Accessibility struct {
		Available bool `json:"available"`
		Prompted  bool `json:"prompted"`
	} `json:"accessibility"`
	EventPosting struct {
		Available bool `json:"available"`
		Prompted  bool `json:"prompted"`
	} `json:"event_posting"`
}

type nativeWindowRow struct {
	Ref  string `json:"ref"`
	Role string `json:"role"`
}

type nativeWindows struct {
	ProcessStartRef string            `json:"process_start_ref"`
	Windows         []nativeWindowRow `json:"windows"`
	Coverage        struct {
		Status      string `json:"status"`
		WindowCount int    `json:"window_count"`
		Truncated   bool   `json:"truncated"`
		TimedOut    bool   `json:"timed_out"`
	} `json:"coverage"`
}

func (backend *NativeBackend) Hello(ctx context.Context, _ Scope) (HelloResult, error) {
	var result string
	if _, err := backend.call(ctx, nativeRequest{Operation: "hello"}, "completed", false, &result); err != nil {
		return HelloResult{}, err
	}
	if result != "hello" {
		return HelloResult{}, errors.New("invalid native hello response")
	}
	return HelloResult{Status: "complete", Message: result}, nil
}

func (backend *NativeBackend) Doctor(ctx context.Context, _ Scope) (DoctorResult, error) {
	var result nativeDoctor
	if _, err := backend.call(ctx, nativeRequest{Operation: "doctor"}, "completed", false, &result); err != nil {
		return DoctorResult{}, err
	}
	if result.Accessibility.Prompted || result.EventPosting.Prompted {
		return DoctorResult{}, ErrNativeContract
	}
	return DoctorResult{Status: "complete", Checks: []Check{
		{Name: "accessibility", Status: available(result.Accessibility.Available), Prompted: result.Accessibility.Prompted},
		{Name: "event_posting", Status: available(result.EventPosting.Available), Prompted: result.EventPosting.Prompted},
	}}, nil
}

func (backend *NativeBackend) Windows(ctx context.Context, scope Scope) (WindowObservation, error) {
	var result nativeWindows
	request := nativeRequest{Operation: "windows", Scope: nativeScopeFrom(scope)}
	envelope, err := backend.call(ctx, request, "completed", true, &result)
	if err != nil {
		var nativeErr nativeError
		if errors.As(err, &nativeErr) && nativeErr.status == "partial" {
			return WindowObservation{Status: "partial"}, nil
		}
		return WindowObservation{}, err
	}
	status := "complete"
	if envelope.Status == "partial" || result.Coverage.Status != "complete" || result.Coverage.Truncated || result.Coverage.TimedOut ||
		result.Coverage.WindowCount != len(result.Windows) || !safeToken(result.ProcessStartRef, 128) {
		status = "partial"
	}
	windows := make([]Window, 0, len(result.Windows))
	for _, row := range result.Windows {
		if row.Role != "window" || !safeToken(row.Ref, 128) {
			return WindowObservation{}, errors.New("invalid native window row")
		}
		windows = append(windows, Window{WindowRef: row.Ref, Role: "AXWindow"})
	}
	if status != "complete" || len(windows) != 1 {
		return WindowObservation{Status: status, Windows: windows}, nil
	}
	return WindowObservation{Status: "complete", ObservationID: envelope.RequestID, ProcessStartRef: result.ProcessStartRef, Windows: windows}, nil
}

func (backend *NativeBackend) Accessibility(ctx context.Context, scope Scope, processStartRef, windowRef string, includeValues bool) (semanticprobe.Snapshot, error) {
	requestID, err := requestID()
	if err != nil {
		return semanticprobe.Snapshot{}, err
	}
	request := nativeRequest{
		SchemaVersion: 1, RequestID: requestID, Operation: "a11y", Scope: nativeScopeFrom(scope),
		WindowRef: windowRef, IncludeValues: includeValues,
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return semanticprobe.Snapshot{}, err
	}
	response, callErr := backend.client.Call(ctx, raw)
	if callErr != nil {
		var nativeErr *bridgeclient.NativeError
		if errors.As(callErr, &nativeErr) && nativeErr.Status == string(bridgeclient.StatusError) {
			_, normalizeErr := semanticprobe.NormalizeEnvelope(response, semanticprobe.ExpectedScope{
				RequestID: requestID, PID: scope.FixturePID, BundleID: scope.FixtureBundleID,
				FixtureNonce: scope.FixtureNonce, ProcessLaunchGeneration: scope.ProcessLaunchGeneration,
				ProcessStartRef: processStartRef, WindowRef: windowRef,
			}, semanticprobe.Options{IncludeSyntheticNormalValue: includeValues})
			if normalizeErr != nil {
				return semanticprobe.Snapshot{}, normalizeErr
			}
		}
		return semanticprobe.Snapshot{}, callErr
	}
	snapshot, err := semanticprobe.NormalizeEnvelope(response, semanticprobe.ExpectedScope{
		RequestID: requestID, PID: scope.FixturePID, BundleID: scope.FixtureBundleID,
		FixtureNonce: scope.FixtureNonce, ProcessLaunchGeneration: scope.ProcessLaunchGeneration,
		ProcessStartRef: processStartRef, WindowRef: windowRef,
	}, semanticprobe.Options{IncludeSyntheticNormalValue: includeValues})
	if err != nil {
		return semanticprobe.Snapshot{}, err
	}
	return snapshot, nil
}

func (backend *NativeBackend) call(ctx context.Context, request nativeRequest, expectedStatus string, allowPartial bool, target any) (nativeEnvelope, error) {
	id, err := requestID()
	if err != nil {
		return nativeEnvelope{}, err
	}
	request.SchemaVersion = 1
	request.RequestID = id
	raw, err := json.Marshal(request)
	if err != nil {
		return nativeEnvelope{}, err
	}
	response, err := backend.client.Call(ctx, raw)
	if err != nil {
		var nativeErr *bridgeclient.NativeError
		if errors.As(err, &nativeErr) {
			return nativeEnvelope{}, nativeError{status: nativeErr.Status, code: safeNativeCode(nativeErr.Detail)}
		}
		return nativeEnvelope{}, err
	}
	var envelope nativeEnvelope
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.SchemaVersion != 1 || envelope.RequestID != id {
		return nativeEnvelope{}, errors.New("invalid native response envelope")
	}
	if envelope.Status != expectedStatus && !(allowPartial && envelope.Status == "partial") {
		code := safeNativeCode(string(envelope.Error))
		if code == "" {
			code = "native_error"
		}
		return nativeEnvelope{}, nativeError{status: envelope.Status, code: code}
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return nativeEnvelope{}, errors.New("native response has no result")
	}
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		return nativeEnvelope{}, fmt.Errorf("invalid native result: %w", err)
	}
	return envelope, nil
}

func nativeScopeFrom(scope Scope) *nativeScope {
	return &nativeScope{PID: scope.FixturePID, BundleID: scope.FixtureBundleID, FixtureNonce: scope.FixtureNonce}
}

func requestID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

func available(value bool) string {
	if value {
		return "available"
	}
	return "unavailable"
}

func nativeCancelled(err error) bool {
	var nativeErr *bridgeclient.NativeError
	if errors.As(err, &nativeErr) {
		return nativeErr.Status == bridgeclient.StatusCancelled || safeNativeCode(nativeErr.Detail) == "cancelled"
	}
	var adapterErr nativeError
	return errors.As(err, &adapterErr) && (adapterErr.status == string(bridgeclient.StatusCancelled) || adapterErr.code == "cancelled")
}

func safeNativeCode(detail string) string {
	var code string
	if err := json.Unmarshal([]byte(detail), &code); err != nil {
		var response struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal([]byte(detail), &response); err != nil {
			return ""
		}
		code = response.Code
	}
	switch code {
	case "cancelled", "permission_denied", "scope_or_permission_denied", "scope_mismatch", "reference_expired", "reference_stale", "reference_unavailable", "reference_limit_exceeded", "fixture_window_unavailable", "windows_unavailable", "request_limit_exceeded", "response_limit_exceeded", "invalid_request", "scope_required", "unsupported_operation":
		return code
	default:
		return ""
	}
}

func nativeErrorCode(err error) string {
	var nativeErr *bridgeclient.NativeError
	if !errors.As(err, &nativeErr) {
		return ""
	}
	return safeNativeCode(nativeErr.Detail)
}
