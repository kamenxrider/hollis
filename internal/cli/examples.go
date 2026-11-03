// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"io"

	"github.com/kamenxrider/hollis/examples"
	"github.com/spf13/cobra"
)

func newExamplesCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use: "examples", Short: "Print copyable recipes organized by job; never execute them",
		Long:        "Print embedded job recipes for notes, replies, errors, code, structured answers and saved chats. This command makes no model calls and writes no files. Copied recipes may invoke models and write the files named in the guide.",
		Annotations: map[string]string{"mcp:read-only": "true"},
		Args:        noExtraArgs("examples"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flags.asJSON {
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{"format": "markdown", "content": examples.Content}, flags)
			}
			_, err := io.WriteString(cmd.OutOrStdout(), examples.Content)
			return err
		},
	}
}
