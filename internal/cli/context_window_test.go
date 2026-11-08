// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

type contextFixtureRunner struct {
	recordingRunner
	window      int
	counts      int
	generations []runner.GenerationRequest
	countErr    error
	summaryErr  error
	finalErr    error
}

func (r *contextFixtureRunner) CountTokens(_ context.Context, request runner.GenerationRequest, reserve int) (runner.TokenCount, error) {
	r.counts++
	n := len(strings.Fields(request.Prompt)) + len(strings.Fields(request.Instructions))
	for _, m := range request.History {
		n += len(strings.Fields(m.Content))
	}
	return runner.TokenCount{Tokens: n, ContextSize: r.window, AnswerReserve: reserve, Fits: n+reserve <= r.window}, r.countErr
}

func (r *contextFixtureRunner) RunGeneration(_ context.Context, model runner.Model, request runner.GenerationRequest, _ func(string) error) (runner.Completion, error) {
	r.generations = append(r.generations, request)
	if strings.HasPrefix(request.Instructions, "Summarize the older conversation") {
		return runner.Completion{Text: "Older turns discussed a garden.", Model: model}, r.summaryErr
	}
	return runner.Completion{Text: "cobalt", Model: model}, r.finalErr
}

func contextFixtureMessages() []store.Message {
	ms := []store.Message{{Role: "system", Content: "Keep this instruction."}}
	for i := 0; i < 4; i++ {
		ms = append(ms, store.Message{Role: "user", Content: strings.Repeat("garden ", 350)},
			store.Message{Role: "assistant", Content: strings.Repeat("flowers ", 150)})
	}
	return append(ms, store.Message{Role: "user", Content: "The secret word is cobalt. Return only the secret word."})
}

func contextFixtureJSON(t *testing.T) []byte {
	t.Helper()
	var messages []map[string]string
	for _, m := range contextFixtureMessages() {
		messages = append(messages, map[string]string{"role": m.Role, "content": m.Content})
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func policyContext(strategy string, turns, reserve int, notes *bytes.Buffer) context.Context {
	return context.WithValue(context.Background(), contextPolicyKey{}, contextPolicy{
		strategy: strategy, maxTurns: turns, reserve: reserve, notes: notes,
	})
}

func TestContextStrategiesPreserveInstructionsAndOriginalMessages(t *testing.T) {
	for _, strategy := range []string{"strict", "drop-oldest", "sliding-window", "summarize"} {
		t.Run(strategy, func(t *testing.T) {
			ms := contextFixtureMessages()
			original := append([]store.Message(nil), ms...)
			r := &contextFixtureRunner{window: 2000}
			var notes bytes.Buffer
			selected, meta, err := prepareRespondContext(policyContext(strategy, 2, 100, &notes), r, runner.ModelLocal, ms, "Explicit instruction.")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ms, original) {
				t.Fatal("caller history was changed")
			}
			if strategy == "strict" {
				if meta != nil || r.counts != 0 || len(selected) != len(ms) || notes.Len() != 0 {
					t.Fatal("strict changed existing behavior")
				}
				return
			}
			if meta.DroppedMessages == 0 || meta.SentMessages+meta.DroppedMessages != len(ms) {
				t.Fatalf("missing disclosure: %+v", meta)
			}
			if !strings.Contains(notes.String(), "older dropped") || !strings.Contains(notes.String(), "Local's 2,000-token window") {
				t.Fatalf("missing note: %s", &notes)
			}
			if selected[len(selected)-1] != ms[len(ms)-1] {
				t.Fatal("newest user message changed")
			}
			foundInstruction := false
			for _, m := range selected {
				foundInstruction = foundInstruction || m == ms[0]
			}
			if !foundInstruction {
				t.Fatal("system instruction dropped")
			}
			if strategy == "sliding-window" && len(selected) != 4 {
				t.Fatalf("two turns should retain instruction, one complete pair, current user: %d", len(selected))
			}
			if strategy == "summarize" {
				if !meta.Summarized || len(r.generations) != 1 || !strings.HasPrefix(selected[0].Content, contextSummaryLabel) ||
					!strings.Contains(notes.String(), "summary costs one extra Local model call") {
					t.Fatalf("summary missing: %+v %s", meta, &notes)
				}
				if len(r.generations[0].Prompt) == 0 || r.generations[0].Options.MaxTokens == nil || *r.generations[0].Options.MaxTokens != 256 {
					t.Fatal("summary must have a bounded output")
				}
			} else if len(r.generations) != 0 || meta.Summarized {
				t.Fatal("non-summary strategy generated a summary")
			}
		})
	}
}

func TestContextNothingDroppedAndCapacityFailures(t *testing.T) {
	for _, strategy := range []string{"drop-oldest", "sliding-window", "summarize"} {
		r := &contextFixtureRunner{window: 8192}
		var notes bytes.Buffer
		ms := []store.Message{{Role: "user", Content: "Hello"}}
		_, meta, err := prepareRespondContext(policyContext(strategy, 1, 1024, &notes), r, runner.ModelLocal, ms, "")
		if err != nil || meta.DroppedMessages != 0 || meta.Summarized || notes.Len() != 0 || len(r.generations) != 0 {
			t.Fatalf("%s meta=%+v err=%v notes=%s", strategy, meta, err, &notes)
		}
		r.window = 10
		_, _, err = prepareRespondContext(policyContext(strategy, 1, 1024, &notes), r, runner.ModelLocal, ms, "")
		if errorCode(err) != "context_capacity" || len(r.generations) != 0 {
			t.Fatalf("%s: %v", strategy, err)
		}
	}
	// Sliding-window is a turn cap, not a second drop-to-fit strategy.
	r := &contextFixtureRunner{window: 300}
	_, _, err := prepareRespondContext(policyContext("sliding-window", 2, 0, &bytes.Buffer{}), r, runner.ModelLocal, contextFixtureMessages(), "")
	if errorCode(err) != "context_capacity" {
		t.Fatalf("oversized sliding window was silently shortened: %v", err)
	}
}

func TestContextRouteRulesAndByteLimits(t *testing.T) {
	for _, model := range []runner.Model{runner.ModelAuto, runner.ModelCloud, runner.ModelCloudPro, runner.ModelOnDevice, runner.ModelChatGPT} {
		r := &contextFixtureRunner{window: 8192}
		_, _, err := prepareRespondContext(policyContext("summarize", 2, 1024, &bytes.Buffer{}), r, model, contextFixtureMessages(), "")
		if ExitCode(err) != 2 || r.counts != 0 || len(r.generations) != 0 {
			t.Fatalf("model=%s err=%v", model, err)
		}
		ms := []store.Message{{Role: "system", Content: "Preserve."},
			{Role: "user", Content: strings.Repeat("x", chat.MaxRenderedPromptBytes-80)},
			{Role: "assistant", Content: "old"}, {Role: "user", Content: "cobalt"}}
		selected, meta, err := prepareRespondContext(policyContext("drop-oldest", 2, 1024, &bytes.Buffer{}), r, model, ms, "")
		if err != nil || len(selected) != 2 || meta.DroppedMessages != 2 || r.counts != 0 {
			t.Fatalf("model=%s selected=%d meta=%+v err=%v", model, len(selected), meta, err)
		}
		_, _, err = prepareRespondContext(policyContext("drop-oldest", 2, 1024, &bytes.Buffer{}), r, model,
			[]store.Message{{Role: "user", Content: strings.Repeat("x", chat.MaxRenderedPromptBytes)}}, "")
		if errorCode(err) != "context_capacity" {
			t.Fatalf("oversized newest turn: %v", err)
		}
	}
}

func TestShortcutInstructionsAfterConversation(t *testing.T) {
	stubConfigPath(t)
	const instructions = "Reply with exactly READY."
	const input = `[{"role":"system","content":"Preserve context."},{"role":"user","content":"Old question"},{"role":"assistant","content":"Old answer"},{"role":"user","content":"Say hello."}]`
	for _, model := range []string{"cloud", "cloud-pro", "on-device", "chatgpt"} {
		for _, shorten := range []bool{false, true} {
			r := &contextFixtureRunner{}
			args := []string{"respond", "-m", model, "--messages", "-", "-s", instructions}
			history := []store.Message{{Role: "system", Content: "Preserve context."}}
			if shorten {
				args = append(args, "--context-strategy", "sliding-window", "--context-max-turns", "1")
			} else {
				history = append(history, store.Message{Role: "user", Content: "Old question"}, store.Message{Role: "assistant", Content: "Old answer"})
			}
			_, _, err := runContextCommand(t, r, input, args...)
			want := "Prompt:\n" + chat.RenderTranscript(history, "Say hello.") + "\n\nInstructions:\n" + instructions
			if err != nil || r.calls != 1 || len(r.prompts) != 1 || r.prompts[0] != want || r.counts != 0 {
				t.Fatalf("model=%s shorten=%t err=%v prompts=%q", model, shorten, err, r.prompts)
			}
		}
	}
}

func TestContextCounterAndSummaryFailuresStop(t *testing.T) {
	for _, summaryFailure := range []bool{false, true} {
		r := &contextFixtureRunner{window: 2000}
		if summaryFailure {
			r.summaryErr = &runner.Error{Kind: runner.KindContentRefused}
		} else {
			r.countErr = &runner.Error{Kind: runner.KindNativeProtocol}
		}
		_, _, err := prepareRespondContext(policyContext("summarize", 2, 100, &bytes.Buffer{}), r, runner.ModelLocal, contextFixtureMessages(), "")
		if err == nil || (!summaryFailure && len(r.generations) != 0) || (summaryFailure && len(r.generations) != 1) {
			t.Fatalf("failure=%t err=%v generations=%d", summaryFailure, err, len(r.generations))
		}
	}
	r := &contextFixtureRunner{window: 2000}
	ms := contextFixtureMessages()
	ms = append(append([]store.Message(nil), ms[:9]...), ms[1:9]...)
	ms = append(ms, store.Message{Role: "user", Content: "cobalt"})
	_, _, err := prepareRespondContext(policyContext("summarize", 2, 100, &bytes.Buffer{}), r, runner.ModelLocal, ms, "")
	if errorCode(err) != "context_capacity" || len(r.generations) != 0 {
		t.Fatalf("oversized summary source must not be truncated or recursively summarized: %v", err)
	}
}

func runContextCommand(t *testing.T, r runner.Runner, input string, args ...string) (string, string, error) {
	t.Helper()
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
	var out, notes bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&notes)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(args)
	err := executeCommand(cmd, flags)
	return out.String(), notes.String(), err
}

func TestContextNotesAndJSONDisclosure(t *testing.T) {
	stubConfigPath(t)
	raw := contextFixtureJSON(t)
	for _, output := range []string{"human", "quiet", "json", "agent", "select"} {
		r := &contextFixtureRunner{window: 2000}
		args := []string{"respond", "--model", "local", "--messages", "-", "--context-strategy", "sliding-window", "--context-max-turns", "2", "--answer-reserve", "100"}
		switch output {
		case "quiet":
			args = append(args, "-q")
		case "json":
			args = append(args, "--json", "-q")
		case "agent":
			args = append(args, "--agent")
		case "select":
			args = append(args, "--json", "--select", "response")
		}
		out, notes, err := runContextCommand(t, r, string(raw), args...)
		if err != nil || len(r.generations) != 1 {
			t.Fatalf("%s: %v %s", output, err, out)
		}
		if output == "human" {
			want := "hollis: context: sent the newest 4 of 10 messages (6 older dropped) with sliding-window to fit Local's 2,000-token window\n"
			if notes != want || out != "cobalt\n" {
				t.Fatalf("out=%q notes=%q", out, notes)
			}
		} else if notes != "" {
			t.Fatalf("%s unexpectedly printed human notes: %q", output, notes)
		}
		if output == "json" || output == "agent" || output == "select" {
			var envelope map[string]any
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatal(err)
			}
			if output == "agent" {
				envelope = envelope["results"].(map[string]any)
			}
			meta := envelope["context"].(map[string]any)
			if meta["strategy"] != "sliding-window" || meta["dropped_messages"] != float64(6) || meta["sent_messages"] != float64(4) || meta["summarized"] != false {
				t.Fatalf("%s missing disclosure: %s", output, out)
			}
		}
	}
}

func TestContextStoredChatIsAppendOnly(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	conv, err := st.CreateConversation("local", "Long chat")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := st.AppendTurn(conv.ID, strings.Repeat("garden ", 350), strings.Repeat("flowers ", 150), store.RunRecord{StartedAt: time.Now(), ModelRequested: "local", ModelUsed: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	original, _ := st.Messages(conv.ID)
	for _, strategy := range []string{"drop-oldest", "sliding-window", "summarize"} {
		r := &contextFixtureRunner{window: 2000, recordingRunner: recordingRunner{response: "cobalt"}}
		var notes bytes.Buffer
		ctx := policyContext(strategy, 2, 100, &notes)
		result, err := runTurnResult(ctx, st, conv, "cobalt", func() runner.Runner { return r }, 0)
		if err != nil || result.Context == nil || result.Context.DroppedMessages == 0 {
			t.Fatalf("%s: %v %+v", strategy, err, result.Context)
		}
		saved, _ := st.Messages(conv.ID)
		if !reflect.DeepEqual(saved[:len(original)], original) || len(saved) != len(original)+2 {
			t.Fatal("old messages were changed or dropped")
		}
		for _, m := range saved {
			if strings.HasPrefix(m.Content, contextSummaryLabel) {
				t.Fatal("transient summary was persisted")
			}
		}
		original = saved
		cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := printChatJSON(cmd, result, conv, flags); err != nil || !strings.Contains(out.String(), `"context":`) {
			t.Fatalf("chat disclosure missing: %v %s", err, &out)
		}
	}
}

func TestContextFlagsConfigAndHelp(t *testing.T) {
	stubConfigPath(t)
	t.Setenv("HOLLIS_STATE_DIR", t.TempDir())
	for _, args := range [][]string{
		{"config", "set", "context-strategy", "sliding-window"},
		{"config", "get", "context-strategy", "--json"},
		{"config", "show", "--json", "--select", "context_strategy"},
	} {
		if _, _, err := runContextCommand(t, &contextFixtureRunner{}, "", args...); err != nil {
			t.Fatal(err)
		}
	}
	raw := contextFixtureJSON(t)
	for _, override := range []bool{false, true} {
		r := &contextFixtureRunner{window: 2000}
		args := []string{"respond", "--model", "local", "--messages", "-", "--context-max-turns", "1", "--json"}
		if override {
			args = []string{"respond", "--model", "local", "--messages", "-", "--context-strategy", "strict", "--json"}
		}
		out, _, err := runContextCommand(t, r, string(raw), args...)
		if err != nil || (override && (r.counts != 0 || strings.Contains(out, `"context":`))) || (!override && !strings.Contains(out, `"dropped_messages":8`)) {
			t.Fatalf("override=%t err=%v out=%s", override, err, out)
		}
	}
	if _, _, err := runContextCommand(t, &contextFixtureRunner{}, "", "config", "unset", "context-strategy"); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig()
	if err != nil || c.ContextStrategy != "" {
		t.Fatalf("unset: %+v %v", c, err)
	}
	for _, command := range []string{"respond", "chat"} {
		out, _, err := runContextCommand(t, &contextFixtureRunner{}, "", command, "--help")
		if err != nil || !strings.Contains(out, contextStrategyHelp) || !strings.Contains(out, contextTurnsHelp) || !strings.Contains(out, contextReserveHelp) {
			t.Fatalf("%s help: %v %s", command, err, out)
		}
	}
	for _, args := range [][]string{
		{"respond", "--context-strategy", "unknown", "--help"},
		{"chat", "--context-max-turns", "0", "--help"},
		{"respond", "--context-strategy", "drop-oldest", "hello"},
		{"chat", "--context-strategy", "drop-oldest", "--answer-reserve", "-1", "hello"},
		{"chat", "--context-max-turns", "2", "hello"},
		{"respond", "--messages", "-", "--count-tokens", "--context-strategy", "summarize"},
		{"respond", "--messages", "-", "--model", "cloud", "--context-strategy", "summarize"},
		{"chat", "--model", "cloud", "--context-strategy", "summarize", "hello"},
		{"chat", "--context-strategy", "drop-oldest", "--image", "missing", "hello"},
		{"config", "set", "context-strategy", "newest-first"},
	} {
		r := &contextFixtureRunner{window: 8192}
		_, _, err := runContextCommand(t, r, string(raw), args...)
		var classified *cliError
		if !errors.As(err, &classified) || ExitCode(err) != 2 || r.counts != 0 || len(r.generations) != 0 || r.calls != 0 {
			t.Fatalf("%v unexpectedly dispatched: %v", args, err)
		}
	}
}

func TestContextFailureStillDisclosesDrops(t *testing.T) {
	stubConfigPath(t)
	r := &contextFixtureRunner{window: 2000, finalErr: &runner.Error{Kind: runner.KindNativeFailed}}
	out, notes, err := runContextCommand(t, r, string(contextFixtureJSON(t)), "respond", "--model", "local",
		"--messages", "-", "--context-strategy", "sliding-window", "--context-max-turns", "1", "--agent", "-q")
	if ExitCode(err) != 5 || notes != "" || !strings.Contains(out, `"error":`) || !strings.Contains(out, `"context":`) || !strings.Contains(out, `"dropped_messages":8`) {
		t.Fatalf("failed shortened request lost disclosure: %v %s %s", err, out, notes)
	}
}

func TestContextStorageLimitStopsBeforeTokenCounting(t *testing.T) {
	history := make([]store.Message, chat.MaxHistoryMessages)
	r := &contextFixtureRunner{window: 8192}
	_, _, err := prepareChatContext(policyContext("summarize", 2, 1024, &bytes.Buffer{}), r, runner.ModelLocal, history, "Hello")
	if ExitCode(err) != 2 || r.counts != 0 || len(r.generations) != 0 {
		t.Fatalf("storage rejection spent a model call: %v", err)
	}
}
