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
	tier      runner.Model
	wfllm     string
	appleName string
	uuid      string
}

// modelCatalog is the exhaustive tier table, ordered as runner.Models.
// Apple model names come from Apple's ML Research announcement of the
// third-generation Foundation Models; Apple publishes no backend model
// IDs or checkpoints for these tiers.
var modelCatalog = []modelInfo{
	{runner.ModelCloud, "Apple Intelligence", "AFM 3 Cloud (Private Cloud Compute)", runner.BridgeUUIDCloud},
	{runner.ModelCloudPro, "Apple Intelligence Pro", "AFM 3 Cloud Pro (Private Cloud Compute)", runner.BridgeUUIDCloudPro},
	{runner.ModelOnDevice, "Apple Intelligence on Device", "AFM 3 Core or AFM 3 Core Advanced (by hardware)", runner.BridgeUUIDOnDevice},
	{runner.ModelChatGPT, "ChatGPT", "OpenAI ChatGPT extension for Apple Intelligence", runner.BridgeUUIDChatGPT},
}

func newModelsCmd(flags *rootFlags, _ newRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List the selectable model tiers and what maps to what",
		Long: `List the selectable model tiers and what maps to what.

Each tier name (used with --model) maps to a WFLLMModel string carried by
the bridge shortcuts. Those are Shortcuts action parameters, not Apple
backend model IDs — Apple publishes none. See README "Model tiers" for
sources.`,
		Example: `  hollis models
  hollis models --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.asJSON {
				rows := make([]map[string]any, 0, len(modelCatalog))
				for _, m := range modelCatalog {
					rows = append(rows, map[string]any{
						"model":       string(m.tier),
						"wfllm_model": m.wfllm,
						"apple_model": m.appleName,
						"bridge_uuid": m.uuid,
					})
				}
				return printJSONArrayFiltered(rows, flags)
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "hollis models (version %s)\n", version)
			for _, m := range modelCatalog {
				fmt.Fprintf(w, "  %-10s %s\n", string(m.tier), m.appleName)
			}
			fmt.Fprintf(w, "\nApple publishes no backend model IDs. The internal Shortcuts parameter\nstrings are included in --json; background in README \"Model tiers\".\n")
			return nil
		},
	}
	return cmd
}
