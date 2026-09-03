// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is the printed CLI's version, overridable at build time via ldflags.
var version = "0.2.0"

func newVersionCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.asJSON {
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{"name": cmd.Root().Name(), "version": version}, flags)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", cmd.Root().Name(), version)
			return nil
		},
	}
}
