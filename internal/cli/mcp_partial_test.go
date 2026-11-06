package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/mcp"
	"github.com/kamenxrider/hollis/internal/runner"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func runPartialCommand(t *testing.T, r runner.Runner, args ...string) (string, string, error) {
	t.Helper()
	cmd, flags := newRootCmdWithImageGenerator(func() runner.Runner { return r }, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	cmd.SetContext(ctx)
	cmd.SetArgs(args)
	cmd.SetIn(strings.NewReader(""))
	var out, diagnostic bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&diagnostic)
	err := executeCommand(cmd, flags)
	if err != nil && !ErrorReported(err) {
		fmt.Fprintln(&diagnostic, err)
	}
	return out.String(), diagnostic.String(), err
}

func TestMCPPartialDefaultHardStop(t *testing.T) {
	for _, mode := range []string{"interactive", "no-tty", "no-input", "json", "agent"} {
		t.Run(mode, func(t *testing.T) {
			state := boundedState(t)
			stubConfigPath(t)
			withNonInteractiveStdin(t)
			stubMCP(t)
			approver := &loopApprover{allow: true}
			stubLoopApprover(t, approver, mode == "interactive")
			text := strings.Repeat("complete documentation line\n", 3000)
			url, calls := newLoopServer(t, false, &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}})
			configureLoopServer(t, url, mode != "interactive")
			call := `{"function":"fixture__read","arguments":{"page":"one"}}`
			r := &boundedFixtureRunner{toolFixtureRunner: toolFixtureRunner{
				replies: []string{`{"tool_calls":[` + call + "," + call + "]}", "Must not answer."},
			}, limit: 10000}
			args := []string{"respond", "--model", "local", "--mcp", "fixture", "Read"}
			switch mode {
			case "no-input":
				args = append(args, "--no-input")
			case "json":
				args = append(args, "--no-input", "--json", "--select", "response")
			case "agent":
				args = append(args, "--agent")
			}
			start := time.Now()
			out, diagnostic, err := runPartialCommand(t, r, args...)
			want := fmt.Sprintf("the result from fixture/read was %d bytes, too big for Local; nothing was truncated. %s", len(text), partialResultsSuggestion)
			if ExitCode(err) != 5 || errorCode(err) != localResultTooLargeCode || err.Error() != want ||
				calls.Load() != 1 || len(r.requests) != 1 || len(r.checks) != 1 || time.Since(start) >= 2*time.Second {
				t.Fatalf("default did not stop cleanly: calls=%d, rounds=%d, error=%v, output=%s", calls.Load(), len(r.requests), err, out)
			}
			if mode == "json" || mode == "agent" {
				var document map[string]any
				if err := json.Unmarshal([]byte(out), &document); err != nil {
					t.Fatal(err)
				}
				failure := document["error"].(map[string]any)
				if failure["code"] != localResultTooLargeCode || failure["exit_code"] != float64(5) ||
					failure["message"] != want || failure["suggestion"] != partialResultsSuggestion {
					t.Fatalf("missing machine-readable failure: %s", out)
				}
			} else if !strings.Contains(diagnostic, want) {
				t.Fatalf("missing hard-stop message: %s", diagnostic)
			}
			if _, err := os.Stat(filepath.Join(state, "tool-results")); !os.IsNotExist(err) {
				t.Fatal("default wrote a full-result file")
			}
			if strings.Contains(out+diagnostic, "tool_results_bounded") || strings.Contains(out+diagnostic, "[partial result:") {
				t.Fatal("default disclosed an excerpt")
			}
		})
	}
}

func TestMCPPartialFlagAndConfigPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, saved string
		flags       []string
		partial     bool
	}{
		{"flag", "", []string{"--partial-results"}, true},
		{"config", "on", nil, true},
		{"flag-off", "on", []string{"--partial-results=false"}, false},
		{"flag-on", "off", []string{"--partial-results"}, true},
		{"config-off", "off", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := boundedState(t)
			stubConfigPath(t)
			withNonInteractiveStdin(t)
			stubMCP(t)
			if tc.saved != "" {
				if _, _, err := runPartialCommand(t, nil, "config", "set", "partial-results", tc.saved); err != nil {
					t.Fatal(err)
				}
			}
			text := strings.Repeat("returned documentation line\n", 3000)
			url, calls := newLoopServer(t, false, &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}})
			configureLoopServer(t, url, true)
			r := &boundedFixtureRunner{toolFixtureRunner: toolFixtureRunner{replies: []string{loopReadCall, "Done."}}, limit: 10000}
			args := append([]string{"respond", "--model", "local", "--mcp", "fixture", "--no-input", "--json", "Read"}, tc.flags...)
			out, diagnostic, err := runPartialCommand(t, r, args...)
			if tc.partial {
				files, _ := filepath.Glob(filepath.Join(state, "tool-results", "*.txt"))
				if err != nil || len(r.requests) != 2 || !strings.Contains(out, `"tool_results_bounded"`) ||
					!strings.Contains(diagnostic, "Local saw the first") || len(files) != 1 ||
					!strings.Contains(framedFixtureOutput(t, r.requests[1]), "[partial result:") {
					t.Fatalf("opt-in failed: %v, output=%s", err, out)
				}
			} else if errorCode(err) != localResultTooLargeCode || len(r.requests) != 1 {
				t.Fatalf("opt-out failed: %v, output=%s", err, out)
			}
			if calls.Load() != 1 {
				t.Fatalf("tool calls=%d, want one", calls.Load())
			}
		})
	}
}

func TestMCPPartialConfigAccess(t *testing.T) {
	stubConfigPath(t)
	withNonInteractiveStdin(t)
	if err := saveConfig(config{DefaultModel: "local", Timeout: "45s"}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"on", "off"} {
		if _, _, err := runPartialCommand(t, nil, "config", "set", "partial-results", value); err != nil {
			t.Fatal(err)
		}
		out, _, err := runPartialCommand(t, nil, "config", "get", "partial-results", "--json")
		if err != nil || !strings.Contains(out, `"value":"`+value+`"`) || !strings.Contains(out, `"configured":true`) {
			t.Fatalf("saved setting unreadable: %s, %v", out, err)
		}
		out, _, err = runPartialCommand(t, nil, "config", "show", "--json", "--select", "partial_results")
		if err != nil || !strings.Contains(out, `"partial_results":`+strconv.FormatBool(value == "on")) {
			t.Fatalf("saved setting absent from show: %s, %v", out, err)
		}
	}
	for _, value := range []string{"true", "yes", ""} {
		if _, _, err := runPartialCommand(t, nil, "config", "set", "partial-results", value); ExitCode(err) != 2 {
			t.Fatal("invalid setting was accepted", err)
		}
	}
	for range 2 {
		if _, _, err := runPartialCommand(t, nil, "config", "unset", "partial-results"); err != nil {
			t.Fatal(err)
		}
	}
	out, _, err := runPartialCommand(t, nil, "config", "get", "partial-results", "--json")
	c, loadErr := loadConfig()
	if err != nil || loadErr != nil || !strings.Contains(out, `"configured":false`) || c.PartialResults != nil ||
		c.DefaultModel != "local" || c.Timeout != "45s" {
		t.Fatalf("unset changed unrelated settings: %s, %+v, %v, %v", out, c, err, loadErr)
	}
}

func TestMCPPartialProcessHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "partial-server" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	_ = os.WriteFile(os.Args[len(os.Args)-1], []byte(strconv.Itoa(os.Getpid())), 0600)
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if decoder.Decode(&request) != nil {
			time.Sleep(time.Hour)
			return
		}
		if len(request.ID) == 0 {
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": "2025-06-18",
				"capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "partial-fixture", "version": "1"}}
		case "tools/list":
			response["result"] = map[string]any{"tools": []any{map[string]any{"name": "read",
				"inputSchema": json.RawMessage(`{"type":"object","properties":{"page":{"type":"string"}},"required":["page"],"additionalProperties":false}`)}}}
		case "tools/call":
			response["result"] = map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Repeat("returned line\n", 6000)}}}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "unsupported"}
		}
		_ = encoder.Encode(response)
	}
}

func TestMCPPartialHardStopCleansStdioProcess(t *testing.T) {
	stubConfigPath(t)
	withNonInteractiveStdin(t)
	stubMCP(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	path, _ := mcpConfigPath()
	if err := mcp.Update(path, func(c *mcp.Config) error {
		c.Servers["fixture"] = mcp.Server{Command: os.Args[0], Args: []string{"-test.run=^TestMCPPartialProcessHelper$", "--", "partial-server", pidFile}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"mcp", "allow", "fixture", "read"}, {"mcp", "mode", "fixture", "auto"}} {
		if _, err := runMCP(t, args...); err != nil {
			t.Fatal(err)
		}
	}
	r := &boundedFixtureRunner{toolFixtureRunner: toolFixtureRunner{replies: []string{loopReadCall}}, limit: 10000}
	out, _, err := runPartialCommand(t, r, "respond", "--model", "local", "--mcp", "fixture", "--no-input", "--json", "Read")
	raw, readErr := os.ReadFile(pidFile)
	pid, parseErr := strconv.Atoi(string(raw))
	if readErr != nil || parseErr != nil || pid <= 0 {
		t.Fatal("missing server process identity", readErr, parseErr)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if ExitCode(err) != 5 || errorCode(err) != localResultTooLargeCode || !strings.Contains(out, `"suggestion"`) {
		t.Fatalf("stdio hard stop failed: %v, %s", err, out)
	}
	if syscall.Kill(pid, 0) != syscall.ESRCH {
		t.Fatal("MCP server survived the hard stop")
	}
}
