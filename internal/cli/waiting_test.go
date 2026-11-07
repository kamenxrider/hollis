// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

type unreadableInput struct{}

func (unreadableInput) Read([]byte) (int, error) { panic("bare terminal command must not read stdin") }

func TestBareTerminalShortHelp(t *testing.T) {
	before := interactiveStdin
	interactiveStdin = func() bool { return true }
	t.Cleanup(func() { interactiveStdin = before })
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { t.Fatal("help must not construct runner"); return nil })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(unreadableInput{})
	cmd.SetArgs(nil)
	if err := executeCommand(cmd, flags); err != nil {
		t.Fatal(err)
	}
	if out.String() != shortRootHelp || strings.Contains(out.String(), "edit distance") {
		t.Fatalf("%s", out.String())
	}
	full := rootHelpOutput(t)
	if !strings.Contains(full, "Report issues: https://github.com/kamenxrider/hollis/issues") || !strings.Contains(full, "Command parsing rules: https://") || strings.Contains(full, "edit distance one") {
		t.Fatal(full)
	}
}

func TestWaitingNoteLifecycle(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		var out bytes.Buffer
		ready := make(chan struct{})
		writer := &waitingTestWriter{out: &out, ready: ready}
		stop := waitingNote(ctx, writer, "Cloud", enabled, time.Millisecond)
		if enabled {
			select {
			case <-ready:
			case <-time.After(2 * time.Second):
				t.Fatal("no waiting note")
			}
		}
		cancel()
		stop()
		stop()
		want := ""
		if enabled {
			want = "Waiting for Cloud…\r\x1b[2K"
		}
		if out.String() != want {
			t.Fatalf("%q want %q", out.String(), want)
		}
	}
}

type waitingTestWriter struct {
	out   *bytes.Buffer
	ready chan struct{}
	sent  bool
}

func (w *waitingTestWriter) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	if !w.sent {
		w.sent = true
		close(w.ready)
	}
	return n, err
}

func TestWaitingSuppressionAndShortFlags(t *testing.T) {
	for _, args := range [][]string{{}, {"--json"}, {"--agent"}, {"--quiet"}, {"--no-input"}} {
		cmd, flags := newRootCmdWithFlags(func() runner.Runner { return &fakeRunner{} })
		var out bytes.Buffer
		cmd.SetErr(&out)
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		if waitingForCommand(cmd, flags).enabled {
			t.Fatal("non-TTY waiting note enabled")
		}
	}
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	for _, path := range [][]string{{"respond"}, {"chat"}, {"batch", "plan"}} {
		sub, _, err := cmd.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if sub.Flags().ShorthandLookup("m").Name != "model" {
			t.Fatalf("%v missing -m", path)
		}
	}
	respond, _, _ := cmd.Find([]string{"respond"})
	for short, long := range map[string]string{"f": "file", "s": "system"} {
		if respond.Flags().ShorthandLookup(short).Name != long {
			t.Fatalf("-%s conflict", short)
		}
	}
	if cmd.PersistentFlags().ShorthandLookup("q").Name != "quiet" {
		t.Fatal("-q conflict")
	}
}
