// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func delayedMCPChatFixture(t *testing.T, delay time.Duration) {
	t.Helper()
	stubMCP(t)
	server := sdk.NewServer(&sdk.Implementation{Name: "deadline-fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			t.Error("plain-answer deadline fixture executed a tool")
			return nil, nil
		})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	serverHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			handler.ServeHTTP(w, r)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(serverHTTP.Close)
	if _, err := runMCP(t, "mcp", "add", "fixture", "--url", serverHTTP.URL); err != nil {
		t.Fatal(err)
	}
}

type deadlineMCPChatRunner struct {
	mcpChatRunner
	deadline time.Time
	expire   bool
}

func (r *deadlineMCPChatRunner) RunToolRound(ctx context.Context, model runner.Model, request runner.ToolRoundRequest) (runner.Completion, error) {
	r.deadline, _ = ctx.Deadline()
	if r.expire {
		<-ctx.Done()
	}
	// Deliberately return a late successful completion in the expiry case.
	return r.mcpChatRunner.RunToolRound(ctx, model, request)
}

func TestMCPChatAttachmentDeadlineControls(t *testing.T) {
	for _, continued := range []bool{false, true} {
		for _, mode := range []string{"slow-attach", "valid", "expired-model"} {
			name := mode
			if continued {
				name += "-continued"
			}
			t.Run(name, func(t *testing.T) {
				path := stubMCPChatStore(t)
				delay := 100 * time.Millisecond
				if mode == "slow-attach" {
					delay = 1200 * time.Millisecond
				}
				delayedMCPChatFixture(t, delay)
				args := []string{"--model", "local"}
				var id string
				if continued {
					st, err := store.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					conv, err := st.CreateConversationWithTurn("local", "prior", "prior question", "prior answer", store.RunRecord{})
					st.Close()
					if err != nil {
						t.Fatal(err)
					}
					id = conv.ID
					args = []string{"--continue", id}
				}
				args = append(args, "--mcp", "fixture", "--timeout", "2s", "--json", "Question")
				r := &deadlineMCPChatRunner{mcpChatRunner: mcpChatRunner{replies: []string{"Done."}}, expire: mode == "expired-model"}
				if r.expire && !continued {
					r.replies[0] = `{"tool_calls":[{"id":"late","function":"fixture__read","arguments":{}}]}`
				}
				start := time.Now()
				out, _, err := runMCPChat(t, r, "", args...)
				if mode == "valid" {
					if err != nil || len(r.requests) != 1 {
						t.Fatalf("valid request failed: %v, generations=%d", err, len(r.requests))
					}
				} else {
					wantRounds := 1
					if mode == "slow-attach" {
						wantRounds = 0
					}
					if ExitCode(err) != 7 || len(r.requests) != wantRounds || time.Since(start) > 3*time.Second {
						t.Fatalf("timeout failed: %v, generations=%d, elapsed=%s", err, len(r.requests), time.Since(start))
					}
				}
				if len(r.requests) > 0 && (r.deadline.IsZero() || r.deadline.Sub(start) > 2100*time.Millisecond) {
					t.Fatalf("attachment reset the absolute turn deadline: %s", r.deadline.Sub(start))
				}
				var audit struct {
					Calls []mcpCallAudit `json:"tool_calls"`
				}
				if err := json.Unmarshal([]byte(out), &audit); err != nil || len(audit.Calls) != 0 {
					t.Fatalf("request attempted a tool after expiry: %s", out)
				}
				if continued && mode != "valid" && len(readMCPChatMessages(t, path, id)) != 2 {
					t.Fatal("failed continuation persisted a partial turn")
				}
				if !continued && mode != "valid" {
					st, err := store.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					conversations, err := st.ListConversations(true)
					st.Close()
					if err != nil || len(conversations) != 0 {
						t.Fatalf("failed first turn persisted a conversation: %v", err)
					}
				}
			})
		}
	}
}

type delayedMCPInput struct{ lines int }

func (r *delayedMCPInput) Read(p []byte) (int, error) {
	if r.lines == 2 {
		return 0, io.EOF
	}
	time.Sleep(2200 * time.Millisecond)
	r.lines++
	return copy(p, "Question\n"), nil
}

func TestMCPChatREPLAttachmentAndTurnDeadlines(t *testing.T) {
	for _, slow := range []bool{true, false} {
		t.Run(map[bool]string{true: "startup-timeout", false: "idle-between-valid-turns"}[slow], func(t *testing.T) {
			stubMCPChatStore(t)
			delay := time.Duration(0)
			if slow {
				delay = 1200 * time.Millisecond
			}
			delayedMCPChatFixture(t, delay)
			interactiveStdin = func() bool { return true }
			r := &mcpChatRunner{replies: []string{"First.", "Second."}}
			cmd, flags := newRootCmdWithImageGenerator(func() runner.Runner { return r }, nil)
			input := &delayedMCPInput{}
			var out, stderr bytes.Buffer
			cmd.SetIn(input)
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"chat", "--model", "local", "--mcp", "fixture", "--timeout", "2s"})
			err := executeCommand(cmd, flags)
			if slow {
				if ExitCode(err) != 7 || len(r.requests) != 0 || input.lines != 0 {
					t.Fatalf("REPL startup not bounded: %v, generations=%d, prompts=%d", err, len(r.requests), input.lines)
				}
			} else if err != nil || len(r.requests) != 2 || !strings.Contains(out.String(), "Second.") {
				t.Fatalf("startup deadline leaked into idle REPL or later turn: %v, output=%s", err, out.String())
			}
		})
	}
}

func TestMCPIncrementalResultBudgetStopsBeforeApproval(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       int
		failure    bool
	}{
		{"oversized-first", strings.Repeat("x", 200000), 1, true},
		{"escaped-first", strings.Repeat(`"`, 80000), 1, true},
		{"framed-first", strings.Repeat("<", 30000), 1, true},
		{"aggregate-third", strings.Repeat("x", 60000), 3, true},
		{"valid", strings.Repeat("x", 100), 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubMCP(t)
			url, calls := newLoopServer(t, false, &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: tc.text}}})
			configureLoopServer(t, url, false)
			approval := &loopApprover{allow: true}
			stubLoopApprover(t, approval, true)
			reply := `{"tool_calls":[{"id":"one","function":"fixture__read","arguments":{"page":"one"}},` +
				`{"id":"pending","function":"get_weather","arguments":{"city":"Tokyo"}},` +
				`{"id":"two","function":"fixture__read","arguments":{"page":"two"}},` +
				`{"id":"three","function":"fixture__read","arguments":{"page":"three"}},` +
				`{"id":"four","function":"fixture__read","arguments":{"page":"four"}}]}`
			r := &toolFixtureRunner{replies: []string{reply}}
			out, _, err := runMCPRespond(t, r, "--model", "local", "--mcp", "fixture", "--tools", writeToolsFixture(t, cliTools), "Question")
			if calls.Load() != int64(tc.want) || len(approval.prompts) != tc.want || len(r.requests) != 1 {
				t.Fatalf("dispatch/approval continued: calls=%d approvals=%d rounds=%d err=%v", calls.Load(), len(approval.prompts), len(r.requests), err)
			}
			if tc.failure && errorCode(err) != "tool_result_too_large" || !tc.failure && err != nil {
				t.Fatalf("unexpected result: %v", err)
			}
			var document struct {
				Calls json.RawMessage `json:"tool_calls"`
				MCP   mcpRunMetadata  `json:"mcp"`
			}
			if err := json.Unmarshal([]byte(out), &document); err != nil {
				t.Fatal(err)
			}
			if len(document.MCP.ToolCalls) != tc.want || len(document.MCP.Dispositions) != 5 {
				t.Fatalf("partial audit missing: %s", out)
			}
			for i, d := range document.MCP.Dispositions {
				if i == 1 {
					if d.Source != "caller" || d.State != "pending" || d.Executed {
						t.Fatalf("caller disposition changed: %+v", d)
					}
					continue
				}
				order := i
				if i > 1 {
					order--
				}
				if order < tc.want {
					if d.State != "completed" || !d.Executed {
						t.Fatalf("actual dispatch lost: %+v", d)
					}
				} else if d.State != "not_dispatched" || d.Executed {
					t.Fatalf("later dispatch incorrectly recorded: %+v", d)
				}
			}
			if tc.failure && document.Calls != nil {
				t.Fatal("failure exposed an executable pending-call document")
			}
		})
	}
}
