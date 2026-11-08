// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/setup"
	"github.com/spf13/cobra"
)

const chatGPTExtensionHint = "Note: enable the ChatGPT extension in System Settings > Apple Intelligence & Siri."

func validateSetupRoute(route string) error {
	if !setup.ValidRoute(route) {
		return usageErr(errors.New("--model requires a concrete setup route: local, cloud, cloud-pro, on-device, chatgpt or image (not auto)"))
	}
	return nil
}

func newSetupCmd(flags *rootFlags, newRunner newRunnerFunc) *cobra.Command {
	var route, producer string
	var pending []string
	var importBridges, yes, force bool
	cmd := &cobra.Command{
		Use: "setup [route…]", Short: "Plan route setup, or explicitly open bundled Shortcut imports",
		Long: "Choose a route with --model: local, cloud, cloud-pro, on-device, chatgpt or image. Without --model or --import, print this help. Standalone planning cannot verify installation files; use doctor --model <route> for availability diagnostics. Planning changes nothing. Explicit --import [cloud cloud-pro on-device chatgpt image] verifies the installed bridge archive against its bundle manifest, signs private copies and opens Add Shortcut windows after confirmation; omit routes for all five. Installed bridges are skipped unless --force. Non-interactive, --json, --no-input and --agent imports require --yes. Never downloads, changes Apple settings or requests inference.",
		Args: func(cmd *cobra.Command, args []string) error {
			if importBridges {
				if cmd.Flags().Changed("model") || producer != "" || len(pending) != 0 {
					return usageErr(errors.New("--import cannot be combined with setup planning flags"))
				}
				_, err := selectedImportRoutes(args)
				return err
			}
			if yes || force {
				return usageErr(errors.New("--yes and --force require --import"))
			}
			return noExtraArgs("setup")(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if importBridges {
				return runSetupImport(cmd, flags, args, yes, force)
			}
			if !cmd.Flags().Changed("model") && !flags.asJSON && producer == "" && len(pending) == 0 {
				return cmd.Help()
			}
			if err := validateSetupRoute(route); err != nil {
				return err
			}
			for _, route := range pending {
				if err := validateSetupRoute(route); err != nil || route == "local" {
					return usageErr(errors.New("--pending-import requires concrete Shortcut route IDs"))
				}
			}
			p := setup.Plan{SchemaVersion: 1, Source: "bootstrap", Route: route,
				Status: "unknown", ReasonCode: "runtime_unverified",
				Message: "Standalone setup cannot verify installation files. This does not mean a helper or bridge is missing. Check availability with hollis doctor --model " + route + ".",
				Actions: []setup.Action{{Code: "verify_runtime", Message: "Use the original installation source to verify the matched CLI/helper pair; availability checks do not verify installed files."}},
				Bridge:  setup.BridgeFacts{Discovery: "not_attempted"}}
			if producer != "" {
				record, err := readRuntimeRecord(cmd.Context(), producer)
				if err != nil {
					return printSetupPlan(cmd, flags, p, err)
				}
				if record.State != "verified" {
					switch record.ReasonCode {
					case "runtime_not_installed", "runtime_integrity_failed", "external_runtime_unverified":
						p.ReasonCode = record.ReasonCode
					}
					return printSetupPlan(cmd, flags, p, setupPlanError(p))
				}
				o := setup.Observations{Route: route, Runtime: record, Platform: "supported"}
				var observationErr error
				if route != "local" || record.Helper.Verification == "verified" {
					o, _, observationErr = collectSetupFacts(cmd.Context(), route, newRunner)
					o.Runtime = record
				}
				if slices.Contains(pending, route) {
					o.PendingImport = true
				}
				p = setup.Build(o)
				if observationErr == nil {
					observationErr = setupPlanError(p)
				}
				return printSetupPlan(cmd, flags, p, observationErr)
			}
			return printSetupPlan(cmd, flags, p, configErr(errors.New(p.Message)))
		},
	}
	cmd.Flags().StringVar(&route, "model", "", "Selected route: local, cloud, cloud-pro, on-device, chatgpt or image")
	cmd.Flags().BoolVar(&importBridges, "import", false, "Open selected bundled Shortcut imports (all five if no routes)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Approve --import without prompting")
	cmd.Flags().BoolVar(&force, "force", false, "Import even bridges already found installed (requires --import)")
	cmd.Flags().StringVar(&producer, "runtime-record-command", "", "Local caller-selected verified-runtime record producer")
	_ = cmd.Flags().MarkHidden("runtime-record-command")
	cmd.Flags().StringArrayVar(&pending, "pending-import", nil, "Caller assertion of a pending import; can only require approval")
	_ = cmd.Flags().MarkHidden("pending-import")
	return cmd
}

func planRow(p setup.Plan) map[string]any {
	actions := make([]map[string]any, 0, len(p.Actions))
	for _, a := range p.Actions {
		actions = append(actions, map[string]any{"code": a.Code, "message": a.Message})
	}
	return map[string]any{"schema_version": p.SchemaVersion, "source": p.Source, "route": p.Route,
		"status": p.Status, "reason_code": p.ReasonCode, "message": p.Message, "actions": actions,
		"inference_tested": p.InferenceTested, "bridge": bridgeFactsRow(p.Bridge)}
}

func printSetupPlan(cmd *cobra.Command, flags *rootFlags, p setup.Plan, err error) error {
	if flags.asJSON {
		return printRouteDiagnostic(cmd, flags, planRow(p), err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n%s\nInference tested: false\n", p.Route, p.Status, p.ReasonCode, p.Message)
	for _, action := range p.Actions {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s\n", action.Code, action.Message)
	}
	if p.Route == string(runner.ModelChatGPT) {
		fmt.Fprintln(cmd.OutOrStdout(), chatGPTExtensionHint)
	}
	return err
}

func setupPlanError(p setup.Plan) error {
	if p.Status == "ready" {
		return nil
	}
	if p.Status == "import_required" {
		return notFoundErr(errors.New(p.Message))
	}
	if p.ReasonCode == "transport_missing" || p.ReasonCode == "discovery_unknown" {
		return transportErr(errors.New(p.Message))
	}
	return configErr(errors.New(p.Message))
}

func printRouteDiagnostic(cmd *cobra.Command, flags *rootFlags, report map[string]any, diagnosticErr error) error {
	if diagnosticErr == nil {
		return printJSONFilteredTo(cmd.OutOrStdout(), report, flags)
	}
	errorBody := map[string]any{
		"code": errorCode(diagnosticErr), "message": diagnosticErr.Error(), "exit_code": ExitCode(diagnosticErr),
	}
	projected, err := projectOutput(report, flags)
	if err != nil {
		return err
	}
	data := projected.(map[string]any)
	payload := data
	if flags.agent {
		payload = map[string]any{"meta": agentMeta(), "results": data}
	}
	// Error metadata is never filtered out: callers must not receive a
	// nonzero exit with an apparently successful body.
	payload["error"] = errorBody
	if err := encodeJSON(cmd.OutOrStdout(), payload); err != nil {
		return err
	}
	return &reportedError{err: diagnosticErr}
}

func runSelectedDoctor(cmd *cobra.Command, flags *rootFlags, route string, newRunner newRunnerFunc) error {
	if err := validateSetupRoute(route); err != nil {
		return err
	}
	o, report, err := observeSetupRoute(cmd.Context(), route, newRunner)
	var quota *runner.PCCQuota
	if route == string(runner.ModelCloud) || route == string(runner.ModelCloudPro) {
		reading := runner.PCCQuota{Status: "unknown"}
		if probe, ok := newRunner().(runner.NativeStatusRunner); ok {
			reading = runner.ReadPCCQuota(cmd.Context(), probe)
		}
		quota = &reading
		report["pcc_quota"] = reading
	}
	if flags.asJSON {
		return printRouteDiagnostic(cmd, flags, report, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", route, report["status"], report["reason_code"])
	if warnings, ok := report["warnings"].([]bridgeWarning); ok {
		printBridgeWarnings(cmd.OutOrStdout(), warnings)
	}
	if quota != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "PCC quota: %s (information only; requests may still fail)\n", strings.ReplaceAll(quota.Status, "_", " "))
		if quota.ResetDate != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "PCC reset: %s\n", quota.ResetDate)
		}
	}
	if message, _ := report["message"].(string); message != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", message)
	}
	if actions, _ := report["actions"].([]map[string]any); len(actions) > 0 {
		for _, action := range actions {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s\n", action["code"], action["message"])
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Artifact verification: unknown; inference not tested\n")
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		err = &reportedError{err: err}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Next: %s\n", doctorNextCommand(setup.Diagnose(o)))
	return err
}
