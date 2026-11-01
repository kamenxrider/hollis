// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

func newModelsCmd(flags *rootFlags, newRunner newRunnerFunc) *cobra.Command {
	var modelFilter string
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List the selectable model tiers and what maps to what",
		Long: `List the selectable model tiers and what maps to what.

Lists auto plus the tiers whose bridge shortcuts resolve on this
machine; a tier without a usable bridge (e.g. cloud-pro on macOS 26,
where Use Model had no Cloud Pro) is omitted.

Each tier name (used with --model) maps to a WFLLMModel string carried by
the bridge shortcuts. Those are Shortcuts action parameters, not Apple
backend model IDs — Apple publishes none. See README "Model tiers" for
sources.`,
		Example: `  hollis models
  hollis models --json`,
		Args: noExtraArgs("models"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if modelFilter != "" && modelFilter != "local" {
				return usageErr(fmt.Errorf("--model supports only local for native-only discovery"))
			}
			localStatus, localErr := probeNativeStatus(cmd.Context(), newRunner())
			if modelFilter == "local" {
				if flags.asJSON {
					return printJSONArrayFilteredTo(cmd.OutOrStdout(), []map[string]any{localModelRow(localStatus, localErr)}, flags)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "local: %s (availability only; inference not tested)\n", localStatusLabel(localStatus, localErr))
				return nil
			}
			resolved, err := resolveBridges(cmd.Context())
			if err != nil && !localStatus.Available {
				return resolutionCLIError(err)
			}
			osMajor := macosMajorVersion()

			if flags.asJSON {
				// auto first — it is selectable but a strategy, not a tier:
				// no WFLLMModel parameter and no bridge of its own, matching
				// the human output and GET /v1/models.
				rows := []map[string]any{{
					"model":       "auto",
					"apple_model": "fallback strategy: cloud first, on-device on failure",
				}}
				for _, spec := range runner.Specs() {
					rb := resolved[spec.ID]
					if !rb.Available {
						continue // catalog = what resolves here
					}
					rows = append(rows, map[string]any{
						"model":        string(spec.ID),
						"wfllm_model":  spec.Selection,
						"apple_model":  appleModelName(spec.Names, osMajor),
						"resolved_ref": rb.Ref,
						"source":       string(rb.Source),
						"verified":     rb.Verified,
					})
				}
				rows = append(rows, localModelRow(localStatus, localErr))
				return printJSONArrayFilteredTo(cmd.OutOrStdout(), rows, flags)
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "hollis models (version %s)\n", version)
			fmt.Fprintf(w, "  %-10s %s\n", "auto", "fallback strategy: cloud first, on-device on failure")
			for _, spec := range runner.Specs() {
				rb := resolved[spec.ID]
				if !rb.Available {
					continue
				}
				desc := appleModelName(spec.Names, osMajor)
				fmt.Fprintf(w, "  %-10s %s\n", string(spec.ID), desc)
			}
			fmt.Fprintf(w, "  %-10s Native Foundation Models SDK: %s (inference not tested)\n", "local", localStatusLabel(localStatus, localErr))
			fmt.Fprintf(w, "\nApple publishes no backend model IDs. The internal Shortcuts parameter\nstrings are included in --json; background in README \"Model tiers\".\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&modelFilter, "model", "", "Use local for native-only availability without Shortcuts discovery")
	return cmd
}

func appleModelName(names runner.NameSet, osMajor int) string {
	if osMajor == 0 {
		return names.Unknown
	}
	if osMajor < runner.MeasuredOSMajor {
		return names.Legacy
	}
	return names.Measured
}
