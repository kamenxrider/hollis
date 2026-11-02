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
	r := &recordingRunner{response: "must not run"}
	out, errOut, err := runTestRepl(t, r, "/help\nquit\n")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 0 {
		t.Fatalf("/help reached the model %d times", r.calls)
	}
	// Help is interface text, not transcript, so it goes to the banner stream.
	help := out + errOut
	for _, want := range []string{"/help", "/image", "quit", "Ctrl-D"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help output missing %q:\n%s", want, help)
		}
	}
}

func TestInteractiveChatStartHintAndNormalTurn(t *testing.T) {
	r := &recordingRunner{response: "answer", used: runner.ModelCloud}
	out, errOut, err := runTestRepl(t, r, "hello\nquit\n")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", r.calls)
	}
	if !strings.Contains(out, "< answer") {
		t.Fatalf("normal turn missing from stdout: %q", out)
	}
	if !strings.Contains(errOut, "Type /help") {
		t.Fatalf("start hint missing from stderr: %q", errOut)
	}
}
