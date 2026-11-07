// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

const shortRootHelp = `Hollis — Apple Intelligence from the terminal.

  hollis respond -m local "Explain closures in Go"
  hollis respond -f notes.txt "Summarize these notes"
  hollis chat
  hollis doctor
  hollis benchmark --runs 3

Run 'hollis --help' for all commands.
`

type waitingContextKey struct{}
type waitingConfig struct {
	out     io.Writer
	enabled bool
}

func waitingForCommand(cmd *cobra.Command, flags *rootFlags) waitingConfig {
	return waitingConfig{out: cmd.ErrOrStderr(), enabled: terminalOutput(cmd.ErrOrStderr()) && !flags.asJSON && !flags.agent && !flags.noInput && !flags.quiet}
}

func (w waitingConfig) start(ctx context.Context, model runner.Model, streaming bool) func() {
	labels := map[runner.Model]string{runner.ModelCloud: "Cloud", runner.ModelCloudPro: "Cloud Pro", runner.ModelOnDevice: "On-device", runner.ModelChatGPT: "ChatGPT", runner.ModelAuto: "Cloud", runner.ModelLocal: "Local"}
	return waitingNote(ctx, w.out, labels[model], w.enabled && !streaming, time.Second)
}

// One delayed line, never an animation. Cancellation and completion both clear
// it before any answer/error is printed. No terminal escapes reach a pipe.
func waitingNote(ctx context.Context, out io.Writer, label string, enabled bool, delay time.Duration) func() {
	if !enabled {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(out, "Waiting for %s…", label)
		<-ctx.Done()
		fmt.Fprint(out, "\r\x1b[2K")
	}()
	return func() { cancel(); <-done }
}
