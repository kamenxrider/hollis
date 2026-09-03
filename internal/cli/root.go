// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package cli implements the hollis command surface in the Printing Press
// conventions (cobra, --agent/--json/--select, stable exit codes, agent-context).
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
	cmd, flags := newRootCmdWithFlags(newRunnerDefault)
	err := cmd.Execute()
	if err == nil {
		return nil
	}
	if ErrorReported(err) {
		return err
	}
	if flags.asJSON || flags.agent {
		payload := map[string]any{
			"error": map[string]any{
				"code": errorCode(err), "message": err.Error(), "exit_code": ExitCode(err),
			},
		}
		if flags.agent {
			payload["meta"] = agentMeta()
		}
		if encodeErr := encodeJSON(cmd.OutOrStdout(), payload); encodeErr != nil {
			return encodeErr
		}
		return &reportedError{err: err}
	}
	return err
}

func NewRootCmd(newRunner newRunnerFunc) *cobra.Command {
	cmd, _ := newRootCmdWithFlags(newRunner)
	return cmd
}

func newRootCmdWithFlags(newRunner newRunnerFunc) (*cobra.Command, *rootFlags) {
	var flags rootFlags
	var showVersion bool
	var helpRequested bool
	rootCmd := &cobra.Command{
		Use:   "hollis",
		Short: `hollis — Apple Intelligence from the terminal (cloud, cloud-pro, on-device, chatgpt), via macOS Shortcuts.`,
		Long: `hollis — Apple Intelligence from the terminal, via macOS Shortcuts.

hollis sends a prompt to Apple Intelligence (cloud, cloud-pro, on-device, or
chatgpt) through a bridge shortcut invoked with /usr/bin/shortcuts, and
returns plain text. Prompts come from arguments or stdin, so shell pipelines
and agents drive it. Persistent chats are stored in local SQLite and replayed
each turn.

Model tiers: run 'hollis models' to see what resolves on this machine
(cloud-pro is macOS 27+). Bridge shortcuts are resolved at runtime:
config override > installed name; a compiled development UUID is retained only
as an unverified candidate ('hollis config set bridge <tier> <name-or-uuid>'
to override).
Default model: auto (cloud first, then one on-device fallback only for an
unavailable bridge, rate limit, transient transport failure, or empty output).
Agent mode: add --agent to supported data commands for JSON output + non-interactive mode.
Health check: run 'hollis doctor' to verify the transport and bridges.
Local OpenAI-compatible endpoint: run 'hollis serve' (127.0.0.1:1978).
See README.md for recipes.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Classify unknown commands and --version extras before deferred help
		// is rendered, so --help cannot turn invalid input into exit 0.
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			if showVersion {
				return usageErr(errors.New("--version does not accept a command or positional arguments"))
			}
			return usageErr(fmt.Errorf("unknown command %q: run 'hollis --help' for the command list", args[0]))
		},
		// Unknown subcommands are a usage error for agents (stable exit 2),
		// not a silent help dump with exit 0. Bare `hollis` still prints help.
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				if len(args) != 0 {
					return usageErr(errors.New("--version does not accept a command or positional arguments"))
				}
				if flags.asJSON {
					return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{"name": cmd.Name(), "version": version}, &flags)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "hollis %s\n", version)
				return nil
			}
			if len(args) == 0 {
				if flags.asJSON {
					return usageErr(errors.New("a command is required in JSON or agent mode"))
				}
				return cmd.Help()
			}
			return usageErr(fmt.Errorf("unknown command %q: run 'hollis --help' for the command list", args[0]))
		},
	}
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageErr(fmt.Errorf("invalid command flags: %w", err))
	})
	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if flags.asJSON || flags.agent {
			_ = printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{
				"command": cmd.CommandPath(),
				"use":     cmd.UseLine(),
				"short":   cmd.Short,
				"long":    cmd.Long,
			}, &flags)
			return
		}
		defaultHelp(cmd, args)
	})

	rootCmd.PersistentFlags().BoolVar(&flags.asJSON, "json", false, "Output as JSON")
	rootCmd.PersistentFlags().BoolVar(&flags.noInput, "no-input", false, "Disable all interactive prompts (for CI/agents)")
	rootCmd.PersistentFlags().StringVar(&flags.selectFields, "select", "", "Comma-separated fields to include in JSON output (e.g. --select response)")
	rootCmd.PersistentFlags().BoolVar(&flags.agent, "agent", false, "Set all agent-friendly defaults (--json --no-input)")
	rootCmd.Flags().BoolVar(&showVersion, "version", false, "Print the Hollis version")

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if flags.agent {
			if cmd.Flags().Changed("json") && !flags.asJSON {
				return usageErr(errors.New("--agent contradicts --json=false: choose agent mode or JSON mode, not both"))
			}
			if cmd.Flags().Changed("no-input") && !flags.noInput {
				return usageErr(errors.New("--agent contradicts --no-input=false: agent mode requires non-interactive input"))
			}
			flags.asJSON = true
			flags.noInput = true
		}
		if strings.TrimSpace(flags.selectFields) != "" && !flags.asJSON {
			return usageErr(errors.New("--select requires --json or --agent"))
		}
		path := cmd.CommandPath()
		if flags.asJSON && (path == "hollis serve" || strings.HasPrefix(path, "hollis completion")) {
			return usageErr(fmt.Errorf("%s supports human output only; remove --json/--agent", path))
		}
		if helpRequested {
			// Hollis installs a deferred help flag so all global contract
			// validation above runs before Cobra renders help.
			return pflag.ErrHelp
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
	rootCmd.AddCommand(newVersionCmd(&flags))
	rootCmd.SetHelpCommand(newStrictHelpCommand(rootCmd))
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()
	makeCompletionParentStrict(rootCmd)
	installDeferredHelpFlags(rootCmd, &helpRequested)
	wrapArgErrors(rootCmd)
	return rootCmd, &flags
}

// deferredHelpValue records --help while always reporting false to Cobra's
// early help check. PersistentPreRunE then validates global flag contracts and
// returns flag.ErrHelp only after those checks pass.
type deferredHelpValue struct {
	requested *bool
}

func (v *deferredHelpValue) Set(raw string) error {
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return err
	}
	*v.requested = value
	return nil
}

func (v *deferredHelpValue) String() string { return "false" }
func (v *deferredHelpValue) Type() string   { return "bool" }

func installDeferredHelpFlags(command *cobra.Command, requested *bool) {
	helpFlag := command.Flags().VarPF(&deferredHelpValue{requested: requested}, "help", "h", "help for "+command.Name())
	helpFlag.NoOptDefVal = "true"
	for _, child := range command.Commands() {
		installDeferredHelpFlags(child, requested)
	}
}

func makeCompletionParentStrict(root *cobra.Command) {
	for _, child := range root.Commands() {
		if child.Name() != "completion" {
			continue
		}
		child.RunE = func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}
		return
	}
}

func newStrictHelpCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Long:  "Help provides help for any Hollis command.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			target, remaining, err := root.Find(args)
			if err != nil || target == nil || len(remaining) != 0 {
				return usageErr(fmt.Errorf("unknown help topic %q", strings.Join(args, " ")))
			}
			return target.Help()
		},
	}
}

func wrapArgErrors(command *cobra.Command) {
	if validate := command.Args; validate != nil {
		command.Args = func(cmd *cobra.Command, args []string) error {
			err := validate(cmd, args)
			if err == nil {
				return nil
			}
			var classified *cliError
			if errors.As(err, &classified) {
				return err
			}
			return usageErr(err)
		}
	}
	for _, child := range command.Commands() {
		wrapArgErrors(child)
	}
}

type cliError struct {
	code int
	err  error
}

type reportedError struct{ err error }

func (e *reportedError) Error() string { return e.err.Error() }
func (e *reportedError) Unwrap() error { return e.err }

// ErrorReported is true when Execute already emitted a JSON error on stdout.
func ErrorReported(err error) bool {
	var reported *reportedError
	return errors.As(err, &reported)
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
// 7 timeout; 10 config.
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
// mapping follows the measured failure surface: confirmed missing-shortcut
// text, exit 64 usage, real SIGABRT, empty successful output, and timeouts.
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
	case runner.KindRateLimited:
		return transportErr(err)
	case runner.KindTimeout:
		return timeoutErr(fmt.Errorf("%s\nhint: raise --timeout (up to the 120s ceiling); empty prompts are rejected before spawn, so a hang here is Apple-side", err))
	case runner.KindNoOutput:
		return transportErr(fmt.Errorf("%s\nhint: run 'hollis doctor'; exit 0 with empty output is always treated as failure", err))
	case runner.KindSIGABRT:
		return transportErr(fmt.Errorf("%s\nhint: SIGABRT (exit 134) — /usr/bin/shortcuts crashed; -o /dev/stdout is never used by hollis", err))
	case runner.KindContextCanceled, runner.KindSignal, runner.KindListFailure:
		return transportErr(err)
	default:
		return transportErr(err)
	}
}

func agentMeta() map[string]any {
	return map[string]any{"source": "apple-intelligence", "schema_version": agentContextSchemaVersion}
}

func encodeJSON(w io.Writer, data any) error {
	return json.NewEncoder(w).Encode(data)
}

// printJSONFilteredTo emits JSON with --select applied through the caller's
// Cobra writer, matching the house agent contract.
func printJSONFilteredTo(w io.Writer, data map[string]any, flags *rootFlags) error {
	data = filterFields(data, flags.selectFields)
	if flags.agent {
		wrapped := map[string]any{"meta": agentMeta(), "results": data}
		return encodeJSON(w, wrapped)
	}
	return encodeJSON(w, data)
}

// printJSONArrayFilteredTo emits a JSON array with --select applied per
// element through the caller's Cobra writer.
func printJSONArrayFilteredTo(w io.Writer, items []map[string]any, flags *rootFlags) error {
	if flags.agent {
		if strings.TrimSpace(flags.selectFields) != "" {
			for i := range items {
				items[i] = filterFields(items[i], flags.selectFields)
			}
		}
		wrapped := map[string]any{"meta": agentMeta(), "results": items}
		return encodeJSON(w, wrapped)
	}
	return encodeJSON(w, items)
}

func errorCode(err error) string {
	switch ExitCode(err) {
	case 2:
		return "usage"
	case 3:
		return "missing"
	case 5:
		return "transport"
	case 7:
		return "timeout"
	case 10:
		return "config"
	default:
		return "unclassified"
	}
}

func noExtraArgs(command string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		return usageErr(fmt.Errorf("%s takes no positional arguments (got %q)", command, strings.Join(args, " ")))
	}
}

func validateTimeout(cmd *cobra.Command, timeout time.Duration) error {
	if !cmd.Flags().Changed("timeout") {
		return nil
	}
	if timeout <= 0 || timeout > runner.MaxTimeout {
		return usageErr(fmt.Errorf("invalid --timeout %s: choose a duration greater than zero and no more than %s", timeout, runner.MaxTimeout))
	}
	return nil
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
