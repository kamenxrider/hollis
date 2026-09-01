// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

// echoRunner returns the transcript it received so tests can assert exactly
// what was sent to the transport, and stores the last prompt.
type echoRunner struct {
	lastPrompt string
}

func (r *echoRunner) Run(_ context.Context, _ runner.Model, prompt string) (string, error) {
	r.lastPrompt = prompt
	return prompt, nil
}

func openTempStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hollis.db"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRunTurnStoresReplayHistory(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()

	conv, err := st.CreateConversation("cloud", "t1")
	if err != nil {
		t.Fatal(err)
	}

	r := &echoRunner{}
	newRunner := func() runner.Runner { return r }

	// Turn 1: no history.
	if _, err := runTurn(st, conv, "Remember the codeword ORBIT-9", func() runner.Runner { return r }); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.lastPrompt, "ASSISTANT:") {
		t.Fatalf("first turn should have no ASSISTANT history: %q", r.lastPrompt)
	}

	// Turn 2: the replay must contain the prior turn's user message.
	if _, err := runTurn(st, conv, "What was the codeword?", newRunner); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.lastPrompt, "ORBIT-9") {
		t.Fatalf("second turn transcript missing prior turn: %q", r.lastPrompt)
	}

	msgs, err := st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 { // 2 user + 2 assistant
		t.Fatalf("stored messages = %d, want 4", len(msgs))
	}
	if msgs[3].Role != "assistant" {
		t.Fatalf("last role = %q, want assistant", msgs[3].Role)
	}
}

func TestTruncateTitle(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := truncateTitle("first line\nsecond line")
	if strings.Contains(got, "\n") {
		t.Fatalf("title must be single line: %q", got)
	}
	if got := truncateTitle(long); len([]rune(got)) > 61 {
		t.Fatalf("title too long: %d runes", len([]rune(got)))
	}
}
