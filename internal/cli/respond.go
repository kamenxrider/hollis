// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
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
is given (multi-line safe). Each call is stateless; chat persistence arrives
in a later phase.

Model choices: cloud (Apple Intelligence), cloud-pro (Apple Intelligence
Pro), on-device (Apple Intelligence on-device), or chatgpt (ChatGPT
extension; enable it in System Settings > Apple Intelligence & Siri).`,
		Example: `  hollis respond "Summarize this repo in one sentence"
  printf 'long prompt from a pipeline' | hollis respond
  hollis respond --model cloud-pro "Draft a reply"
  hollis respond --agent "Return strict JSON describing X"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var prompt string
			if len(args) > 0 {
				prompt = strings.Join(args, " ")
			} else {
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

			m := runner.Model(strings.TrimSpace(model))
			if !m.Valid() {
				return usageErr(fmt.Errorf("unknown model %q: choose cloud, cloud-pro, on-device, or chatgpt", model))
			}

			r := newRunner()
			ctx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			text, err := r.Run(ctx, m, prompt)
			if err != nil {
				return toCLIError(err)
			}

			if flags.asJSON {
				return printJSONFiltered(map[string]any{
					"model":    string(m),
					"response": text,
				}, flags)
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
	cmd.Flags().StringVar(&model, "model", string(runner.ModelCloud), "Apple Intelligence tier: cloud (AFM 3 Cloud), cloud-pro (AFM 3 Cloud Pro), on-device (AFM 3 Core / Core Advanced by hardware), or chatgpt (ChatGPT extension); see hollis models")
	cmd.Flags().DurationVar(&timeout, "timeout", runner.DefaultTimeout, "Per-call timeout (default 30s, ceiling 120s)")
	return cmd
}
