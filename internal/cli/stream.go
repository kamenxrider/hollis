// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

type streamOutputKey struct{}

func selectStreaming(cmd *cobra.Command, flags *rootFlags, model runner.Model, requested bool) (bool, error) {
	explicit := cmd.Flags().Changed("stream")
	if explicit && requested && flags.asJSON {
		return false, usageErr(errors.New("--stream cannot be combined with JSON or agent output"))
	}
	if explicit && requested && model != runner.ModelLocal {
		return false, usageErr(errors.New("streaming requires --model local"))
	}
	if flags.asJSON || model != runner.ModelLocal {
		return false, nil
	}
	if explicit {
		return requested, nil
	}
	return terminalOutput(cmd.OutOrStdout()), nil
}

func runLocalText(ctx context.Context, r runner.Runner, prompt string, streaming bool, out io.Writer) (runner.Completion, error) {
	if streaming {
		sr, ok := r.(runner.StreamRunner)
		if !ok {
			return runner.Completion{}, errors.New("configured runner does not support native-local streaming")
		}
		safe := &terminalStreamWriter{out: out}
		completion, err := sr.Stream(ctx, runner.ModelLocal, prompt, func(delta string) error { _, err := safe.Write([]byte(delta)); return err })
		// Flush even on failure so no pending byte can escape sanitization.
		flushErr := safe.Flush()
		if err != nil {
			return completion, err
		}
		return completion, flushErr
	}
	if cr, ok := r.(runner.CompleteRunner); ok {
		return cr.RunComplete(ctx, runner.ModelLocal, prompt)
	}
	text, model, err := r.Run(ctx, runner.ModelLocal, prompt)
	return runner.Completion{Text: text, Model: model}, err
}

// terminalStreamWriter escapes controls as data. It retains incomplete UTF-8
// across writes; no escape byte is ever forwarded, even across chunk boundaries.
// Redirected explicit streaming uses the same protection as a terminal.
type terminalStreamWriter struct {
	out     io.Writer
	pending []byte
}

func (w *terminalStreamWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	return len(p), w.drain(false)
}
func (w *terminalStreamWriter) Flush() error { return w.drain(true) }
func (w *terminalStreamWriter) drain(final bool) error {
	var safe strings.Builder
	for len(w.pending) > 0 {
		if !utf8.FullRune(w.pending) && !final {
			break
		}
		r, n := utf8.DecodeRune(w.pending)
		if r == utf8.RuneError && n == 1 {
			fmt.Fprintf(&safe, "\\x%02x", w.pending[0])
		} else {
			safe.WriteString(sanitizeTerminalText(string(r)))
		}
		w.pending = w.pending[n:]
	}
	if safe.Len() == 0 {
		return nil
	}
	_, err := io.WriteString(w.out, safe.String())
	return err
}

type prefixedWriter struct {
	out     io.Writer
	prefix  string
	started bool
}

func (w *prefixedWriter) Write(p []byte) (int, error) {
	if !w.started {
		if _, err := io.WriteString(w.out, w.prefix); err != nil {
			return 0, err
		}
		w.started = true
	}
	return w.out.Write(p)
}
