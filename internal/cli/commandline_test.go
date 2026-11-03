// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
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
		{name: "model value before command", args: []string{"--model", "cloud", "chat", "--help"}, want: []string{"chat", "--model", "cloud", "--help"}},
		{name: "equals model before prompt", args: []string{"--model=local", "hello"}, want: []string{"respond", "--model=local", "hello"}},
		{name: "prompt file named like command", args: []string{"--prompt-file", "chat"}, want: []string{"respond", "--prompt-file", "chat"}},
		{name: "image named like command", args: []string{"--image", "setup"}, want: []string{"respond", "--image", "setup"}},
		{name: "model before multiword prompt", args: []string{"--model", "nosuchmodel", "respod", "hello"}, want: []string{"respond", "--model", "nosuchmodel", "respod", "hello"}},
		{name: "single dash is a prompt", args: []string{"-"}, want: []string{"respond", "-"}},
		{name: "terminator is literal", args: []string{"--", "--chat"}, want: []string{"respond", "--", "--chat"}},
		{name: "terminator after flags", args: []string{"--json", "--", "models"}, want: []string{"respond", "--json", "--", "models"}},
		{name: "known command", args: []string{"models"}, want: []string{"models"}},
		{name: "known command with prompt", args: []string{"chat", "hello"}, want: []string{"chat", "hello"}},
		{name: "help topic", args: []string{"help", "respond"}, want: []string{"help", "respond"}},
		{name: "completion internals are untouched", args: []string{"__complete", "models", ""}, want: []string{"__complete", "models", ""}},
		{name: "no arguments", args: nil, want: nil},
		{name: "flags only", args: []string{"--json"}, want: []string{"--json"}},
		{name: "version flag only", args: []string{"--version"}, want: []string{"--version"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveCommandLine(cmd, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ResolveCommandLine(%q) = %q, want %q", tc.args, got, tc.want)
			}
			// Resolution is idempotent: the binary applies it once, and an
			// embedder that applies it again must not lose the prompt.
			if again, err := ResolveCommandLine(cmd, got); err != nil || !reflect.DeepEqual(again, got) {
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
		{"shwo", "show", 1}, {"lsit", "list", 1}, {"chat", "serve", 5},
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
		{"CHTA", "chat"}, {"modles", "models"}, {"respn", "respond"}, {"mdls", ""},
		{"what", "chat"}, {"hello", ""}, {"make", ""},
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
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	err := executeResolvedCommand(cmd, flags, args)
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
	_, _, err := runResolvedCLI(t, r, "respondd")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d, want usage 2", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), `did you mean "respond"`) {
		t.Fatalf("error %q does not suggest respond", err)
	}
	if !strings.Contains(err.Error(), `hollis respond "respondd"`) {
		t.Fatalf("error %q does not repeat the prompt form", err)
	}
	if r.calls != 0 {
		t.Fatalf("refused command ran the model %d times", r.calls)
	}
}

func TestBarePromptWordsAndLiteralModelPrefix(t *testing.T) {
	for _, args := range [][]string{
		{"what", "is", "a", "closure"}, {"hello"}, {"make", "a", "plan"},
		{"tell", "me", "a", "joke"}, {"tell"}, {"set"}, {"cat", "facts"}, {"watch", "this"},
		{"server", "stop", "page"}, {"respod", "hello"}, {"-"},
		{"--", "model", "cloud", "hello"},
		{"respond", "--", "model", "cloud", "hello"},
		{"respond", "model", "--", "cloud", "hello"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := &recordingRunner{response: "answer"}
			_, _, err := runResolvedCLI(t, r, args...)
			if err != nil {
				t.Fatal(err)
			}
			prompt := args
			if prompt[0] == "respond" {
				prompt = prompt[1:]
			}
			var words []string
			for _, word := range prompt {
				if word != "--" {
					words = append(words, word)
				}
			}
			if r.calls != 1 || r.prompts[0] != strings.Join(words, " ") || r.requested[0] != runner.ModelAuto {
				t.Fatalf("calls=%d prompts=%q models=%v", r.calls, r.prompts, r.requested)
			}
		})
	}
}

func TestSinglePositionalTyposAndLeadingFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"chta"}, `did you mean "chat"`},
		{[]string{"respnd", "--json"}, `did you mean "respond"`},
		{[]string{"modles"}, `did you mean "models"`},
		{[]string{"doctr"}, `did you mean "doctor"`},
		{[]string{"--json", "respnd"}, `hollis --json respond "respnd"`},
		{[]string{"--model", "local", "respnd"}, `hollis --model local respond "respnd"`},
		{[]string{"--json", "--select=response", "respnd"}, `hollis --json --select=response respond "respnd"`},
		{[]string{"--model=local", "respnd"}, `hollis --model=local respond "respnd"`},
		{[]string{"--json", "--select", "response", "respnd"}, `hollis --json --select response respond "respnd"`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			r := &recordingRunner{}
			out, _, err := runResolvedCLI(t, r, tc.args...)
			if ExitCode(err) != 2 || !strings.Contains(err.Error(), tc.want) || r.calls != 0 {
				t.Fatalf("error=%v output=%s calls=%d, want %q", err, out, r.calls, tc.want)
			}
		})
	}
}

func TestRootHelpAndVersionExtrasRemainErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--version", "explain"}, "--version does not accept a command or positional arguments"},
		{[]string{"--help", "explain"}, `unknown command "explain": run 'hollis --help' for the command list`},
		{[]string{"explain", "--help"}, `unknown command "explain": run 'hollis --help' for the command list`},
		{[]string{"help", "explain"}, `unknown help topic "explain"`},
		{[]string{"--version=false", "explain"}, `unknown command "explain": run 'hollis --help' for the command list`},
		{[]string{"--help=false", "explain"}, `unknown command "explain": run 'hollis --help' for the command list`},
		{[]string{"-h", "explain"}, `unknown command "explain": run 'hollis --help' for the command list`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			r := &recordingRunner{}
			out, _, err := runResolvedCLI(t, r, tc.args...)
			if ExitCode(err) != 2 || err.Error() != tc.want || out != "" || r.calls != 0 {
				t.Fatalf("error=%v output=%q calls=%d, want %q", err, out, r.calls, tc.want)
			}
		})
	}
}

func TestBarePromptPathsAreFlagValues(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{"chat", "./-"} {
		if err := os.WriteFile(name, []byte("fixture instruction"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := &recordingRunner{response: "answer"}
		_, _, err := runResolvedCLI(t, r, "--prompt-file", name)
		if err != nil || r.calls != 1 || r.prompts[0] != "fixture instruction" {
			t.Fatalf("path=%q error=%v calls=%d prompts=%q", name, err, r.calls, r.prompts)
		}
	}
	r := &recordingRunner{}
	_, _, err := runResolvedCLI(t, r, "--image", "setup")
	if ExitCode(err) != 2 || !strings.Contains(err.Error(), "--image requires an instruction") || r.calls != 0 {
		t.Fatalf("error=%v calls=%d", err, r.calls)
	}
}

func TestBarePromptFlagValuesExecute(t *testing.T) {
	for _, args := range [][]string{
		{"--model=local", "hello"},
		{"--model", "cloud", "chat", "--help"},
		{"--model", "nosuchmodel", "respod", "hello"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := &recordingRunner{response: "answer"}
			out, _, err := runResolvedCLI(t, r, args...)
			switch args[0] {
			case "--model=local":
				if err != nil || r.calls != 1 || r.prompts[0] != "hello" || r.requested[0] != runner.ModelLocal {
					t.Fatalf("error=%v calls=%d prompts=%q models=%v", err, r.calls, r.prompts, r.requested)
				}
			default:
				if args[1] == "cloud" {
					if err != nil || !strings.Contains(out, "hollis chat [prompt]") || r.calls != 0 {
						t.Fatalf("error=%v output=%q calls=%d", err, out, r.calls)
					}
				} else if ExitCode(err) != 2 || !strings.Contains(err.Error(), `unknown model "nosuchmodel"`) || r.calls != 0 {
					t.Fatalf("error=%v calls=%d", err, r.calls)
				}
			}
		})
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
	if ctx.SchemaVersion != "2" {
		t.Fatalf("schema version = %q, want 2", ctx.SchemaVersion)
	}
	for _, rule := range []string{"sole positional", "edit distance 1", "edit distance 2", "7 or more", "adjacent swap", "--flag=value", "boolean", "leading flags", "no model-prefix parsing", "--help/--version", "a lone positional - is literal prompt text, not stdin", "--prompt-file - reads the instruction from stdin", "refused on a terminal under --no-input or --agent"} {
		if !strings.Contains(ctx.Contracts.CommandResolution, rule) {
			t.Fatalf("command resolution lacks %q: %q", rule, ctx.Contracts.CommandResolution)
		}
	}
}

func TestCommandResolutionUsesFlagDefinitions(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.PersistentFlags().StringP("fixture-value", "v", "", "Fixture value")
	cmd.PersistentFlags().BoolP("fixture-bool", "b", false, "Fixture boolean")
	for _, args := range [][]string{
		{"--fixture-value", "chat", "hello"},
		{"--fixture-value=chat", "hello"},
		{"-v", "chat", "hello"}, {"-vchat", "hello"},
		{"-bvchat", "hello"}, {"--fixture-bool", "hello"},
		{"--fixture-bool=false", "hello"}, {"-b", "hello"},
	} {
		resolved, err := ResolveCommandLine(cmd, args)
		if err != nil || !reflect.DeepEqual(resolved, append([]string{"respond"}, args...)) {
			t.Fatalf("resolve %q = %q, %v", args, resolved, err)
		}
	}
}
