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
	press := actionTransport(backend.Action{ID: "a1", WindowRef: "w1", ElementRef: "e1", StateID: "s1", Kind: backend.ActionPress})
	actionRequest, err := json.Marshal(nativeRequest{SchemaVersion: 1, RequestID: "r2", Operation: "execute", Action: &press})
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

func TestActionTransportPreservesClosedFieldsAndRequiredZeroValues(t *testing.T) {
	cases := []struct {
		name   string
		action backend.Action
		want   map[string]any
	}{
		{name: "replace empty text", action: backend.Action{ID: "a", WindowRef: "w", ElementRef: "e", StateID: "s", Kind: backend.ActionReplace}, want: map[string]any{"text": ""}},
		{name: "click zero hold and coordinates", action: backend.Action{ID: "a", WindowRef: "w", Kind: backend.ActionClick, Button: "left", Count: 1}, want: map[string]any{"x": float64(0), "y": float64(0), "button": "left", "count": float64(1), "hold_ms": float64(0)}},
		{name: "type delay zero", action: backend.Action{ID: "a", WindowRef: "w", Kind: backend.ActionTypeText, Text: "x"}, want: map[string]any{"text": "x", "delay_ms": float64(0)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(actionTransport(tc.action))
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			for key, value := range tc.want {
				if got[key] != value {
					t.Fatalf("%s = %#v, want %#v", key, got[key], value)
				}
			}
			if len(got) != 5+len(tc.want) {
				t.Fatalf("unexpected fields: %#v", got)
			}
		})
	}
}
