// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"fmt"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/setup"
	"github.com/spf13/cobra"
)

func newModelsCmd(flags *rootFlags, newRunner newRunnerFunc) *cobra.Command {
	var modelFilter string
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List the selectable model tiers and what maps to what",
		Long: `List the selectable model tiers and what maps to what.

Lists every text route, marks the configured default (auto if unset),
and explains unavailable routes using read-only readiness checks.
Availability is not proof of successful inference.

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
			c, err := loadConfig()
			if err != nil {
				return configErr(err)
			}
			defaultModel := c.DefaultModel
			if defaultModel == "" {
				defaultModel = string(runner.ModelAuto)
			}
			native := newRunner()
			localStatus, localErr := probeNativeStatus(cmd.Context(), native)
			nativeFacts := nativeAvailabilityFacts(native, localStatus, localErr)
			osMajor := macosMajorVersion()
			localPlan := modelReadiness(runner.ModelLocal, runner.ResolvedBridge{}, nil, osMajor, nativeFacts)
			localRow := localModelRow(localStatus, localErr)
			annotateModel(localRow, defaultModel, localPlan)
			if modelFilter == "local" {
				if flags.asJSON {
					return printJSONArrayFilteredTo(cmd.OutOrStdout(), []map[string]any{localRow}, flags)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "local%s: %s%s (availability only; inference not tested)\n", defaultMarker("local", defaultModel), localStatusLabel(localStatus, localErr), localStatusDetail(localStatus, localErr))
				return nil
			}
			resolved, err := resolveBridges(cmd.Context())
			if err != nil && !localStatus.Available {
				return resolutionCLIError(err)
			}
			autoAvailable := resolved[runner.ModelCloud].Available || resolved[runner.ModelOnDevice].Available
			autoRow := map[string]any{
				"model": "auto", "apple_model": "fallback strategy: cloud first, on-device on failure",
				"default": defaultModel == "auto", "available": autoAvailable,
			}
			if !autoAvailable {
				autoRow["reason_code"] = "routes_unavailable"
				autoRow["reason"] = "neither cloud nor on-device bridge is available"
			}

			if flags.asJSON {
				// auto first — it is selectable but a strategy, not a tier:
				// no WFLLMModel parameter and no bridge of its own, matching
				// the human output and GET /v1/models.
				rows := []map[string]any{autoRow}
				for _, spec := range runner.Specs() {
					rb := resolved[spec.ID]
					row := map[string]any{
						"model":        string(spec.ID),
						"wfllm_model":  spec.Selection,
						"apple_model":  appleModelName(spec.Names, osMajor),
						"resolved_ref": rb.Ref,
						"source":       string(rb.Source),
						"verified":     rb.Verified,
						"available":    rb.Available,
					}
					annotateModel(row, defaultModel, modelReadiness(spec.ID, rb, err, osMajor, nativeFacts))
					rows = append(rows, row)
				}
				rows = append(rows, localRow)
				return printJSONArrayFilteredTo(cmd.OutOrStdout(), rows, flags)
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "hollis models (version %s)\n", version)
			fmt.Fprintf(w, "  %-20s %s%s\n", "auto"+defaultMarker("auto", defaultModel), autoRow["apple_model"], modelAvailabilityDetail(autoRow))
			for _, spec := range runner.Specs() {
				rb := resolved[spec.ID]
				desc := appleModelName(spec.Names, osMajor)
				row := map[string]any{"model": string(spec.ID), "available": rb.Available}
				annotateModel(row, defaultModel, modelReadiness(spec.ID, rb, err, osMajor, nativeFacts))
				fmt.Fprintf(w, "  %-20s %s%s\n", string(spec.ID)+defaultMarker(string(spec.ID), defaultModel), desc, modelAvailabilityDetail(row))
			}
			fmt.Fprintf(w, "  %-20s Native Foundation Models SDK: %s%s (inference not tested)\n", "local"+defaultMarker("local", defaultModel), localStatusLabel(localStatus, localErr), localStatusDetail(localStatus, localErr))
			fmt.Fprintf(w, "\nApple publishes no backend model IDs. The internal Shortcuts parameter\nstrings are included in --json; background in README \"Model tiers\".\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&modelFilter, "model", "", "Use local for native-only availability without Shortcuts discovery")
	return cmd
}

func defaultMarker(model, configured string) string {
	if model == configured {
		return " (default)"
	}
	return ""
}

func annotateModel(row map[string]any, configured string, p setup.Plan) {
	row["default"] = row["model"] == configured
	row["readiness"] = p.Status
	if p.Status != "ready" {
		row["reason_code"] = p.ReasonCode
		row["reason"] = modelReadinessReason(p)
	}
}

func modelAvailabilityDetail(row map[string]any) string {
	reason, _ := row["reason"].(string)
	if reason == "" {
		return ""
	}
	label := "unavailable"
	if row["available"] == true {
		label = "unverified"
	}
	return "; " + label + ": " + reason
}

// Reuse selected-route policy with facts already collected for the overview.
// A configured UUID remains callable but is not discovery-verified.
func modelReadiness(model runner.Model, rb runner.ResolvedBridge, discoveryErr error, major int, native setup.NativeFacts) setup.Plan {
	o := setup.Observations{Route: string(model), Platform: "supported", ConfigKnown: true, Transport: "available"}
	spec, _ := runner.Spec(model)
	if major == 0 {
		o.Platform = "unknown"
	} else if (spec.MinOSMajor > 0 && major < spec.MinOSMajor) || (model == runner.ModelLocal && major < runner.MeasuredOSMajor) {
		o.Platform = "unsupported"
	}
	if model == runner.ModelLocal {
		o.Native = native
	} else {
		o.Bridge = setup.BridgeFacts{Discovery: "completed", Reference: rb.Ref,
			Configured: rb.Source == runner.SourceConfig || rb.Source == runner.SourceConfiguredUnverified, Discovered: rb.Verified}
		if discoveryErr != nil {
			o.Bridge.Discovery = "failed"
			var resolutionErr *bridgeResolutionError
			if errors.As(discoveryErr, &resolutionErr) && resolutionErr.state {
				o.ConfigKnown = false
			}
		}
	}
	return setup.Diagnose(o)
}

func nativeAvailabilityFacts(r runner.Runner, status runner.LocalStatus, err error) setup.NativeFacts {
	_, checked := r.(runner.NativeStatusRunner)
	return setup.NativeFacts{Checked: checked, Available: status.Available, Failed: err != nil}
}

func modelReadinessReason(p setup.Plan) string {
	switch p.ReasonCode {
	case "platform_unsupported":
		return "requires macOS 27 or later"
	case "platform_unknown":
		return "macOS version could not be checked"
	case "native_status_unknown":
		return "native helper missing or availability could not be checked"
	case "model_unavailable":
		return "Apple Intelligence model unavailable; check settings and downloads"
	case "config_unknown":
		return "configuration could not be read"
	case "transport_missing":
		return "Shortcuts transport missing"
	case "discovery_unknown":
		return "Shortcuts discovery failed; bridge presence unknown"
	case "configured_reference_unverified":
		return "configured Shortcut reference is unverified"
	case "bridge_missing":
		return "Shortcut not imported"
	default:
		return p.Message
	}
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
