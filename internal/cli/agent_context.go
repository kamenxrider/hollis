// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
// Adapted from the Printing Press agent contract (delpher internal/cli/agent_context.go).

package cli

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// agentContextSchemaVersion is bumped on any breaking change to the JSON
// shape emitted by `agent-context`. Agents should check this before parsing.
const agentContextSchemaVersion = "1"

// agentContext is the structured description of this CLI consumed by AI
// agents. Agents can introspect the live CLI without parsing --help or
// reading source.
type agentContext struct {
	SchemaVersion string                `json:"schema_version"`
	CLI           agentContextCLI       `json:"cli"`
	Auth          agentContextAuth      `json:"auth"`
	Commands      []agentContextCommand `json:"commands"`
}

type agentContextCLI struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

type agentContextAuth struct {
	Mode    string `json:"mode"`
	EnvVars []any  `json:"env_vars"`
}

type agentContextCommand struct {
	Name        string                `json:"name"`
	Use         string                `json:"use,omitempty"`
	Short       string                `json:"short,omitempty"`
	Annotations map[string]string     `json:"annotations,omitempty"`
	Flags       []agentContextFlag    `json:"flags,omitempty"`
	Subcommands []agentContextCommand `json:"subcommands,omitempty"`
}

type agentContextFlag struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Usage   string `json:"usage,omitempty"`
	Default string `json:"default,omitempty"`
}

func newAgentContextCmd(rootCmd *cobra.Command) *cobra.Command {
	var pretty bool
	cmd := &cobra.Command{
		Use:         "agent-context",
		Short:       "Emit structured JSON describing this CLI for agents",
		Annotations: map[string]string{"mcp:read-only": "true"},
		Long: `Outputs a machine-readable description of commands, flags, and transport so
agents can introspect this CLI at runtime without parsing --help or
reading source. Schema is versioned via schema_version.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := buildAgentContext(rootCmd)
			enc := json.NewEncoder(os.Stdout)
			if pretty {
				enc.SetIndent("", "  ")
			}
			return enc.Encode(ctx)
		},
	}
	cmd.Flags().BoolVar(&pretty, "pretty", false, "indent JSON output for human reading")
	return cmd
}

func buildAgentContext(rootCmd *cobra.Command) agentContext {
	return agentContext{
		SchemaVersion: agentContextSchemaVersion,
		CLI: agentContextCLI{
			Name:        "hollis",
			Description: "Apple Intelligence Cloud & Cloud Pro from the terminal, via macOS Shortcuts.",
			Version:     rootCmd.Version,
		},
		Auth: agentContextAuth{
			// No credentials: transport is the local Shortcuts app.
			Mode:    "none",
			EnvVars: []any{},
		},
		Commands: collectAgentCommands(rootCmd),
	}
}

// collectAgentCommands walks the cobra tree from the given command and
// returns its direct children (skipping the agent-context command itself
// to avoid self-reference). Each child is recursed into if it has
// subcommands. Flags are captured via VisitAll. Output is sorted by
// command name for stable diffs.
func collectAgentCommands(c *cobra.Command) []agentContextCommand {
	children := c.Commands()
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })

	out := make([]agentContextCommand, 0, len(children))
	for _, sub := range children {
		if sub.Name() == "agent-context" {
			continue
		}
		entry := agentContextCommand{
			Name:  sub.Name(),
			Use:   sub.Use,
			Short: sub.Short,
		}
		if len(sub.Annotations) > 0 {
			entry.Annotations = make(map[string]string, len(sub.Annotations))
			for k, v := range sub.Annotations {
				entry.Annotations[k] = v
			}
		}
		sub.Flags().VisitAll(func(f *pflag.Flag) {
			entry.Flags = append(entry.Flags, agentContextFlag{
				Name:    f.Name,
				Type:    f.Value.Type(),
				Usage:   f.Usage,
				Default: f.DefValue,
			})
		})
		sort.Slice(entry.Flags, func(i, j int) bool {
			return entry.Flags[i].Name < entry.Flags[j].Name
		})
		if len(sub.Commands()) > 0 {
			entry.Subcommands = collectAgentCommands(sub)
		}
		out = append(out, entry)
	}
	return out
}
