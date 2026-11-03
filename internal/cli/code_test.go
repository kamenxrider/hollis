// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"io"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestFirstFencedBlock(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{name: "language tag", text: "prose\n```go\nfmt.Println(1)\n```\ntrailing", want: "fmt.Println(1)", ok: true},
		{name: "no tag", text: "```\nalpha\nbeta\n```", want: "alpha\nbeta", ok: true},
		{name: "tilde fence", text: "~~~\nvalue\n~~~", want: "value", ok: true},
		{name: "indented fence", text: "  ```sh\n  ls -l\n  ```", want: "  ls -l", ok: true},
		{name: "second block", text: "text\n```go\nfirst\n```\n```go\nsecond\n```", want: "first", ok: true},
		{name: "no fence", text: "just words", ok: false},
		{name: "unclosed fence", text: "```go\nunfinished", ok: false},
		{name: "empty block", text: "```go\n```", want: "", ok: true},
		{name: "empty then content", text: "```go\n```\nprose\n~~~sh\nprintf hi\n~~~", want: "printf hi", ok: true},
		{name: "only empty fences", text: "prose\n```\n```\n~~~go\n\n~~~\nmore prose", ok: true},
		{name: "whitespace-only fence", text: "```go\n \t\n  \n```", ok: true},
		{name: "whitespace then content", text: "```\n \t\n```\n```go\nreal\n```", want: "real", ok: true},
		{name: "empty then unclosed", text: "```\n```\n```go\nunfinished", ok: true},
		{name: "opening at end", text: "```", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := firstFencedBlock(tc.text)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("firstFencedBlock(%q) = %q, %v; want %q, %v", tc.text, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func runCodeRespond(t *testing.T, r runner.Runner, args ...string) (string, string, error) {
	t.Helper()
	stubConfigPath(t)
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs(append([]string{"respond", "--code"}, args...))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestRespondCodePrintsFirstFencedBlock(t *testing.T) {
	r := &recordingRunner{
		response: "Here is the function:\n\n```go\nfunc reverse(s []int) []int {\n\treturn s\n}\n```\n\nCall it this way.",
		used:     runner.ModelCloud,
	}
	out, errOut, err := runCodeRespond(t, r, "write a reverse function")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "func reverse(s []int) []int {\n\treturn s\n}\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want empty", errOut)
	}
}

func TestRespondCodePrintsPlainResponseWithoutFence(t *testing.T) {
	r := &recordingRunner{response: "plain answer", used: runner.ModelCloud}
	out, _, err := runCodeRespond(t, r, "answer")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "plain answer\n" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestRespondCodeEmptyResponseExits3(t *testing.T) {
	r := &recordingRunner{response: "  \n", used: runner.ModelCloud}
	out, _, err := runCodeRespond(t, r, "answer")
	if err == nil || ExitCode(err) != 3 || errorCode(err) != "missing" {
		t.Fatalf("err=%v exit=%d code=%s, want missing exit 3", err, ExitCode(err), errorCode(err))
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	if err.Error() != "no code to print: the response contained no code" {
		t.Fatalf("error %q does not explain the empty result", err)
	}
}

func TestRespondCodeSkipsEmptyFences(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
		want     string
		exit     int
	}{
		{name: "empty then content", response: "```\n```\n```go\nreal\n```", want: "real\n"},
		{name: "only empty fences", response: "prose\n```\n```\n~~~go\n\n~~~\nmore prose", exit: 3},
		{name: "whitespace-only fence", response: "prose\n```go\n \t\n  \n```", exit: 3},
		{name: "no complete fence", response: "```go\nunfinished", want: "```go\nunfinished\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recordingRunner{response: tc.response, used: runner.ModelCloud}
			out, _, err := runCodeRespond(t, r, "answer")
			if ExitCode(err) != tc.exit || out != tc.want {
				t.Fatalf("stdout=%q err=%v exit=%d, want stdout=%q exit=%d", out, err, ExitCode(err), tc.want, tc.exit)
			}
			if tc.exit != 0 && (errorCode(err) != "missing" || err.Error() != "no code to print: the code blocks were empty") {
				t.Fatalf("err=%v code=%s, want missing with empty-block message", err, errorCode(err))
			}
		})
	}
}

func TestRespondCodeContradictionsAreUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "json", args: []string{"--json", "hi"}},
		{name: "agent", args: []string{"--agent", "hi"}},
		{name: "stream", args: []string{"--stream", "hi"}},
		{name: "schema", args: []string{"--model", "local", "--schema", "schema.json", "hi"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recordingRunner{response: "must not run"}
			_, _, err := runCodeRespond(t, r, tc.args...)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d, want usage 2", err, ExitCode(err))
			}
			if r.calls != 0 {
				t.Fatalf("refused combination ran the model %d times", r.calls)
			}
		})
	}
}

func TestRespondCodeDisablesTerminalStreaming(t *testing.T) {
	stubConfigPath(t)
	old := terminalOutput
	terminalOutput = func(io.Writer) bool { return true }
	t.Cleanup(func() { terminalOutput = old })
	r := &localFixtureRunner{text: "prose\n```go\nfunc f() {}\n```\n"}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--model", "local", "--code", "write code"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.streams != 0 {
		t.Fatalf("--code streamed %d times", r.streams)
	}
	if r.calls != 1 || out.String() != "func f() {}\n" {
		t.Fatalf("calls=%d stdout=%q", r.calls, out.String())
	}
}

func TestRespondCodeFlagIsAdvertised(t *testing.T) {
	ctx := buildAgentContext(NewRootCmd(func() runner.Runner { return &fakeRunner{} }))
	var found bool
	for _, command := range ctx.Commands {
		if command.Name != "respond" {
			continue
		}
		for _, flag := range command.Flags {
			if flag.Name == "code" {
				found = true
				if flag.Type != "bool" {
					t.Fatalf("--code type = %q", flag.Type)
				}
			}
		}
	}
	if !found {
		t.Fatal("agent-context does not advertise the --code flag")
	}
}
