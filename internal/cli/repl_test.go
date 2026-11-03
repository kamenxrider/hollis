// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func runTestRepl(t *testing.T, r runner.Runner, input string) (string, string, error) {
	t.Helper()
	st := openTempStore(t)
	t.Cleanup(func() { st.Close() })
	var out, errOut bytes.Buffer
	err := runInteractiveChat(context.Background(), st, "cloud", "", func() runner.Runner { return r }, 0, strings.NewReader(input), &out, &errOut)
	return out.String(), errOut.String(), err
}

func TestInteractiveChatLeavesOnCommandWords(t *testing.T) {
	for _, word := range []string{"quit", "exit", "/quit", "/exit", "Exit"} {
		t.Run(word, func(t *testing.T) {
			r := &recordingRunner{response: "must not run"}
			out, _, err := runTestRepl(t, r, word+"\n")
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if r.calls != 0 {
				t.Fatalf("%q reached the model %d times", word, r.calls)
			}
			if strings.Contains(out, "must not run") {
				t.Fatalf("%q produced a model answer: %q", word, out)
			}
		})
	}
}

func TestInteractiveChatHelpListsCommands(t *testing.T) {
	for _, word := range []string{"/help", "help", "  HeLp \t", " /HeLp "} {
		t.Run(word, func(t *testing.T) {
			r := &recordingRunner{response: "must not run"}
			out, errOut, err := runTestRepl(t, r, word+"\nquit\n")
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if r.calls != 0 {
				t.Fatalf("%q reached the model %d times", word, r.calls)
			}
			if out != "> > " || !strings.Contains(errOut, replHelp) {
				t.Fatalf("command list missing or printed as a model response: stdout=%q stderr=%q", out, errOut)
			}
		})
	}
}

func TestInteractiveChatStartHintAndNormalTurn(t *testing.T) {
	r := &recordingRunner{response: "answer", used: runner.ModelCloud}
	out, errOut, err := runTestRepl(t, r, "help me\nquit\n")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", r.calls)
	}
	if !strings.Contains(r.prompts[0], "help me") {
		t.Fatalf("normal prompt changed: %q", r.prompts[0])
	}
	if !strings.Contains(out, "< answer") {
		t.Fatalf("normal turn missing from stdout: %q", out)
	}
	if !strings.Contains(errOut, "Type help or /help") {
		t.Fatalf("start hint missing from stderr: %q", errOut)
	}
}
