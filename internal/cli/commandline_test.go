// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestResolveCommandLineRules(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{name: "bare prompt", args: []string{"Explain closures"}, want: []string{"respond", "Explain closures"}},
		{name: "several prompt words", args: []string{"Explain", "closures", "in", "Go"}, want: []string{"respond", "Explain", "closures", "in", "Go"}},
		{name: "global flag before prompt", args: []string{"--json", "Explain closures"}, want: []string{"respond", "--json", "Explain closures"}},
		{name: "select value is skipped", args: []string{"--select", "response", "hi"}, want: []string{"respond", "--select", "response", "hi"}},
		{name: "respond flag before prompt", args: []string{"--model", "local", "hi"}, want: []string{"respond", "--model", "local", "hi"}},
		{name: "single dash is a prompt", args: []string{"-"}, want: []string{"respond", "-"}},
		{name: "terminator is literal", args: []string{"--", "--chat"}, want: []string{"respond", "--", "--chat"}},
		{name: "terminator after flags", args: []string{"--json", "--", "models"}, want: []string{"respond", "--json", "--", "models"}},
		{name: "known command", args: []string{"models"}, want: []string{"models"}},
		{name: "known command with prompt", args: []string{"chat", "hello"}, want: []string{"chat", "hello"}},
		{name: "help topic", args: []string{"help", "respond"}, want: []string{"help", "respond"}},
		{name: "near miss stays for the suggestion", args: []string{"respondd"}, want: []string{"respondd"}},
		{name: "near miss after global flag", args: []string{"--json", "respondd"}, want: []string{"--json", "respondd"}},
		{name: "completion internals are untouched", args: []string{"__complete", "models", ""}, want: []string{"__complete", "models", ""}},
		{name: "no arguments", args: nil, want: nil},
		{name: "flags only", args: []string{"--json"}, want: []string{"--json"}},
		{name: "version flag only", args: []string{"--version"}, want: []string{"--version"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveCommandLine(cmd, tc.args)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ResolveCommandLine(%q) = %q, want %q", tc.args, got, tc.want)
			}
			// Resolution is idempotent: the binary applies it once, and an
			// embedder that applies it again must not lose the prompt.
			if again := ResolveCommandLine(cmd, got); !reflect.DeepEqual(again, got) {
				t.Fatalf("second resolution changed %q to %q", got, again)
			}
		})
	}
}

func TestEditDistanceAndSuggestion(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        int
	}{
		{"", "", 0}, {"a", "", 1}, {"chat", "chats", 1}, {"respod", "respond", 1},
		{"shwo", "show", 2}, {"lsit", "list", 2}, {"chat", "serve", 5},
	} {
		if got := editDistance(tc.left, tc.right); got != tc.want {
			t.Fatalf("editDistance(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
	names := []string{"chat", "chats", "help", "models", "respond"}
	for _, tc := range []struct {
		token string
		want  string
	}{
		{"respondd", "respond"}, {"Chat", "chat"}, {"model", "models"}, {"zzzzzz", ""},
	} {
		if got := nearestCommand(tc.token, names); got != tc.want {
			t.Fatalf("nearestCommand(%q) = %q, want %q", tc.token, got, tc.want)
		}
	}
}

// runResolvedCLI is the process-level path without the process: resolve the
// command line, then run the tree exactly as Execute does.
func runResolvedCLI(t *testing.T, r runner.Runner, args ...string) (string, string, error) {
	t.Helper()
	stubConfigPath(t)
	cmd, flags := newRootCmdWithImageGenerator(func() runner.Runner { return r }, nil)
	cmd.SetArgs(ResolveCommandLine(cmd, args))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := executeCommand(cmd, flags)
	return out.String(), errOut.String(), err
}

func TestBarePromptRunsRespond(t *testing.T) {
	r := &recordingRunner{response: "fixture answer", used: runner.ModelCloud}
	out, errOut, err := runResolvedCLI(t, r, "Explain closures")
	if err != nil {
		t.Fatalf("bare prompt: %v", err)
	}
	if r.calls != 1 || !reflect.DeepEqual(r.prompts, []string{"Explain closures"}) {
		t.Fatalf("runner calls=%d prompts=%q", r.calls, r.prompts)
	}
	if out != "fixture answer\n" || errOut != "" {
		t.Fatalf("stdout=%q stderr=%q", out, errOut)
	}
}

func TestBarePromptAcceptsRespondFlags(t *testing.T) {
	r := &recordingRunner{response: "local answer"}
	_, _, err := runResolvedCLI(t, r, "--model", "local", "say hi")
	if err != nil {
		t.Fatalf("bare prompt with --model: %v", err)
	}
	if r.calls != 1 || len(r.requested) != 1 || r.requested[0] != runner.ModelLocal {
		t.Fatalf("model requested = %v, want local", r.requested)
	}
}

func TestBarePromptJSONOutput(t *testing.T) {
	r := &recordingRunner{response: "answer"}
	_, _, err := runResolvedCLI(t, r, "--json", "prompt marker")
	if err != nil {
		t.Fatalf("bare prompt with --json: %v", err)
	}
	if r.calls != 1 || r.prompts[0] != "prompt marker" {
		t.Fatalf("prompts=%q", r.prompts)
	}
}

func TestBarePromptTerminatorSendsLiteralText(t *testing.T) {
	r := &recordingRunner{response: "answer"}
	if _, _, err := runResolvedCLI(t, r, "--", "--not-a-flag"); err != nil {
		t.Fatalf("terminator prompt: %v", err)
	}
	if len(r.prompts) != 1 || r.prompts[0] != "--not-a-flag" {
		t.Fatalf("prompts=%q, want the literal token", r.prompts)
	}
}

func TestNearMissCommandIsRefusedWithSuggestion(t *testing.T) {
	r := &recordingRunner{response: "must not run"}
	_, _, err := runResolvedCLI(t, r, "respondd", "hello")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d, want usage 2", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), `did you mean "respond"`) {
		t.Fatalf("error %q does not suggest respond", err)
	}
	if !strings.Contains(err.Error(), `hollis respond "respondd hello"`) {
		t.Fatalf("error %q does not repeat the prompt form", err)
	}
	if r.calls != 0 {
		t.Fatalf("refused command ran the model %d times", r.calls)
	}
}

func TestNearMissCommandJSONError(t *testing.T) {
	r := &recordingRunner{response: "must not run"}
	out, errOut, err := runResolvedCLI(t, r, "--json", "respod")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d, want usage 2", err, ExitCode(err))
	}
	if !ErrorReported(err) {
		t.Fatalf("JSON mode must emit the error object, got %v", err)
	}
	if errOut != "" {
		t.Fatalf("structured error leaked to stderr: %q", errOut)
	}
	var body map[string]any
	if decodeErr := json.Unmarshal([]byte(out), &body); decodeErr != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", decodeErr, out)
	}
	errorBody, ok := body["error"].(map[string]any)
	if !ok || errorBody["exit_code"] != float64(2) || errorBody["code"] != "usage" {
		t.Fatalf("error body = %#v", body["error"])
	}
}

func TestGroupCommandSuggestsNearest(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		want    string
		refused string
	}{
		{args: []string{"config", "shwo"}, want: `did you mean "show"`, refused: "config"},
		{args: []string{"chats", "lsit"}, want: `did you mean "list"`, refused: "chats"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cmd := NewRootCmd(func() runner.Runner { return &recordingRunner{} })
			cmd.SetArgs(tc.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d, want usage 2", err, ExitCode(err))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q lacks %q", err, tc.want)
			}
		})
	}
}

func TestAgentContextDescribesCommandResolution(t *testing.T) {
	ctx := buildAgentContext(NewRootCmd(func() runner.Runner { return &fakeRunner{} }))
	if !strings.Contains(ctx.Contracts.CommandResolution, "prompt") ||
		!strings.Contains(ctx.Contracts.CommandResolution, "edit distance 2") {
		t.Fatalf("agent contract does not describe the prompt rules: %q", ctx.Contracts.CommandResolution)
	}
}
