// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
// Adapted from the Printing Press agent contract (delpher internal/cli/agent_context.go).

package cli

import (
	"encoding/json"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// agentContextSchemaVersion is bumped on any breaking change to the JSON
// shape emitted by `agent-context`. Agents should check this before parsing.
const agentContextSchemaVersion = "2"

// agentContext is the structured description of this CLI consumed by AI
// agents. Agents can introspect the live CLI without parsing --help or
// reading source.
type agentContext struct {
	SchemaVersion string                `json:"schema_version"`
	CLI           agentContextCLI       `json:"cli"`
	Auth          agentContextAuth      `json:"auth"`
	Commands      []agentContextCommand `json:"commands"`
	Contracts     agentContextContracts `json:"contracts"`
}

type agentContextCLI struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

type agentContextAuth struct {
	Mode    string   `json:"mode"`
	EnvVars []string `json:"env_vars"`
}

type agentContextContracts struct {
	ExitCodes     map[string]int `json:"exit_codes"`
	JSONSchema    string         `json:"json_schema"`
	ErrorSchema   string         `json:"error_schema"`
	ModelDefaults string         `json:"model_defaults"`
	Fallback      string         `json:"fallback_policy"`
	Network       string         `json:"network"`
	Stdout        string         `json:"stdout"`
	Stderr        string         `json:"stderr"`
}

type agentContextCommand struct {
	Name        string                `json:"name"`
	Use         string                `json:"use,omitempty"`
	Short       string                `json:"short,omitempty"`
	Annotations map[string]string     `json:"annotations,omitempty"`
	Flags       []agentContextFlag    `json:"flags,omitempty"`
	Subcommands []agentContextCommand `json:"subcommands,omitempty"`
	OutputModes []string              `json:"output_modes"`
	SideEffects []string              `json:"side_effects"`
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
			enc := json.NewEncoder(cmd.OutOrStdout())
			if pretty {
				enc.SetIndent("", "  ")
			}
			return enc.Encode(ctx)
		},
		Args: noExtraArgs("agent-context"),
	}
	cmd.Flags().BoolVar(&pretty, "pretty", false, "indent JSON output for human reading")
	return cmd
}

func buildAgentContext(rootCmd *cobra.Command) agentContext {
	return agentContext{
		SchemaVersion: agentContextSchemaVersion,
		CLI: agentContextCLI{
			Name: "hollis",
			Description: "Apple Intelligence (cloud, cloud-pro, on-device, chatgpt) from the " +
				"terminal, via macOS Shortcuts. Persistent chats in local SQLite; local " +
				"OpenAI-compatible HTTP endpoint via serve.",
			Version: version,
		},
		Auth: agentContextAuth{
			Mode:    "local Shortcuts; optional bearer authentication for HTTP",
			EnvVars: []string{"HOLLIS_API_TOKEN"},
		},
		Contracts: agentContextContracts{
			ExitCodes: map[string]int{
				"success": 0, "unexpected": 1, "usage": 2, "missing": 3, "transport": 5,
				"timeout": 7, "config": 10,
			},
			JSONSchema:    "agent meta schema_version=2; data under results; --select filters results",
			ErrorSchema:   "{meta:{source,schema_version},error:{code,message,exit_code}}",
			ModelDefaults: "positional model <tier> > explicit --model flag > config model > built-in default; text defaults to auto, --image defaults directly to cloud",
			Fallback: "auto tries cloud once, then on-device once, only for missing/rate-limited/" +
				"transient/no-output failures; invalid input, timeout, cancellation, and crashes never retry",
			Network: "HTTP defaults to loopback; remote bind requires --allow-remote plus bearer authentication and an external encrypted tunnel",
			Stdout:  "successful command output only",
			Stderr:  "human errors, fallback notices, and newly-created plain-chat IDs only",
		},
		Commands: collectAgentCommands(rootCmd),
	}
}

// collectAgentCommands walks the cobra tree from the given command and
// returns its direct children. Each child is recursed into if it has
// subcommands. Local and inherited flags are de-duplicated by name. Output
// is sorted by command and flag name for stable diffs.
func collectAgentCommands(c *cobra.Command) []agentContextCommand {
	children := c.Commands()
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })

	out := make([]agentContextCommand, 0, len(children))
	for _, sub := range children {
		entry := agentContextCommand{
			Name:        sub.Name(),
			Use:         sub.Use,
			Short:       sub.Short,
			OutputModes: commandOutputModes(sub),
			SideEffects: commandSideEffects(sub),
		}
		if len(sub.Annotations) > 0 {
			entry.Annotations = make(map[string]string, len(sub.Annotations))
			for k, v := range sub.Annotations {
				entry.Annotations[k] = v
			}
		}
		flagsByName := map[string]agentContextFlag{}
		addFlag := func(f *pflag.Flag) {
			flagsByName[f.Name] = agentContextFlag{
				Name:    f.Name,
				Type:    f.Value.Type(),
				Usage:   f.Usage,
				Default: f.DefValue,
			}
		}
		sub.Flags().VisitAll(addFlag)
		sub.InheritedFlags().VisitAll(addFlag)
		for _, flag := range flagsByName {
			entry.Flags = append(entry.Flags, flag)
		}
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

func commandOutputModes(command *cobra.Command) []string {
	switch command.CommandPath() {
	case "hollis agent-context":
		return []string{"json"}
	case "hollis chats", "hollis completion", "hollis config", "hollis serve":
		return []string{"human"}
	default:
		if command.Parent() != nil && command.Parent().CommandPath() == "hollis completion" {
			return []string{"human"}
		}
		return []string{"human", "json", "agent"}
	}
}

func commandSideEffects(command *cobra.Command) []string {
	switch command.CommandPath() {
	case "hollis respond":
		return []string{"invokes one Shortcut model run", "with --image, reads local image files and creates then removes a private temporary prompt file"}
	case "hollis chat":
		return []string{"invokes Shortcut model runs", "writes local conversation state after successful turns"}
	case "hollis chats rename":
		return []string{"updates local conversation metadata"}
	case "hollis chats delete":
		return []string{"deletes one local conversation transactionally"}
	case "hollis config set":
		return []string{"updates the private local config atomically"}
	case "hollis models", "hollis doctor":
		return []string{"runs read-only local Shortcuts discovery"}
	case "hollis serve":
		return []string{"opens an HTTP listener", "invokes Shortcut model runs for valid model requests"}
	default:
		return []string{}
	}
}
