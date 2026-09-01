// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package cli implements the hollis command surface in the Printing Press
// conventions (cobra, --agent/--json/--select, stable exit codes, agent-context).
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

type rootFlags struct {
	asJSON       bool
	agent        bool
	noInput      bool
	selectFields string
}

// newRunnerFunc lets tests substitute a fake runner.
type newRunnerFunc func() runner.Runner

func newRunnerDefault() runner.Runner { return runner.New() }

// Execute runs the CLI in non-interactive mode: never prompts, all values via
// flags or stdin.
func Execute() error {
	return NewRootCmd(newRunnerDefault).Execute()
}

func NewRootCmd(newRunner newRunnerFunc) *cobra.Command {
	var flags rootFlags
	rootCmd := &cobra.Command{
		Use:   "hollis",
		Short: `hollis — Apple Intelligence from the terminal (cloud, cloud-pro, on-device, chatgpt), via macOS Shortcuts.`,
		Long: `hollis — Apple Intelligence from the terminal, via macOS Shortcuts.

hollis sends a prompt to Apple Intelligence (cloud, cloud-pro, on-device, or
chatgpt) through a bridge shortcut invoked with /usr/bin/shortcuts, and
returns plain text. Prompts come from arguments or stdin, so shell pipelines
and agents drive it. Persistent chats are stored in local SQLite and replayed
each turn.

Model tiers: run 'hollis models' to see what maps to what.
Default model: auto (cloud first, on-device fallback if cloud fails).
Agent mode: add --agent to any command for JSON output + non-interactive mode.
Health check: run 'hollis doctor' to verify the transport and bridges.
Local OpenAI-compatible endpoint: run 'hollis serve' (127.0.0.1:1976).
See README.md for recipes.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	rootCmd.SetVersionTemplate("hollis {{ .Version }}\n")

	rootCmd.PersistentFlags().BoolVar(&flags.asJSON, "json", false, "Output as JSON")
	rootCmd.PersistentFlags().BoolVar(&flags.noInput, "no-input", false, "Disable all interactive prompts (for CI/agents)")
	rootCmd.PersistentFlags().StringVar(&flags.selectFields, "select", "", "Comma-separated fields to include in JSON output (e.g. --select response)")
	rootCmd.PersistentFlags().BoolVar(&flags.agent, "agent", false, "Set all agent-friendly defaults (--json --no-input)")

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// --agent sets agent-friendly defaults; explicit flags win.
		if flags.agent {
			flags.asJSON = true
			flags.noInput = true
		}
		return nil
	}

	rootCmd.AddCommand(newRespondCmd(&flags, newRunner))
	rootCmd.AddCommand(newChatCmd(&flags, newRunner))
	rootCmd.AddCommand(newChatsCmd(&flags, newRunner))
	rootCmd.AddCommand(newModelsCmd(&flags, newRunner))
	rootCmd.AddCommand(newConfigCmd(&flags, newRunner))
	rootCmd.AddCommand(newServeCmd(&flags, newRunner))
	rootCmd.AddCommand(newDoctorCmd(&flags, newRunner))
	rootCmd.AddCommand(newAgentContextCmd(rootCmd))
	rootCmd.AddCommand(newVersionCmd())
	return rootCmd
}

type cliError struct {
	code int
	err  error
}

func (e *cliError) Error() string { return e.err.Error() }
func (e *cliError) Unwrap() error { return e.err }

func usageErr(err error) error     { return &cliError{code: 2, err: err} }
func notFoundErr(err error) error  { return &cliError{code: 3, err: err} }
func transportErr(err error) error { return &cliError{code: 5, err: err} }
func timeoutErr(err error) error   { return &cliError{code: 7, err: err} }
func configErr(err error) error    { return &cliError{code: 10, err: err} }

// ExitCode maps an error returned from Execute to a process exit code.
// Exit codes are stable and agent-parseable (plan §23):
// 0 success; 1 unclassified; 2 usage; 3 missing resource; 5 transport;
// 7 timeout.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.code
	}
	return 1
}

// toCLIError converts a runner.Error into a stable exit-code error. The
// mapping follows the measured failure surface: exit 1 = missing shortcut,
// 64 = usage, 134 = SIGABRT, 0+empty = no_output, no exit = hang/timeout.
func toCLIError(err error) error {
	var re *runner.Error
	if !errors.As(err, &re) {
		if err == nil {
			return nil
		}
		return transportErr(err)
	}
	switch re.Kind {
	case runner.KindEmptyPrompt:
		return usageErr(fmt.Errorf("%s\nhint: give a prompt as an argument or pipe it via stdin", err))
	case runner.KindUsage:
		return usageErr(err)
	case runner.KindShortcutMissing:
		return notFoundErr(fmt.Errorf("%s\nhint: install the AFM bridge shortcuts (bridges/*.shortcut) or run 'hollis doctor'", err))
	case runner.KindTimeout:
		return timeoutErr(fmt.Errorf("%s\nhint: raise --timeout; empty prompts are rejected before spawn, so a hang here is Apple-side", err))
	case runner.KindNoOutput:
		return transportErr(fmt.Errorf("%s\nhint: run 'hollis doctor'; exit 0 with empty output is always treated as failure", err))
	case runner.KindSIGABRT:
		return transportErr(fmt.Errorf("%s\nhint: SIGABRT (exit 134) — /usr/bin/shortcuts crashed; -o /dev/stdout is never used by hollis", err))
	default:
		return transportErr(err)
	}
}

// printJSONFiltered emits JSON with --select applied, matching the house
// agent contract (subset of delpher's printJSONFiltered — no compact/csv/quiet).
func printJSONFiltered(data map[string]any, flags *rootFlags) error {
	data = filterFields(data, flags.selectFields)
	enc := json.NewEncoder(os.Stdout)
	if flags.agent {
		wrapped := map[string]any{
			"meta":    map[string]any{"source": "apple-intelligence"},
			"results": data,
		}
		return enc.Encode(wrapped)
	}
	return enc.Encode(data)
}

// printJSONArrayFiltered emits a JSON array with --select applied per
// element, matching the house agent contract.
func printJSONArrayFiltered(items []map[string]any, flags *rootFlags) error {
	if strings.TrimSpace(flags.selectFields) != "" {
		for i := range items {
			items[i] = filterFields(items[i], flags.selectFields)
		}
	}
	enc := json.NewEncoder(os.Stdout)
	if flags.agent {
		wrapped := map[string]any{
			"meta":    map[string]any{"source": "apple-intelligence"},
			"results": items,
		}
		return enc.Encode(wrapped)
	}
	return enc.Encode(items)
}

// filterFields keeps only the specified fields (comma-separated) from a
// JSON object. Supports dotted paths for nested structures.
func filterFields(data map[string]any, fields string) map[string]any {
	if strings.TrimSpace(fields) == "" {
		return data
	}
	var paths [][]string
	for _, f := range strings.Split(fields, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		paths = append(paths, strings.Split(strings.ToLower(f), "."))
	}
	return filterFieldsRec(data, paths)
}

func filterFieldsRec(obj map[string]any, paths [][]string) map[string]any {
	keepWhole := map[string]bool{}
	subPaths := map[string][][]string{}
	for _, p := range paths {
		if len(p) == 0 {
			continue
		}
		if len(p) == 1 {
			keepWhole[p[0]] = true
		} else {
			subPaths[p[0]] = append(subPaths[p[0]], p[1:])
		}
	}
	out := map[string]any{}
	for k, v := range obj {
		lower := strings.ToLower(k)
		if keepWhole[lower] {
			out[k] = v
			continue
		}
		if subs := subPaths[lower]; subs != nil {
			if nested, ok := v.(map[string]any); ok {
				out[k] = filterFieldsRec(nested, subs)
			}
		}
	}
	return out
}
