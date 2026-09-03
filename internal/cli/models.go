// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

// modelInfo describes one selectable tier. WFLLMModel is the Shortcuts
// action parameter the bridge shortcuts carry — a Shortcuts-layer
// identifier, never an Apple backend model ID (plan §3.5/§21).
type modelInfo struct {
	tier  runner.Model
	wfllm string
	// appleName is the description on the measured macOS 27 machine.
	appleName string
	// legacyName is the description on macOS < 27, where the AFM 3
	// family does not apply (the cloud tier is the pre-AFM-3 PCC model;
	// do not print "AFM 3" for 26.
	legacyName string
	// unknownName avoids claiming a generation or availability when the
	// operating-system version could not be detected.
	unknownName string
}

// modelCatalog is the exhaustive tier table, ordered as runner.Models.
// Apple model names come from Apple's ML Research announcement of the
// third-generation Foundation Models; Apple publishes no backend model
// IDs or checkpoints for these tiers.
var modelCatalog = []modelInfo{
	{runner.ModelCloud, "Apple Intelligence", "AFM 3 Cloud (Private Cloud Compute)", "Apple Intelligence cloud on Private Cloud Compute (pre-27; not AFM 3)", "Apple Intelligence cloud (macOS version unknown)"},
	{runner.ModelCloudPro, "Apple Intelligence Pro", "AFM 3 Cloud Pro (Private Cloud Compute; macOS 27+)", "Cloud Pro is not offered on macOS 26", "Apple Intelligence Cloud Pro (requires macOS 27; macOS version unknown)"},
	{runner.ModelOnDevice, "Apple Intelligence on Device", "AFM 3 Core or AFM 3 Core Advanced (by hardware)", "Apple Intelligence on-device model (pre-27)", "Apple Intelligence on-device model (macOS version unknown)"},
	{runner.ModelChatGPT, "ChatGPT", "OpenAI ChatGPT extension for Apple Intelligence", "OpenAI ChatGPT extension for Apple Intelligence", "OpenAI ChatGPT extension for Apple Intelligence"},
}

func newModelsCmd(flags *rootFlags, _ newRunnerFunc) *cobra.Command {
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
			resolved, err := resolveBridges(cmd.Context())
			if err != nil {
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
				for _, m := range modelCatalog {
					rb := resolved[m.tier]
					if !rb.Available {
						continue // catalog = what resolves here
					}
					rows = append(rows, map[string]any{
						"model":        string(m.tier),
						"wfllm_model":  m.wfllm,
						"apple_model":  appleModelName(m, osMajor),
						"resolved_ref": rb.Ref,
						"source":       string(rb.Source),
						"verified":     rb.Verified,
					})
				}
				return printJSONArrayFilteredTo(cmd.OutOrStdout(), rows, flags)
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "hollis models (version %s)\n", version)
			fmt.Fprintf(w, "  %-10s %s\n", "auto", "fallback strategy: cloud first, on-device on failure")
			for _, m := range modelCatalog {
				rb := resolved[m.tier]
				if !rb.Available {
					continue
				}
				desc := appleModelName(m, osMajor)
				fmt.Fprintf(w, "  %-10s %s\n", string(m.tier), desc)
			}
			fmt.Fprintf(w, "\nApple publishes no backend model IDs. The internal Shortcuts parameter\nstrings are included in --json; background in README \"Model tiers\".\n")
			return nil
		},
	}
	return cmd
}

func appleModelName(m modelInfo, osMajor int) string {
	if osMajor == 0 {
		return m.unknownName
	}
	if osMajor != 0 && osMajor < runner.MeasuredOSMajor {
		return m.legacyName
	}
	return m.appleName
}
