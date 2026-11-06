// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPChatAttachSharesTimeout(t *testing.T) {
	stubMCPChatStore(t)
	stubMCP(t)
	server := sdk.NewServer(&sdk.Implementation{Name: "deadline-fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{
		Name:        "read",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		t.Fatal("plain-answer fixture must not call a tool")
		return nil, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every connection must spend more than the command's one-second budget
		// before initialization. Cancellation still releases the handler promptly.
		timer := time.NewTimer(1200 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer ts.Close()
	if _, err := runMCP(t, "mcp", "add", "fixture", "--url", ts.URL); err != nil {
		t.Fatal(err)
	}
	r := &mcpChatRunner{replies: []string{"Done."}}
	start := time.Now()
	out, _, err := runMCPChat(t, r, "", "--model", "local", "--mcp", "fixture",
		"--timeout", "1s", "--json", "Synthetic question")
	elapsed := time.Since(start)
	t.Logf("elapsed=%s, model_generations=%d, exit=%d, output=%s",
		elapsed, len(r.requests), ExitCode(err), out)
	if ExitCode(err) != 7 || len(r.requests) != 0 {
		t.Fatalf("attachment exceeded --timeout but chat still generated: elapsed=%s, generations=%d, error=%v",
			elapsed, len(r.requests), err)
	}
}

func TestMCPStopAfterUnusableFramedResult(t *testing.T) {
	stubMCP(t)
	url, calls := newLoopServer(t, false, &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: strings.Repeat("x", 200000)}},
	})
	configureLoopServer(t, url, true)
	r := &toolFixtureRunner{replies: []string{`{"tool_calls":[` +
		`{"id":"large","function":"fixture__read","arguments":{"page":"one"}},` +
		`{"id":"pending","function":"get_weather","arguments":{"city":"Tokyo"}},` +
		`{"id":"later","function":"fixture__read","arguments":{"page":"two"}}]}`}}
	out, _, err := runMCPRespond(t, r, "--model", "local", "--mcp", "fixture",
		"--tools", writeToolsFixture(t, cliTools), "Synthetic mixed request")
	t.Logf("tool_dispatches=%d, model_generations=%d, code=%s, output=%s",
		calls.Load(), len(r.requests), errorCode(err), out)
	if errorCode(err) != localResultTooLargeCode || len(r.requests) != 1 {
		t.Fatalf("fixture did not reach the framed-result budget: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("later MCP call executed after first result alone exceeded the 128 KiB transcript limit: dispatches=%d",
			calls.Load())
	}
}
