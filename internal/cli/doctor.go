// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

type bridgeCheck struct {
	model     string
	uuid      string
	name      string
	installed bool
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

			// Installed bridges: name presence plus the UUID hollis runs.
			// `shortcuts run <UUID>` is verified to work even though
			// `shortcuts list` shows names; a name miss is a warning, not
			// a verdict.
			bridges := []bridgeCheck{}
			names, err := r.ListShortcuts(cmd.Context())
			if err != nil {
				report["shortcuts_list"] = fmt.Sprintf("ERROR %s", err)
			} else {
				have := map[string]bool{}
				for _, n := range names {
					have[n] = true
				}
				for _, m := range []struct {
					model runner.Model
					uuid  string
					name  string
				}{
					{runner.ModelCloud, runner.BridgeUUIDCloud, "AFM Bridge - Cloud.signed"},
					{runner.ModelCloudPro, runner.BridgeUUIDCloudPro, "AFM Bridge - Cloud Pro.signed"},
				} {
					bridges = append(bridges, bridgeCheck{
						model:     string(m.model),
						uuid:      m.uuid,
						name:      m.name,
						installed: have[m.name] || have[strings.TrimSuffix(m.name, ".signed")],
					})
				}
				report["bridges"] = bridges
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
			fmt.Fprintf(w, "  timeout default: %s (ceiling 120s)\n", runner.DefaultTimeout)
			if list, ok := report["shortcuts_list"].(string); ok {
				fmt.Fprintf(w, "  shortcuts list: %s\n", list)
			}
			fmt.Fprintf(w, "  bridges (referenced by UUID):\n")
			for _, b := range bridges {
				indicator := "OK"
				if !b.installed {
					indicator = "WARN"
				}
				fmt.Fprintf(w, "    [%s] %-10s %s (%s)\n", indicator, b.model, b.uuid, b.name)
			}
			return nil
		},
	}
	return cmd
}
