// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

type combinedContextRetryRunner struct {
	contextFixtureRunner
	summaries, answers int
	summaryFailure     error
	answerFailure      error
	partial            runner.Completion
	deadlines          []time.Time
}

func (r *combinedContextRetryRunner) generate(ctx context.Context, summary bool) (runner.Completion, error) {
	// Avoid real backoff in these deterministic command-level tests.
	if state, _ := ctx.Value(localRetryKey{}).(*localRetry); state != nil {
		state.wait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	}
	deadline, _ := ctx.Deadline()
	r.deadlines = append(r.deadlines, deadline)
	if summary {
		r.summaries++
		return runner.Completion{Text: "Older turns discussed a garden.", Model: runner.ModelLocal}, r.summaryFailure
	}
	r.answers++
	if r.answers == 1 && r.answerFailure != nil {
		return r.partial, r.answerFailure
	}
	return runner.Completion{Text: "cobalt", Model: runner.ModelLocal}, nil
}

func (r *combinedContextRetryRunner) RunGeneration(ctx context.Context, _ runner.Model, request runner.GenerationRequest, _ func(string) error) (runner.Completion, error) {
	return r.generate(ctx, strings.HasPrefix(request.Instructions, "Summarize the older conversation"))
}

func (r *combinedContextRetryRunner) RunComplete(ctx context.Context, _ runner.Model, _ string) (runner.Completion, error) {
	return r.generate(ctx, false)
}

func TestCombinedRetrySummaryQuietAndDisclosure(t *testing.T) {
	for _, command := range []string{"respond", "chat"} {
		for _, mode := range []string{"human", "quiet", "json", "agent", "select"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				stubConfigPath(t)
				t.Setenv("HOLLIS_STATE_DIR", t.TempDir())
				r := &combinedContextRetryRunner{
					contextFixtureRunner: contextFixtureRunner{window: 2000},
					answerFailure:        busyRetryError(false),
				}
				args := []string{command, "--retry=1", "--context-strategy", "summarize",
					"--answer-reserve", "100", "--stream=false"}
				input := ""
				if command == "respond" {
					args = append(args, "--model", "local", "--messages", "-")
					input = string(contextFixtureJSON(t))
				} else {
					st, err := openStore()
					if err != nil {
						t.Fatal(err)
					}
					conv, err := st.CreateConversation("local", "Synthetic conversation")
					if err != nil {
						t.Fatal(err)
					}
					for i := 0; i < 4; i++ {
						if err := st.AppendTurn(conv.ID, strings.Repeat("garden ", 350),
							strings.Repeat("flowers ", 150), store.RunRecord{StartedAt: time.Now()}); err != nil {
							t.Fatal(err)
						}
					}
					st.Close()
					args = append(args, "--continue", conv.ID, "Return only cobalt.")
				}
				switch mode {
				case "quiet":
					args = append(args, "-q")
				case "json":
					args = append(args, "--json", "-q")
				case "agent":
					args = append(args, "--agent", "-q")
				case "select":
					args = append(args, "--json", "-q", "--select", "response,attempts")
				}
				out, notes, err := runContextCommand(t, r, input, args...)
				if err != nil || r.summaries != 1 || r.answers != 2 {
					t.Fatalf("err=%v summaries=%d answers=%d out=%s notes=%s", err, r.summaries, r.answers, out, notes)
				}
				for _, deadline := range r.deadlines {
					if deadline.IsZero() || !deadline.Equal(r.deadlines[0]) {
						t.Fatal("summary and answer attempts must share the original deadline")
					}
				}
				if mode == "human" {
					if !strings.Contains(notes, "older dropped") || !strings.Contains(notes, "retrying in 1 s") {
						t.Fatalf("missing context or retry note: %s", notes)
					}
				} else if notes != "" {
					t.Fatalf("quiet did not hide both notes: %q", notes)
				}
				if mode == "human" || mode == "quiet" {
					if out != "cobalt\n" {
						t.Fatalf("answer=%q", out)
					}
					return
				}
				var result map[string]any
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatal(err)
				}
				if mode == "agent" {
					result = result["results"].(map[string]any)
				}
				meta, ok := result["context"].(map[string]any)
				if !ok || meta["summarized"] != true || meta["dropped_messages"].(float64) <= 0 ||
					result["attempts"] != float64(2) || result["response"] != "cobalt" {
					t.Fatalf("combined disclosure=%s", out)
				}
			})
		}
	}
}

func TestCombinedSummaryIsNotRetriedAndAnswerSafetyStillApplies(t *testing.T) {
	stubConfigPath(t)
	for _, mode := range []string{"summary-busy", "answer-text", "answer-structured", "answer-callback"} {
		t.Run(mode, func(t *testing.T) {
			r := &combinedContextRetryRunner{contextFixtureRunner: contextFixtureRunner{window: 2000}}
			switch mode {
			case "summary-busy":
				r.summaryFailure = busyRetryError(false)
			case "answer-text":
				r.answerFailure, r.partial.Text = busyRetryError(false), "partial"
			case "answer-structured":
				r.answerFailure, r.partial.Structured = busyRetryError(false), json.RawMessage(`{"partial":1}`)
			case "answer-callback":
				r.answerFailure = busyRetryError(true)
			}
			out, notes, err := runContextCommand(t, r, string(contextFixtureJSON(t)), "respond", "-m", "local",
				"--messages", "-", "--context-strategy", "summarize", "--answer-reserve", "100",
				"--retry=3", "--json", "-q")
			wantAnswers := 1
			if mode == "summary-busy" {
				wantAnswers = 0
			}
			if err == nil || r.summaries != 1 || r.answers != wantAnswers || notes != "" {
				t.Fatalf("err=%v summaries=%d answers=%d out=%s notes=%s", err, r.summaries, r.answers, out, notes)
			}
			if mode != "summary-busy" && !strings.Contains(out, `"context":`) {
				t.Fatalf("answer failure lost context disclosure: %s", out)
			}
		})
	}
}

func TestCombinedCommandDiscoveryGoldens(t *testing.T) {
	stubConfigPath(t)
	root := NewRootCmd(func() runner.Runner { t.Fatal("discovery must not construct a runner"); return nil })
	view := struct {
		Commands          []string                      `json:"commands"`
		ConversationFlags map[string][]agentContextFlag `json:"conversation_flags"`
		LocalContract     string                        `json:"local_contract"`
	}{ConversationFlags: map[string][]agentContextFlag{}, LocalContract: buildAgentContext(root).Contracts.Local}
	for _, cmd := range buildAgentContext(root).Commands {
		view.Commands = append(view.Commands, cmd.Name)
		if cmd.Name == "respond" || cmd.Name == "chat" {
			for _, flag := range cmd.Flags {
				switch flag.Name {
				case "retry", "context-strategy", "context-max-turns", "answer-reserve":
					view.ConversationFlags[cmd.Name] = append(view.ConversationFlags[cmd.Name], flag)
				}
			}
		}
	}
	joined := strings.Join(view.Commands, "\n")
	if !strings.Contains(joined, "update") || !strings.Contains(joined, "benchmark") ||
		len(view.ConversationFlags["respond"]) != 4 || len(view.ConversationFlags["chat"]) != 4 {
		t.Fatalf("incomplete combined command discovery: %+v", view)
	}
	raw, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "combined_commands.json", append(raw, '\n'))
	out, err := runGoldenCommand(t, "--help")
	if err != nil || !strings.Contains(string(out), "benchmark") || !strings.Contains(string(out), "update") {
		t.Fatalf("combined root help: %v %s", err, out)
	}
	compareGolden(t, "combined_root_help.txt", out)
}
