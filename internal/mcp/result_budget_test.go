package mcp

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResultBudgetPreservesCompleteContent(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			flag := ""
			if enabled {
				flag = "1"
			}
			t.Setenv("HOLLIS_DEBUG_BUDGET", flag)
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			text := "RESULT_CONTENT_CANARY" + strings.Repeat("x", 28072)
			structured := map[string]any{"excerpt": strings.Repeat("z", 27479)}
			result, err := mapResult("SERVER_CANARY", "TOOL_CANARY", &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{Text: text}}, StructuredContent: structured,
			})
			if err != nil || !strings.HasPrefix(result.Text, text+"\n"+structuredLabel+"\n") ||
				!strings.Contains(result.Text, structured["excerpt"].(string)) || result.Bytes != len(result.Text) {
				t.Fatal("full result was changed or lost", err)
			}
			framer, err := NewFramer()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(framer.Frame("SERVER_CANARY", "TOOL_CANARY", result), result.Text) {
				t.Fatal("framing shortened the result")
			}
			for _, private := range []string{"CANARY", "excerpt", framer.Nonce()} {
				if strings.Contains(logs.String(), private) {
					t.Fatal("content or identity reached diagnostics")
				}
			}
			if enabled != strings.Contains(logs.String(), "mcp_result_budget") ||
				enabled != strings.Contains(logs.String(), "mcp_frame_budget") {
				t.Fatal("diagnostics were not opt-in")
			}
		})
	}
}
