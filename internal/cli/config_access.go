// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"fmt"
	"slices"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

func validateConfigAccessArgs(_ *cobra.Command, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return usageErr(errors.New("choose model, timeout, partial-results, context-strategy, bridge <tier>, or image-bridge [style]"))
	}
	switch args[0] {
	case "model", "timeout", "partial-results", "context-strategy":
		if len(args) != 1 {
			return usageErr(errors.New("model, timeout, partial-results and context-strategy take no selector"))
		}
	case "bridge":
		if len(args) != 2 {
			return usageErr(errors.New("bridge requires a tier: cloud, cloud-pro, on-device or chatgpt"))
		}
		m := runner.Model(args[1])
		if !m.Valid() || m == runner.ModelAuto || m == runner.ModelLocal {
			return usageErr(errors.New("bridge tier must be cloud, cloud-pro, on-device or chatgpt"))
		}
	case "image-bridge":
		if len(args) == 2 && !slices.Contains(imageStyles, args[1]) {
			return usageErr(fmt.Errorf("unknown image style %q", args[1]))
		}
	default:
		return usageErr(fmt.Errorf("unknown config key %q: choose model, timeout, partial-results, context-strategy, bridge or image-bridge", args[0]))
	}
	return nil
}

func newConfigAccessCmd(flags *rootFlags, unset bool) *cobra.Command {
	verb, summary := "get", "Read one saved setting (not environment or built-in defaults)"
	if unset {
		verb, summary = "unset", "Remove one saved setting; leave unrelated settings unchanged"
	}
	return &cobra.Command{
		Use: verb + " <key> [tier-or-style]", Short: summary,
		Args: validateConfigAccessArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if unset {
				if err := updateConfig(func(c *config) error {
					switch args[0] {
					case "context-strategy":
						c.ContextStrategy = ""
					case "partial-results":
						c.PartialResults = nil
					case "model":
						c.DefaultModel = ""
					case "timeout":
						c.Timeout = ""
					case "bridge":
						delete(c.Bridges, args[1])
					case "image-bridge":
						if len(args) == 1 {
							c.ImageBridge = ""
						} else {
							delete(c.ImageBridges, args[1])
						}
					}
					return nil
				}); err != nil {
					return configErr(err)
				}
			}
			value := ""
			if !unset {
				c, err := readSetupConfig()
				if err != nil {
					return configErr(err)
				}
				switch args[0] {
				case "context-strategy":
					value = c.ContextStrategy
				case "partial-results":
					if c.PartialResults != nil {
						value = partialResultsValue(*c.PartialResults)
					}
				case "model":
					value = c.DefaultModel
				case "timeout":
					value = c.Timeout
				case "bridge":
					value = c.Bridges[args[1]]
				case "image-bridge":
					value = c.ImageBridge
					if len(args) == 2 {
						value = c.ImageBridges[args[1]]
					}
				}
			}
			if flags.asJSON {
				row := map[string]any{"key": args[0], "value": value, "configured": value != ""}
				if unset {
					row["ok"] = true
				}
				if len(args) == 2 {
					if args[0] == "bridge" {
						row["tier"] = args[1]
					} else {
						row["style"] = args[1]
					}
				}
				return printJSONFilteredTo(cmd.OutOrStdout(), row, flags)
			}
			if unset {
				fmt.Fprintf(cmd.OutOrStdout(), "%s unset\n", args[0])
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), value)
			}
			return nil
		},
	}
}
