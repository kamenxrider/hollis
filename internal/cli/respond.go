// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

func newRespondCmd(flags *rootFlags, newRunner newRunnerFunc) *cobra.Command {
	var (
		model   string
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "respond [prompt]",
		Short: "Send one prompt to Apple Intelligence and print the plain-text response",
		Long: `Send one prompt to Apple Intelligence and print the plain-text response.

The prompt comes from the positional argument, or from stdin when no argument
is given (multi-line safe). Each call is stateless; use "hollis chat" for
persistent, SQLite-backed conversations.

The default model is auto: cloud first, with automatic fallback to the
on-device model if the cloud run fails. Explicit choices: cloud (AFM 3
Cloud), cloud-pro (AFM 3 Cloud Pro; macOS 27+ — unavailable if that
bridge is not installed), on-device (AFM 3 Core / Core Advanced by
hardware), or chatgpt (ChatGPT extension; enable it in System Settings >
Apple Intelligence & Siri). See hollis models.`,
		Example: `  hollis respond "Summarize this repo in one sentence"
  hollis respond model cloud-pro "Draft a reply"
  printf 'long prompt from a pipeline' | hollis respond
  hollis respond --model cloud-pro "Flag form also works"
  hollis respond --agent "Return strict JSON describing X"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			posModel, promptArgs, hasPosModel := splitModelArgs(args)
			var prompt string
			if len(promptArgs) > 0 {
				prompt = strings.Join(promptArgs, " ")
			} else {
				// --no-input never waits on a terminal (measured: it would
				// otherwise block on stdin forever).
				if flags.noInput && interactiveStdin() {
					return usageErr(errors.New("no prompt provided: pass an argument or pipe stdin (refusing to wait on a terminal in --no-input mode)"))
				}
				// Read the whole of stdin; agents and pipelines drive this.
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return usageErr(fmt.Errorf("read prompt from stdin: %w", err))
				}
				prompt = string(b)
			}
			if strings.TrimSpace(prompt) == "" {
				return usageErr(errors.New("empty prompt: give a prompt as an argument or pipe it via stdin"))
			}

			m, err := effectiveModel(cmd, model, posModel, hasPosModel)
			if err != nil {
				return configErr(err)
			}
			if !m.Valid() {
				return usageErr(fmt.Errorf("unknown model %q: choose auto (default), cloud, cloud-pro, on-device, or chatgpt", m))
			}

			r := newRunner()
			ctx := cmd.Context()
			// Runtime bridge resolution:
			// explicit tiers refuse to run when their bridge did not resolve,
			// and the real transport is retargeted at the resolved refs.
			resolved, err := resolveForRunner(ctx, newRunner)
			if err != nil {
				return configErr(err)
			}
			if err := checkModelAvailable(resolved, m); err != nil {
				return err
			}
			applyResolvedRefs(r, resolved)
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			text, used, err := r.Run(ctx, m, prompt)
			if err != nil {
				return toCLIError(err)
			}

			if flags.asJSON {
				// model is what was asked for; model_used is what answered.
				// They differ when auto falls back, and the two tiers are not
				// interchangeable — a silent substitution would be the one
				// place hollis hides something from the caller.
				return printJSONFiltered(map[string]any{
					"model":      string(m),
					"model_used": string(used),
					"response":   text,
				}, flags)
			}
			// Human output stays clean on stdout; the fallback notice goes to
			// stderr so pipelines are unaffected.
			if m == runner.ModelAuto && used != runner.ModelCloud {
				fmt.Fprintf(os.Stderr, "hollis: cloud unavailable, answered with %s\n", used)
			}
			// Plain text out. Apple emits no trailing newline; add one
			// only for terminal ergonomics, never into stored values.
			fmt.Print(text)
			if !strings.HasSuffix(text, "\n") {
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&model, "model", string(runner.ModelAuto), "Model tier: auto (default: cloud first, on-device fallback), cloud (AFM 3 Cloud), cloud-pro (AFM 3 Cloud Pro; macOS 27+), on-device (AFM 3 Core / Core Advanced by hardware), or chatgpt (ChatGPT extension); see hollis models")
	cmd.Flags().DurationVar(&timeout, "timeout", runner.DefaultTimeout, "Per-call timeout (default 30s, ceiling 120s)")
	return cmd
}
