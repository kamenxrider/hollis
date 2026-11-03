// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPIdlePromptCancellation(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	if _, err := write.WriteString("partial prompt"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = readMCPPromptLine(ctx, bufio.NewReader(read), read, 1024)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("idle partial input ignored cancellation: %v", err)
	}
}

func TestMCPModelLoopCatalogCountAndCollision(t *testing.T) {
	stubMCP(t)
	server := sdk.NewServer(&sdk.Implementation{Name: "catalog-fixture", Version: "1"}, nil)
	for i := 0; i < 64; i++ {
		server.AddTool(&sdk.Tool{Name: "t" + strconv.Itoa(i), InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				t.Error("catalog-only inspection executed a tool")
				return &sdk.CallToolResult{}, nil
			})
	}
	ts := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	defer ts.Close()
	for _, name := range []string{"first", "second"} {
		if _, err := runMCP(t, "mcp", "add", name, "--url", ts.URL); err != nil {
			t.Fatal(err)
		}
	}
	r := &toolFixtureRunner{replies: []string{"Done."}}
	out, _, err := runMCPRespond(t, r, "--model", "local", "--mcp", "first", "--mcp", "second", "--json", "Inspect")
	if err != nil || len(r.requests) != 1 || len(r.requests[0].Definitions) != 128 {
		t.Fatalf("128-tool catalog was pruned or refused: %s %v", out, err)
	}
	if r.requests[0].Definitions[0].Description != "" {
		t.Fatal("empty server description was fabricated")
	}
	for _, tc := range []struct {
		tools string
		code  string
	}{
		{cliTools, "tool_count_limit"},
		{strings.ReplaceAll(cliTools, "get_weather", "first__t0"), "tool_count_limit"},
	} {
		r := &toolFixtureRunner{}
		_, _, err := runMCPRespond(t, r, "--model", "local", "--mcp", "first", "--mcp", "second",
			"--tools", writeToolsFixture(t, tc.tools), "Inspect")
		if errorCode(err) != tc.code || len(r.requests) != 0 {
			t.Fatalf("union overflow reached model: %v", err)
		}
	}
	r = &toolFixtureRunner{}
	_, _, err = runMCPRespond(t, r, "--model", "local", "--mcp", "first",
		"--tools", writeToolsFixture(t, strings.ReplaceAll(cliTools, "get_weather", "first__t0")), "Inspect")
	if errorCode(err) != "tool_name_conflict" || len(r.requests) != 0 {
		t.Fatalf("name collision reached model: %v", err)
	}
}

func TestMCPModelLoopAdHocIsAskOnly(t *testing.T) {
	stubMCP(t)
	mcpConfigPath = func() (string, error) { t.Fatal("ad-hoc run read config"); return "", nil }
	url, calls := newLoopServer(t, false, nil)
	r := &toolFixtureRunner{replies: []string{strings.ReplaceAll(loopReadCall, "fixture__", "adhoc-1__")}}
	out, _, err := runMCPRespond(t, r, "--model", "local", "--mcp-server", url, "--json", "Read")
	if errorCode(err) != "approval_required" || calls.Load() != 0 || len(r.requests) != 1 ||
		!strings.Contains(out, `"approval":"ask"`) {
		t.Fatalf("ad-hoc approval: %s %v calls=%d", out, err, calls.Load())
	}
}

func TestMCPModelLoopResultErrorAndBudgets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		size    int
		mixed   bool
		isError bool
		code    string
	}{
		{name: "server-error", size: 20, isError: true},
		{name: "single-result-cap", size: (1 << 20) + 1, code: "tool_result_too_large"},
		{name: "aggregate", size: 60000, code: "tool_result_too_large"},
		{name: "mixed-aggregate", size: 60000, mixed: true, code: "tool_result_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubMCP(t)
			url, calls := newLoopServer(t, false, &sdk.CallToolResult{
				IsError: tc.isError, Content: []sdk.Content{&sdk.TextContent{Text: strings.Repeat("x", tc.size)}},
			})
			configureLoopServer(t, url, true)
			reply := loopReadCall
			args := []string{"--model", "local", "--mcp", "fixture", "Read"}
			if tc.size == 60000 {
				call := `{"function":"fixture__read","arguments":{"page":"one"}}`
				reply = `{"tool_calls":[` + call + "," + call + "," + call
				if tc.mixed {
					reply += `,{"function":"get_weather","arguments":{"city":"Tokyo"}}`
				}
				reply += "]}"
			}
			if tc.mixed {
				args = append(args, "--tools", writeToolsFixture(t, cliTools))
			} else {
				args = append(args, "--json")
			}
			r := &toolFixtureRunner{replies: []string{reply, "Done."}}
			out, diagnostics, err := runMCPRespond(t, r, args...)
			if tc.code == "" {
				if err != nil || len(r.requests) != 2 || calls.Load() != 1 || !strings.Contains(out, `"is_error":true`) {
					t.Fatalf("error result was not data: %s %v", out, err)
				}
			} else if errorCode(err) != tc.code || len(r.requests) != 1 {
				t.Fatalf("budget: model=%d %s %v", len(r.requests), out, err)
			}
			if strings.Contains(out+diagnostics, strings.Repeat("x", 20)) {
				t.Fatal("raw tool result leaked")
			}
		})
	}
}

func TestMCPModelLoopCallDeadline(t *testing.T) {
	stubMCP(t)
	var calls atomic.Int64
	release := make(chan struct{})
	server := sdk.NewServer(&sdk.Implementation{Name: "slow-fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "read", Description: "Read slowly.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"page":{"type":"string"}},"required":["page"]}`)},
		func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			calls.Add(1)
			select {
			case <-ctx.Done():
			case <-release:
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "late fixture response"}}}, nil
		})
	ts := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	defer ts.Close()
	defer close(release)
	configureLoopServer(t, ts.URL, true)
	r := &toolFixtureRunner{replies: []string{loopReadCall}}
	start := time.Now()
	out, _, err := runMCPRespond(t, r, "--model", "local", "--mcp", "fixture", "--mcp-timeout", "50ms", "--json", "Read")
	if ExitCode(err) != 7 || len(r.requests) != 1 || calls.Load() != 1 || time.Since(start) > 5*time.Second ||
		!strings.Contains(out, `"executed":true`) {
		t.Fatalf("deadline: %s %v calls=%d", out, err, calls.Load())
	}
}

func TestMCPModelStdioFixture(t *testing.T) {
	if os.Getenv("MCP_LOOP_FIXTURE") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("MCP_LOOP_PID"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(2)
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "stdio-loop-fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "read", Description: "Read fixture.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"page":{"type":"string"}},"required":["page"]}`)},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{StructuredContent: json.RawMessage(`{"n":9007199254740993,"huge":1e400}`)}, nil
		})
	_ = server.Run(context.Background(), &sdk.StdioTransport{})
	os.Exit(0)
}

func TestMCPModelLoopStdioCleanup(t *testing.T) {
	stubMCP(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	if _, err := runMCP(t, "mcp", "add", "fixture", "--command", executable,
		"--arg=-test.run=^TestMCPModelStdioFixture$", "--env", "MCP_LOOP_FIXTURE=1", "--env", "MCP_LOOP_PID="+pidFile); err != nil {
		t.Fatal(err)
	}
	approver := &loopApprover{allow: true}
	stubLoopApprover(t, approver, true)
	r := &toolFixtureRunner{replies: []string{loopReadCall, "Done."}}
	out, _, err := runMCPRespond(t, r, "--model", "local", "--mcp", "fixture", "Read")
	if err != nil || out != "Done.\n" || len(r.requests) != 2 ||
		!strings.Contains(r.requests[1].Prompt, "9007199254740993") || !strings.Contains(r.requests[1].Prompt, "1e400") {
		t.Fatalf("stdio round: %s %v", out, err)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("fixture process remains after command: %v", err)
	}
}

func TestMCPModelLoopNoCallsAfterCanceledContext(t *testing.T) {
	stubMCP(t)
	url, calls := newLoopServer(t, false, nil)
	configureLoopServer(t, url, true)
	cmd, flags := newRootCmdWithImageGenerator(func() runner.Runner { return &toolFixtureRunner{} }, nil)
	options := mcpFlags{names: []string{"fixture"}}
	session, err := options.attach(context.Background(), cmd, flags, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	execution, err := session.ExecuteTools(ctx, []runner.ToolCall{{ID: "cancelled", Name: "fixture__read", Arguments: `{"page":"one"}`}}, func(runner.ToolResult) error {
		t.Fatal("cancelled call produced a result")
		return nil
	})
	if err == nil || calls.Load() != 0 || len(execution.Results) != 0 ||
		len(session.calls) != 1 || session.calls[0].Executed {
		t.Fatalf("cancellation dispatched: %v %#v", err, session.calls)
	}
}
