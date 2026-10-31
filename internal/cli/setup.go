// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"fmt"
	"slices"

	"github.com/kamenxrider/hollis/internal/setup"
	"github.com/spf13/cobra"
)

func validateSetupRoute(route string) error {
	if !setup.ValidRoute(route) {
		return usageErr(errors.New("--model requires a concrete setup route: local, cloud, cloud-pro, on-device, chatgpt or image (not auto)"))
	}
	return nil
}

func newSetupCmd(flags *rootFlags, newRunner newRunnerFunc) *cobra.Command {
	var route, producer string
	var pending []string
	cmd := &cobra.Command{
		Use: "setup", Short: "Plan setup for one selected route without changing anything",
		Long: "Return read-only setup actions for a selected route. Runtime verification belongs to the installation owner. This command never downloads, imports, changes Apple settings or requests inference.",
		Args: noExtraArgs("setup"),
		RunE: func(cmd *cobra.Command, _ []string) error {
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
				Message: "Verify the runtime with its installation owner before selected-route planning. Use doctor --model for independent availability diagnostics.",
				Actions: []setup.Action{{Code: "verify_runtime", Message: "Verify the installed runtime through its installation owner."}},
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
	data := filterFields(report, flags.selectFields)
	payload := data
	if flags.agent {
		payload = map[string]any{"meta": agentMeta(), "results": data}
	}
	if diagnosticErr != nil {
		payload["error"] = map[string]any{"code": errorCode(diagnosticErr), "message": diagnosticErr.Error(), "exit_code": ExitCode(diagnosticErr)}
	}
	if err := encodeJSON(cmd.OutOrStdout(), payload); err != nil {
		return err
	}
	if diagnosticErr != nil {
		return &reportedError{err: diagnosticErr}
	}
	return nil
}

func runSelectedDoctor(cmd *cobra.Command, flags *rootFlags, route string, newRunner newRunnerFunc) error {
	if err := validateSetupRoute(route); err != nil {
		return err
	}
	_, report, err := observeSetupRoute(cmd.Context(), route, newRunner)
	if flags.asJSON {
		return printRouteDiagnostic(cmd, flags, report, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\nArtifact verification: unknown; inference not tested\n", route, report["status"], report["reason_code"])
	return err
}
