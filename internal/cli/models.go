// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"fmt"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/setup"
	"github.com/spf13/cobra"
)

// modelsBackgroundNote closes the human models table.
const modelsBackgroundNote = "Apple publishes no backend model IDs. The internal Shortcuts parameter\n" +
	"strings are included in --json; background: https://github.com/kamenxrider/hollis/blob/main/README.md#models."

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
backend model IDs — Apple publishes none. Sources:
https://github.com/kamenxrider/hollis/blob/main/README.md#models
https://github.com/kamenxrider/hollis/blob/main/docs/shortcuts.md`,
		Example: `  hollis models
  hollis models --json`,
		Args: noExtraArgs("models"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if modelFilter != "" && modelFilter != "local" {
				return usageErr(fmt.Errorf("--model supports only local for native-only discovery"))
			}
			// Read-only: the plugin calls `models --model local` and relies on it
			// leaving configuration untouched, so this must not use loadConfig,
			// which repairs permissions. An unreadable config leaves the default
			// unknown instead of failing native discovery.
			defaultModel, defaultKnown := "", false
			if c, err := readSetupConfig(); err == nil {
				defaultModel, defaultKnown = c.DefaultModel, true
				if defaultModel == "" {
					defaultModel = string(runner.ModelAuto)
				}
			}
			native := newRunner()
			localStatus, localErr := probeNativeStatus(cmd.Context(), native)
			nativeFacts := nativeAvailabilityFacts(native, localStatus, localErr)
			localRow := localModelRow(localStatus, localErr)
			if modelFilter == "local" {
				annotateModel(localRow, defaultModel, defaultKnown, localReadiness(platformNotChecked, nativeFacts, localErr))
				if flags.asJSON {
					return printJSONArrayFilteredTo(cmd.OutOrStdout(), []map[string]any{localRow}, flags)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "local%s: %s%s (availability only; inference not tested)\n", defaultMarker("local", defaultModel), localStatusLabel(localStatus, localErr), localStatusDetail(localStatus, localErr))
				return nil
			}
			osMajor := macosMajorVersion()
			annotateModel(localRow, defaultModel, defaultKnown, localReadiness(osMajor, nativeFacts, localErr))
			resolved, err := resolveBridges(cmd.Context())
			if err != nil && !localStatus.Available {
				return resolutionCLIError(err)
			}
			transport := shortcutsTransport(err)
			autoRow := autoModelRow(resolved, err, transport, osMajor)
			if defaultKnown {
				autoRow["default"] = defaultModel == "auto"
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
						"capabilities": routeCapabilities(spec),
					}
					annotateModel(row, defaultModel, defaultKnown, modelReadiness(spec.ID, rb, err, transport, osMajor, nativeFacts))
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
				row := map[string]any{"model": string(spec.ID), "available": rb.Available, "verified": rb.Verified}
				annotateModel(row, defaultModel, defaultKnown, modelReadiness(spec.ID, rb, err, transport, osMajor, nativeFacts))
				fmt.Fprintf(w, "  %-20s %s%s\n", string(spec.ID)+defaultMarker(string(spec.ID), defaultModel), desc, modelAvailabilityDetail(row))
			}
			fmt.Fprintf(w, "  %-20s Native Foundation Models SDK: %s%s (inference not tested)\n", "local"+defaultMarker("local", defaultModel), localStatusLabel(localStatus, localErr), localStatusDetail(localStatus, localErr))
			fmt.Fprintf(w, "\n%s\n", modelsBackgroundNote)
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

func annotateModel(row map[string]any, configured string, configuredKnown bool, p setup.Plan) {
	if configuredKnown {
		row["default"] = row["model"] == configured
	}
	row["readiness"] = p.Status
	if p.Status != "ready" {
		row["reason_code"] = p.ReasonCode
		row["reason"] = modelReadinessReason(p)
	}
}

// autoModelRow derives auto from the cloud and on-device facts so its reason
// never claims more than those rows: a failed discovery proves no bridge absent.
func autoModelRow(resolved map[runner.Model]runner.ResolvedBridge, discoveryErr error, transport string, major int) map[string]any {
	available := resolved[runner.ModelCloud].Available || resolved[runner.ModelOnDevice].Available
	row := map[string]any{
		"model": "auto", "apple_model": "fallback strategy: cloud first, on-device on failure",
		"available": available,
	}
	if major == 0 {
		row["reason_code"], row["reason"] = "platform_unknown", "macOS version could not be checked"
		return row
	}
	if available {
		return row
	}
	var resolutionErr *bridgeResolutionError
	switch {
	case transport == "missing":
		row["reason_code"], row["reason"] = "transport_missing", "Shortcuts transport missing; cloud and on-device cannot run"
	case errors.As(discoveryErr, &resolutionErr) && resolutionErr.state:
		row["reason_code"], row["reason"] = "config_unknown", "configuration could not be read; cloud and on-device bridge presence unknown"
	case discoveryErr != nil:
		row["reason_code"], row["reason"] = "discovery_unknown", "Shortcuts discovery failed; cloud and on-device bridge presence unknown"
	default:
		row["reason_code"], row["reason"] = "routes_unavailable", "neither cloud nor on-device bridge is available"
	}
	return row
}

// shortcutsTransport distinguishes a missing Shortcuts binary from a listing
// that failed for another reason. It probes only after discovery failed.
func shortcutsTransport(discoveryErr error) string {
	var resolutionErr *bridgeResolutionError
	if discoveryErr == nil || (errors.As(discoveryErr, &resolutionErr) && resolutionErr.state) {
		return "available"
	}
	if _, err := lookPath(runner.DefaultShortcutsPath); err != nil {
		return "missing"
	}
	return "available"
}

// "unverified" is reserved for a configured reference that discovery could
// not check; a verified bridge with an unrelated unknown keeps a neutral label.
func modelAvailabilityDetail(row map[string]any) string {
	reason, _ := row["reason"].(string)
	if reason == "" {
		return ""
	}
	label := "unavailable"
	if row["available"] == true {
		label = "unchecked"
		if row["verified"] == false {
			label = "unverified"
		}
	}
	return "; " + label + ": " + reason
}

// platformNotChecked skips the additional CLI platform probe. NativeStatus
// still performs its own bounded platform check.
const platformNotChecked = -1

func localReadiness(major int, native setup.NativeFacts, nativeErr error) setup.Plan {
	return modelReadiness(runner.ModelLocal, runner.ResolvedBridge{}, nil, "available", major, native)
}

// Reuse selected-route policy with facts already collected for the overview.
// A configured UUID remains callable but is not discovery-verified.
func modelReadiness(model runner.Model, rb runner.ResolvedBridge, discoveryErr error, transport string, major int, native setup.NativeFacts) setup.Plan {
	o := setup.Observations{Route: string(model), Platform: "supported", ConfigKnown: true, Transport: transport}
	spec, _ := runner.Spec(model)
	if major == 0 {
		o.Platform = "unknown"
	} else if major != platformNotChecked && ((spec.MinOSMajor > 0 && major < spec.MinOSMajor) || (model == runner.ModelLocal && major < runner.MeasuredOSMajor)) {
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
	facts := setup.NativeFacts{Checked: checked, Available: status.Available, Failed: err != nil,
		Version: status.Version, Protocol: status.Protocol}
	var re *runner.Error
	if errors.As(err, &re) {
		facts.FailureReason = re.NativeReason
		if facts.FailureReason == "" && re.Kind == runner.KindNativeProtocol {
			facts.FailureReason = "native_protocol"
		}
	}
	return facts
}

func modelReadinessReason(p setup.Plan) string {
	switch p.ReasonCode {
	case "platform_unsupported":
		if p.Route == "local" {
			return "requires Apple Silicon and macOS 27 or later"
		}
		return "requires macOS 27 or later"
	case "platform_unknown":
		return "macOS version could not be checked"
	case "native_status_unknown":
		return "native helper missing or availability could not be checked"
	case "helper_mismatch":
		return p.Message
	case "model_unavailable":
		return "Apple Intelligence model unavailable; check settings and downloads"
	case "config_unknown":
		return "configuration could not be read"
	case "transport_missing":
		return "Shortcuts transport /usr/bin/shortcuts not found"
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
