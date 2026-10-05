package inputprobe

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sirerun/comuse/spikes/internal/hostcap"
	"github.com/sirerun/comuse/spikes/policyprobe"
	"github.com/sirerun/comuse/spikes/semanticprobe"
)

func TestFixtureAcceptanceRejectsUnmintedCapabilityBeforeOpeningRuntime(t *testing.T) {
	config := FixtureAcceptanceConfig{
		Scenario: FixtureReadNormalValue, LibraryPath: "/no/such/library.dylib", PID: 123,
		FixtureNonce: "nonce-1", PrivateJournalRoot: "/tmp/private-journal",
		JournalKey: make([]byte, 32), ActionCommitmentKey: make([]byte, 32),
	}
	_, err := RunFixtureAcceptance(context.Background(), hostcap.Capability{}, config)
	if err == nil || !strings.Contains(err.Error(), "capability") {
		t.Fatalf("unminted capability error = %v", err)
	}
}

func TestFixtureAcceptanceConfigIsClosedAndRequiresCallerKeys(t *testing.T) {
	base := FixtureAcceptanceConfig{
		Scenario: FixtureReadNormalValue, LibraryPath: "/private/bridge.dylib", PID: 123,
		FixtureNonce: "nonce-1", PrivateJournalRoot: "/private/journal",
		JournalKey: make([]byte, 32), ActionCommitmentKey: make([]byte, 32),
	}
	if err := validateFixtureAcceptanceConfig(base); err != nil {
		t.Fatal(err)
	}
	base.Scenario = FixtureScenario("arbitrary_action")
	if err := validateFixtureAcceptanceConfig(base); err == nil {
		t.Fatal("accepted arbitrary scenario")
	}
	base.Scenario = FixtureReadNormalValue
	base.JournalKey = nil
	if err := validateFixtureAcceptanceConfig(base); err == nil {
		t.Fatal("accepted missing durable key")
	}
}

func TestFixedScenarioRequiresPositiveExactNativeTarget(t *testing.T) {
	config := FixtureAcceptanceConfig{Scenario: FixtureReadNormalValue, PID: 123, FixtureNonce: "nonce-1"}
	snapshot := semanticprobe.Snapshot{Status: semanticprobe.SnapshotComplete, NativeStateID: strings.Repeat("a", 64), Elements: []semanticprobe.Element{{Ref: "element", Role: "AXTextField", Identifier: "textfield", ValueStatus: "included_synthetic_normal", Value: stringPointer("synthetic value")}}}
	request, err := fixedScenarioRequest(FixtureReadNormalValue, config, "process", "window", snapshot)
	if err != nil || request.Scope.StateID != snapshot.NativeStateID || request.Action.ElementRef != "element" || request.Action.Kind != policyprobe.ActionReadValue {
		t.Fatalf("fixed request = %+v, err %v", request, err)
	}
	snapshot.Elements[0].Role = "AXSecureTextField"
	if _, err := fixedScenarioRequest(FixtureReadNormalValue, config, "process", "window", snapshot); err == nil {
		t.Fatal("accepted secure or mismatched native target")
	}
	config.Scenario = FixtureReplaceNormalText
	snapshot.Elements[0].Role = "AXTextField"
	request, err = fixedScenarioRequest(FixtureReplaceNormalText, config, "process", "window", snapshot)
	if err != nil || request.Action.Text != "comuse fixture approved replacement" {
		t.Fatalf("fixed replace request = %+v, err %v", request, err)
	}
	if request.Scope.ExpiresAt.Before(time.Now()) {
		t.Fatal("fixture action scope is already expired")
	}
}

func stringPointer(value string) *string { return &value }

func TestFixtureExecutionErrorCannotReportCompleted(t *testing.T) {
	report := FixtureAcceptanceReport{Status: "held"}
	response := []byte(`{"schema_version":1,"request_id":"action-1","execution":"applied","verification":{"status":"verified"},"state_status":"complete","cleanup":{"status":"complete"},"error":null}`)
	err := applyFixtureExecution(&report, response, "action-1", FixtureReplaceNormalText, errors.New("pump failed"))
	if err == nil || report.Status == "completed" {
		t.Fatalf("execution error was accepted: report=%+v err=%v", report, err)
	}
}

func TestFixtureCloseFailureCannotReportCompleted(t *testing.T) {
	report := FixtureAcceptanceReport{Status: "completed", Cleanup: "complete"}
	applyFixtureCloseFailure(&report)
	if report.Status != "held" || report.Cleanup != "unknown" || report.ErrorCode != "native_close_failed" {
		t.Fatalf("close failure report = %+v", report)
	}
}
