package comuse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirerun/comuse"
	"github.com/sirerun/comuse/internal/cli"
	comusemcp "github.com/sirerun/comuse/mcp"
)

func TestConditionWaitCLIAndSDKShareOutcomeAndAccounting(t *testing.T) {
	b := newParityMCPActionBackend()
	s, err := comuse.NewSession(comuse.Config{Backend: b, Scope: comuse.Scope{Processes: []comuse.ProcessIdentity{b.process}, ExpiresAt: time.Now().Add(time.Hour)}, Budget: comuse.Budget{MaxDepth: 8, MaxNodes: 64, MaxBytes: 16384, Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if _, err := s.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	obs, err := s.Observe(context.Background(), "window-1")
	if err != nil {
		t.Fatal(err)
	}
	client := parityMCPClient(t, comusemcp.NewServer(s))
	for _, expected := range []bool{true, false} {
		args := map[string]any{"condition": "element_enabled", "window_ref": "window-1", "element_ref": obs.Elements[0].Ref, "state_id": obs.StateID, "expected": expected, "timeout_ms": 1, "poll_interval_ms": 50}
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		var out, diag bytes.Buffer
		code := cli.Run(context.Background(), s, []string{"wait"}, strings.NewReader(string(raw)), &out, &diag)
		if expected && code != 0 || !expected && code == 0 || diag.Len() != 0 {
			t.Fatalf("CLI exit%d output%s", code, out.String())
		}
		mcpResult, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "computer_wait", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if mcpResult.IsError == expected {
			t.Fatalf("MCP error=%v expected=%v", mcpResult.IsError, expected)
		}
		assertSameMCPEnvelope(t, mcpResult)
		var cliEnvelope, mcpEnvelope map[string]any
		if err := json.Unmarshal(out.Bytes(), &cliEnvelope); err != nil {
			t.Fatal(err)
		}
		text := mcpResult.Content[0].(*sdk.TextContent).Text
		if err := json.Unmarshal([]byte(text), &mcpEnvelope); err != nil {
			t.Fatal(err)
		}
		for _, env := range []map[string]any{cliEnvelope, mcpEnvelope} {
			result, ok := env["result"].(map[string]any)
			if !ok {
				t.Fatalf("missing condition result: %#v", env)
			}
			reason := "timeout"
			if expected {
				reason = "satisfied"
			}
			if result["reason"] != reason || result["satisfied"] != expected || env["observation"] != nil {
				t.Fatalf("invalid condition result=%#v", env)
			}
			usage := env["usage"].(map[string]any)
			if usage["actions"] != float64(0) || usage["serialized_text_bytes"].(float64) <= 0 {
				t.Fatalf("bad wait counters=%#v", usage)
			}
		}
	}
}
