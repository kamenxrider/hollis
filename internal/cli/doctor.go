// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"os/exec"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

// bridgeCheck carries one bridge's health. Exported fields with JSON
// tags so `doctor --json` emits real objects instead of {} (results
// advanced-cli-test-2026-09-01, defect 1).
type bridgeCheck struct {
	Model string `json:"model"`
	// UUID is the compiled fallback reference — a private artifact of the
	// measured macOS 27 machine, not necessarily what Run invokes.
	UUID string `json:"uuid"`
	// Name is the expected import name from make-bridge.py.
	Name string `json:"name"`
	// Installed reports whether an expected name was seen in
	// `shortcuts list` (or supplied by a matching config override).
	Installed bool `json:"installed"`
	// ResolvedRef is what Run actually invokes: a name or a UUID.
	ResolvedRef string `json:"resolved_ref"`
	// Source is how the ref was resolved: config, shortcuts-list, or
	// compiled-uuid.
	Source string `json:"source"`
	// Status is ok, missing, or (for cloud-pro on macOS < 27)
	// unsupported.
	Status string `json:"status"`
}

func newDoctorCmd(flags *rootFlags, _ newRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check transport health: shortcuts CLI, bridge shortcuts, settings",
		Example: `  hollis doctor
  hollis doctor --json
  hollis doctor --agent`,
		RunE: func(cmd *cobra.Command, args []string) error {
			report := map[string]any{}

			// Doctor always probes the real transport.
			r := runner.New()

			// Transport binary.
			if _, err := exec.LookPath(r.ShortcutsPath); err != nil {
				report["shortcuts_cli"] = fmt.Sprintf("ERROR not found at %s", r.ShortcutsPath)
			} else {
				report["shortcuts_cli"] = "ok"
			}

			// Runtime bridge resolution (results/macos-26-compat.md steps 1+3):
			// what each tier would invoke and whether it is usable here.
			resolved, resErr := resolveBridges(cmd.Context())
			osMajor := macosMajorVersion()
			report["macos"] = macosVersion()
			report["support"] = supportNote
			bridges := []bridgeCheck{}
			for _, m := range runner.Models {
				rb := resolved[m]
				bridges = append(bridges, bridgeCheck{
					Model:       string(m),
					UUID:        runner.CompiledUUID(m),
					Name:        runner.BridgeNameCandidates[m][0],
					Installed:   rb.ListedName != "",
					ResolvedRef: rb.Ref,
					Source:      string(rb.Source),
					Status:      resolvedStatus(rb, osMajor),
				})
			}
			report["bridges"] = bridges
			if resErr != nil {
				report["resolution"] = fmt.Sprintf("ERROR %s", resErr)
			}

			report["timeout_default"] = runner.DefaultTimeout.String()
			report["exit_codes"] = map[string]int{
				"success": 0, "usage": 2, "missing": 3, "transport": 5, "timeout": 7,
			}
			report["version"] = version

			if flags.asJSON {
				return printJSONFiltered(report, flags)
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "hollis doctor (version %s)\n", version)
			fmt.Fprintf(w, "  transport: %s\n", report["shortcuts_cli"])
			fmt.Fprintf(w, "  macos: %s\n", report["macos"])
			fmt.Fprintf(w, "  support: %s\n", supportNote)
			fmt.Fprintf(w, "  timeout default: %s (ceiling 120s)\n", runner.DefaultTimeout)
			fmt.Fprintf(w, "  bridges (resolved at runtime):\n")
			for _, b := range bridges {
				indicator := "OK"
				if b.Status != "ok" {
					indicator = "MISSING"
					if b.Status == "unsupported" {
						indicator = "UNSUPPORTED"
					}
				}
				fmt.Fprintf(w, "    [%s] %-10s %s (%s)\n", indicator, b.Model, b.ResolvedRef, b.Source)
				if b.Status == "unsupported" {
					fmt.Fprintf(w, "           %s\n", unresolvedProNote)
				}
			}
			return nil
		},
	}
	return cmd
}
