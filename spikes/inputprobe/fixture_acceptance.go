package inputprobe

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirerun/comuse/spikes/bridgeclient"
	"github.com/sirerun/comuse/spikes/internal/hostcap"
	"github.com/sirerun/comuse/spikes/policyprobe"
	"github.com/sirerun/comuse/spikes/semanticprobe"
)

const fixtureBundleID = "com.sirerun.comuse.fixture"

type FixtureScenario string

const (
	FixtureReadNormalValue   FixtureScenario = "read_normal_value"
	FixtureReplaceNormalText FixtureScenario = "replace_normal_text"
	FixturePressCounter      FixtureScenario = "press_counter"
)

type FixtureAcceptanceConfig struct {
	Scenario            FixtureScenario
	LibraryPath         string
	PID                 int32
	FixtureNonce        string
	PrivateJournalRoot  string
	JournalKey          []byte
	ActionCommitmentKey []byte
}

type FixtureAcceptanceReport struct {
	SchemaVersion int             `json:"schema_version"`
	Scenario      FixtureScenario `json:"scenario"`
	Status        string          `json:"status"`
	Execution     string          `json:"execution"`
	Verification  string          `json:"verification"`
	StateStatus   string          `json:"state_status"`
	Cleanup       string          `json:"cleanup"`
	ErrorCode     string          `json:"error_code,omitempty"`
}

type fixtureWindowEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Status        string          `json:"status"`
	Error         json.RawMessage `json:"error"`
	Result        struct {
		ProcessStartRef string `json:"process_start_ref"`
		Windows         []struct {
			Ref string `json:"ref"`
		} `json:"windows"`
		Coverage struct {
			Status    string `json:"status"`
			Truncated bool   `json:"truncated"`
			TimedOut  bool   `json:"timed_out"`
		} `json:"coverage"`
	} `json:"result"`
}

// RunFixtureAcceptance is a fixed-scenario harness entrypoint. It is not a
// generic input API; callers cannot provide action JSON, arbitrary text,
// approval material, or a backend implementation.
func RunFixtureAcceptance(ctx context.Context, capability hostcap.Capability, config FixtureAcceptanceConfig) (report FixtureAcceptanceReport, err error) {
	report = FixtureAcceptanceReport{SchemaVersion: 1, Scenario: config.Scenario, Status: "held", Execution: "unknown", Verification: "unavailable", StateStatus: "unavailable", Cleanup: "unknown"}
	if !hostcap.Valid(capability) {
		return report, errors.New("trusted fixture host capability is unavailable")
	}
	if ctx == nil {
		return report, errors.New("fixture acceptance requires a context")
	}
	if err := validateFixtureAcceptanceConfig(config); err != nil {
		return report, err
	}
	client, err := bridgeclient.Open(config.LibraryPath)
	if err != nil {
		report.ErrorCode = "runtime_open_failed"
		return report, errors.New("fixture runtime could not be opened")
	}
	backend, err := NewBridgeBackend(client)
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Close(closeCtx)
		return report, errors.New("fixture native backend could not be created")
	}
	gate, authority, err := policyprobe.New(policyprobe.Clock(time.Now))
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Close(closeCtx)
		return report, errors.New("fixture host policy could not be initialized")
	}
	host, err := newDesktopInputHost(gate, config.PrivateJournalRoot, config.JournalKey, config.ActionCommitmentKey, backend, time.Now)
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Close(closeCtx)
		return report, errors.New("fixture input composition could not be initialized")
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if closeErr := host.closeAfterNativeDrain(closeCtx); closeErr != nil {
			applyFixtureCloseFailure(&report)
			if err == nil {
				err = errors.New("fixture host did not close cleanly")
			}
		}
	}()

	requestBase := "fixture-" + string(config.Scenario) + "-" + config.FixtureNonce
	windowRef, processStartRef, err := fixtureWindow(ctx, client, config, requestBase+"-windows")
	if err != nil {
		report.ErrorCode = "fixture_window_unavailable"
		return report, err
	}
	a11yRequestID := requestBase + "-a11y"
	a11yData, err := callAndPump(ctx, client, map[string]any{
		"schema_version": 1, "request_id": a11yRequestID, "op": "a11y", "include_values": true,
		"scope": map[string]any{"pid": config.PID, "bundle_id": fixtureBundleID, "fixture_nonce": config.FixtureNonce, "window_ref": windowRef},
	})
	if err != nil {
		report.ErrorCode = "observation_unavailable"
		return report, errors.New("fixture observation failed")
	}
	expected := semanticprobe.ExpectedScope{
		RequestID: a11yRequestID, PID: config.PID, BundleID: fixtureBundleID,
		FixtureNonce: config.FixtureNonce, ProcessLaunchGeneration: processStartRef,
		ProcessStartRef: processStartRef, WindowRef: windowRef,
	}
	snapshot, err := semanticprobe.NormalizeEnvelope(a11yData, expected, semanticprobe.Options{IncludeSyntheticNormalValue: true})
	if err != nil || snapshot.Status != semanticprobe.SnapshotComplete || snapshot.NativeStateID == "" {
		report.ErrorCode = "observation_incomplete"
		return report, errors.New("fixture observation was incomplete or mismatched")
	}
	request, err := fixedScenarioRequest(config.Scenario, config, processStartRef, windowRef, snapshot)
	if err != nil {
		report.ErrorCode = "scenario_target_unavailable"
		return report, err
	}
	operation, err := operationFor(request.Action.Kind)
	if err != nil {
		report.ErrorCode = "scenario_target_unavailable"
		return report, errors.New("fixed fixture action is unsupported")
	}
	var classification NativeClassification
	inspectScope := NativeTargetRequest{
		PID: config.PID, BundleID: fixtureBundleID, FixtureNonce: config.FixtureNonce,
		ProcessStartRef: processStartRef, WindowRef: windowRef, ElementRef: request.Scope.ElementRef,
	}
	_, err = runWorkerAndPump(ctx, client, func(callCtx context.Context) ([]byte, error) {
		var inspectErr error
		classification, inspectErr = backend.Inspect(callCtx, inspectScope)
		return nil, inspectErr
	})
	if err != nil {
		report.ErrorCode = "scenario_target_unavailable"
		return report, errors.New("fresh fixture target classification failed")
	}
	classifiedTarget, err := classifyNativeTarget(request, operation, classification)
	if err != nil || classifiedTarget != request.Action.Target {
		report.ErrorCode = "protected_or_unsupported_target"
		return report, errors.New("fixed fixture target was not positively classified")
	}
	challenge, err := authority.Issue(request.Scope, request.Action, time.Minute)
	if err != nil {
		report.ErrorCode = "scenario_approval_failed"
		return report, errors.New("fixed fixture scenario approval could not be prepared")
	}
	request.Approval, err = authority.ApproveHost(challenge.ID)
	if err != nil {
		report.ErrorCode = "scenario_approval_failed"
		return report, errors.New("fixed fixture scenario approval could not be prepared")
	}

	executionData, executionErr := runWorkerAndPump(ctx, client, func(callCtx context.Context) ([]byte, error) {
		return host.execute(callCtx, request)
	})
	if err := applyFixtureExecution(&report, executionData, request.ActionID, config.Scenario, executionErr); err != nil {
		return report, err
	}
	return report, nil
}

func applyFixtureExecution(report *FixtureAcceptanceReport, data []byte, requestID string, scenario FixtureScenario, executionErr error) error {
	var envelope nativeEnvelope
	if report == nil || len(data) == 0 || json.Unmarshal(data, &envelope) != nil || envelope.RequestID != requestID {
		if report != nil {
			report.Status, report.ErrorCode = "held", "backend_outcome_invalid"
		}
		return errors.New("fixture input result is missing or mismatched")
	}
	report.Execution = envelope.Execution
	report.Verification = envelope.Verification.Status
	report.StateStatus = envelope.StateStatus
	report.Cleanup = envelope.Cleanup.Status
	if envelope.Error != nil && safeErrorCode(*envelope.Error) {
		report.ErrorCode = *envelope.Error
	}
	if executionErr != nil {
		report.Status = "held"
		if report.ErrorCode == "" {
			report.ErrorCode = "native_input_unavailable"
		}
		return errors.New("fixture input did not complete cleanly")
	}
	readOnlyComplete := scenario == FixtureReadNormalValue && envelope.Execution == "not_applied"
	if (envelope.Execution == "applied" || readOnlyComplete) && envelope.Error == nil && envelope.Verification.Status == "verified" && envelope.Cleanup.Status != "failed" && envelope.Cleanup.Status != "unknown" {
		report.Status = "completed"
	} else if envelope.Execution == "partial" {
		report.Status = "partial"
	} else {
		report.Status = "held"
	}
	if report.Status == "held" && report.ErrorCode == "" {
		report.ErrorCode = "outcome_unknown"
	}
	return nil
}

func applyFixtureCloseFailure(report *FixtureAcceptanceReport) {
	if report == nil {
		return
	}
	report.Status = "held"
	report.Cleanup = "unknown"
	if report.ErrorCode == "" {
		report.ErrorCode = "native_close_failed"
	}
}

func validateFixtureAcceptanceConfig(config FixtureAcceptanceConfig) error {
	switch config.Scenario {
	case FixtureReadNormalValue, FixtureReplaceNormalText, FixturePressCounter:
	default:
		return errors.New("fixture scenario is unsupported")
	}
	if !filepath.IsAbs(config.LibraryPath) || !filepath.IsAbs(config.PrivateJournalRoot) || config.PID <= 0 || !validFixtureNonce(config.FixtureNonce) || len(config.JournalKey) != 32 || len(config.ActionCommitmentKey) != 32 {
		return errors.New("fixture config requires absolute trusted paths, exact fixture scope, and two 32-byte caller-owned keys")
	}
	return nil
}

func validFixtureNonce(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		if char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func fixtureWindow(ctx context.Context, client *bridgeclient.Client, config FixtureAcceptanceConfig, requestID string) (string, string, error) {
	data, err := callAndPump(ctx, client, map[string]any{
		"schema_version": 1, "request_id": requestID, "op": "windows",
		"scope": map[string]any{"pid": config.PID, "bundle_id": fixtureBundleID, "fixture_nonce": config.FixtureNonce},
	})
	if err != nil {
		return "", "", errors.New("fixture windows observation failed")
	}
	var response fixtureWindowEnvelope
	if err := json.Unmarshal(data, &response); err != nil || response.SchemaVersion != 1 || response.RequestID != requestID || response.Status != "completed" || string(response.Error) != "null" || response.Result.ProcessStartRef == "" || response.Result.Coverage.Status != "complete" || response.Result.Coverage.Truncated || response.Result.Coverage.TimedOut || len(response.Result.Windows) != 1 || response.Result.Windows[0].Ref == "" {
		return "", "", errors.New("fixture window identity is missing, ambiguous, partial, or mismatched")
	}
	return response.Result.Windows[0].Ref, response.Result.ProcessStartRef, nil
}

func callAndPump(ctx context.Context, client *bridgeclient.Client, request any) ([]byte, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return nil, errors.New("fixture native request could not be encoded")
	}
	return runWorkerAndPump(ctx, client, func(callCtx context.Context) ([]byte, error) {
		return client.Call(callCtx, data)
	})
}

func runWorkerAndPump(ctx context.Context, client *bridgeclient.Client, work func(context.Context) ([]byte, error)) ([]byte, error) {
	callCtx, cancelCall := context.WithTimeout(ctx, 10*time.Second)
	defer cancelCall()
	type result struct {
		data []byte
		err  error
	}
	finished := make(chan result, 1)
	go func() {
		response, callErr := work(callCtx)
		finished <- result{data: response, err: callErr}
	}()
	var cancelDeadline time.Time
	var cancelCause error
	for {
		select {
		case completed := <-finished:
			cancelCall()
			return completed.data, errors.Join(completed.err, cancelCause)
		default:
		}
		if err := client.Pump(10 * time.Millisecond); err != nil {
			cancelCause = errors.Join(cancelCause, err)
			if cancelDeadline.IsZero() {
				cancelCall()
				cancelDeadline = time.Now().Add(2 * time.Second)
			}
		}
		if err := callCtx.Err(); err != nil && cancelDeadline.IsZero() {
			cancelCause = errors.Join(cancelCause, err)
			cancelCall()
			cancelDeadline = time.Now().Add(2 * time.Second)
		}
		if !cancelDeadline.IsZero() && time.Now().After(cancelDeadline) {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
			closeErr := client.Close(closeCtx)
			closeCancel()
			if closeErr != nil {
				return nil, errors.Join(cancelCause, closeErr)
			}
			select {
			case completed := <-finished:
				return completed.data, errors.Join(completed.err, cancelCause)
			case <-time.After(time.Second):
				return nil, errors.Join(cancelCause, errors.New("native worker did not return after drained close"))
			}
		}
	}
}

func fixedScenarioRequest(scenario FixtureScenario, config FixtureAcceptanceConfig, processStartRef, windowRef string, snapshot semanticprobe.Snapshot) (Request, error) {
	targetID, targetRole := "textfield", "AXTextField"
	kind, target := policyprobe.ActionReadValue, policyprobe.TargetTextField
	text := ""
	switch scenario {
	case FixtureReadNormalValue:
	case FixtureReplaceNormalText:
		kind, text = policyprobe.ActionReplaceText, "comuse fixture approved replacement"
	case FixturePressCounter:
		targetID, targetRole = "buttoncounter", "AXButton"
		kind, target = policyprobe.ActionPress, policyprobe.TargetButton
	default:
		return Request{}, errors.New("fixture scenario is unsupported")
	}
	var element *semanticprobe.Element
	for index := range snapshot.Elements {
		candidate := &snapshot.Elements[index]
		if candidate.Identifier == targetID && candidate.Role == targetRole {
			element = candidate
			break
		}
	}
	if element == nil || element.Ref == "" {
		return Request{}, errors.New("fixed fixture target was not positively classified")
	}
	if target == policyprobe.TargetTextField && (element.ValueStatus != "included_synthetic_normal" || element.Value == nil) {
		return Request{}, errors.New("fixed fixture text field did not have an allowed synthetic value")
	}
	actionID := "fixture:" + strings.ReplaceAll(string(scenario), "_", "-") + ":" + config.FixtureNonce
	scope := policyprobe.Scope{
		PrincipalID: "controlled_fixture_operator", SessionID: "fixture_acceptance",
		Process:      policyprobe.ProcessIdentity{PID: config.PID, BundleID: fixtureBundleID, LaunchGeneration: processStartRef},
		FixtureNonce: config.FixtureNonce, StateID: snapshot.NativeStateID,
		WindowRef: windowRef, WindowTitle: "Comuse Fixture " + config.FixtureNonce,
		ElementRef: element.Ref, PolicyVersion: policyprobe.PolicyVersion, ExpiresAt: time.Now().Add(time.Minute),
	}
	action := policyprobe.Action{Kind: kind, Target: target, ElementRef: element.Ref, Text: text}
	return Request{ActionID: actionID, Scope: scope, Action: action}, nil
}

func (backend *BridgeBackend) CloseAndDrain(ctx context.Context) error {
	if backend == nil || backend.client == nil {
		return errors.New("native bridge client is unavailable")
	}
	return backend.client.Close(ctx)
}
