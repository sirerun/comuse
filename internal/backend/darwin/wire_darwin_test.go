//go:build darwin && cgo

package darwin

import (
	"encoding/json"
	"testing"

	"github.com/sirerun/comuse/internal/backend"
)

func TestGoWireBudgetAndOmittedPressText(t *testing.T) {
	budget := &backend.Budget{MaxDepth: 8, MaxNodes: 64, MaxBytes: 8192, Timeout: 1_000_000_000}
	request, err := json.Marshal(nativeRequest{SchemaVersion: 1, RequestID: "r1", Operation: "observe",
		WindowRef: "w1", Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatal(err)
	}
	gotBudget := decoded["budget"].(map[string]any)
	if gotBudget["timeout"] != float64(1_000_000_000) {
		t.Fatalf("timeout field mismatch: %#v", gotBudget)
	}
	actionRequest, err := json.Marshal(nativeRequest{SchemaVersion: 1, RequestID: "r2", Operation: "execute",
		Action: &backend.Action{ID: "a1", WindowRef: "w1", ElementRef: "e1", StateID: "s1", Kind: backend.ActionPress}})
	if err != nil {
		t.Fatal(err)
	}
	if json.Valid(actionRequest) == false {
		t.Fatalf("invalid action wire: %s", actionRequest)
	}
	var actionEnvelope map[string]any
	if err := json.Unmarshal(actionRequest, &actionEnvelope); err != nil {
		t.Fatal(err)
	}
	if _, ok := actionEnvelope["action"].(map[string]any)["text"]; ok {
		t.Fatalf("empty press text was not omitted: %s", actionRequest)
	}
}
