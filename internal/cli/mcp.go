// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

// The `hollis mcp` group edits mcp.json and inspects configured servers. No
// command here calls a model or a tool. `tools` and `allow` start the server
// once to read its tool list; everything else only touches the file.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/kamenxrider/hollis/internal/mcp"
	"github.com/kamenxrider/hollis/internal/store"
	"github.com/spf13/cobra"
)

// mcpConfigPath and mcpEnviron are package vars so tests can isolate them.
var (
	mcpConfigPath = defaultMCPConfigPath
	mcpEnviron    = os.Environ
)

// connectMCP is the common session boundary for inspection and model requests.
func connectMCP(ctx context.Context, attachments []mcp.Attachment, options mcp.Options) (*mcp.Manager, error) {
	return mcp.Attach(ctx, attachments, options)
}

func defaultMCPConfigPath() (string, error) {
	base, err := store.DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, mcp.FileName), nil
}

// mcpInspectBudget bounds `mcp tools` and `mcp allow`: connect plus one
// listing, with room for a slow server to start.
const mcpInspectBudget = 90 * time.Second

func newMCPCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Configure and inspect MCP servers",
		Long: `Configure and inspect MCP (Model Context Protocol) servers.

Servers are defined in mcp.json next to the chat database. A defined server
is inert: nothing in this group calls a model or runs a tool. 'mcp tools' and
'mcp allow' start the server once to read its tool list, then stop it.

Approval modes: "ask" (default) prompts on the terminal before every call;
"auto" runs only the exact tool names in the server's allow list, each pinned
to the definition reviewed by 'hollis mcp allow'.`,
		Example: `  hollis mcp add files --command npx --arg -y --arg @modelcontextprotocol/server-filesystem --arg ~/Documents
  hollis mcp add wiki --url https://mcp.deepwiki.com/mcp
  hollis mcp list
  hollis mcp tools wiki
  hollis mcp allow wiki read_wiki_structure
  hollis mcp mode wiki auto`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			return unknownSubcommandError(cmd, args[0])
		},
	}
	cmd.AddCommand(newMCPListCmd(flags))
	cmd.AddCommand(newMCPToolsCmd(flags))
	cmd.AddCommand(newMCPAddCmd(flags))
	cmd.AddCommand(newMCPSetCmd(flags))
	cmd.AddCommand(newMCPRemoveCmd(flags))
	cmd.AddCommand(newMCPAllowCmd(flags))
	cmd.AddCommand(newMCPDenyCmd(flags))
	cmd.AddCommand(newMCPModeCmd(flags))
	return cmd
}

// mcpCLIError maps an mcp error to the stable exit-code families.
func mcpCLIError(err error) error {
	if err == nil {
		return nil
	}
	var classified *cliError
	if errors.As(err, &classified) {
		return err
	}
	switch mcp.CodeOf(err) {
	case mcp.CodeConfig:
		return &cliError{code: 10, machineCode: string(mcp.CodeConfig), err: err}
	case mcp.CodeTimeout:
		return timeoutErr(err)
	case "":
		return transportErr(err)
	default:
		return &cliError{code: 5, machineCode: string(mcp.CodeOf(err)), err: err}
	}
}

func loadMCPConfig() (string, mcp.Config, error) {
	path, err := mcpConfigPath()
	if err != nil {
		return "", mcp.Config{}, configErr(err)
	}
	cfg, err := mcp.Load(path)
	if err != nil {
		return path, mcp.Config{}, mcpCLIError(err)
	}
	return path, cfg, nil
}

func unknownServerErr(path, name string) error {
	return notFoundErr(fmt.Errorf("no MCP server named %q in %s; run 'hollis mcp list'", name, path))
}

// mcpServerName validates a positional server name before any file access.
func mcpServerName(name string) error {
	if err := mcp.ValidateServerName(name); err != nil {
		return usageErr(errors.New(strings.TrimPrefix(err.Error(), string(mcp.CodeConfig)+": ")))
	}
	return nil
}

type mcpVariable struct {
	Name string `json:"name"`
	From string `json:"from,omitempty"`
	Set  bool   `json:"set"`
}

type mcpAuthView struct {
	Type   string `json:"type"`
	Header string `json:"header,omitempty"`
	Env    string `json:"env"`
	Set    bool   `json:"set"`
}

type mcpServerView struct {
	Name      string        `json:"name"`
	Transport string        `json:"transport"`
	Command   string        `json:"command,omitempty"`
	ArgCount  int           `json:"arg_count"`
	URL       string        `json:"url,omitempty"`
	Approval  string        `json:"approval"`
	Allow     []string      `json:"allow"`
	Pinned    []string      `json:"pinned"`
	Tools     []string      `json:"tools"`
	Env       []string      `json:"env"`
	EnvFrom   []mcpVariable `json:"env_from"`
	Auth      *mcpAuthView  `json:"auth,omitempty"`
}

func viewServer(name string, s mcp.Server, lookup func(string) bool) mcpServerView {
	view := mcpServerView{
		Name: name, Transport: s.Transport(), Command: s.Command, ArgCount: len(s.Args), URL: s.URL,
		Approval: s.Mode(), Allow: append([]string{}, s.Allow...), Pinned: []string{}, Env: []string{}, EnvFrom: []mcpVariable{},
		Tools: append([]string{}, s.Tools...),
	}
	for _, tool := range s.Allow {
		if s.Pins[tool] != "" {
			view.Pinned = append(view.Pinned, tool)
		}
	}
	for key := range s.Env {
		view.Env = append(view.Env, key)
	}
	slices.Sort(view.Env)
	for child, parent := range s.EnvFrom {
		view.EnvFrom = append(view.EnvFrom, mcpVariable{Name: child, From: parent, Set: lookup(parent)})
	}
	slices.SortFunc(view.EnvFrom, func(a, b mcpVariable) int { return strings.Compare(a.Name, b.Name) })
	if s.Auth != nil {
		view.Auth = &mcpAuthView{Type: s.Auth.Type, Header: s.Auth.Name, Env: s.Auth.Env, Set: lookup(s.Auth.Env)}
	}
	return view
}

func environLookup() func(string) bool {
	set := map[string]bool{}
	for _, kv := range mcpEnviron() {
		if key, value, ok := strings.Cut(kv, "="); ok && value != "" {
			set[key] = true
		}
	}
	return func(name string) bool { return set[name] }
}

func newMCPListCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured MCP servers without starting them",
		Long: `List configured MCP servers without starting them.

Shows each server's transport, approval mode, allow list and which referenced
environment variables are set. Values of env, env_from and auth variables are
never printed, and neither are command arguments.`,
		Args: noExtraArgs("mcp list"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, cfg, err := loadMCPConfig()
			if err != nil {
				return err
			}
			lookup := environLookup()
			views := make([]mcpServerView, 0, len(cfg.Servers))
			for _, name := range cfg.Names() {
				views = append(views, viewServer(name, cfg.Servers[name], lookup))
			}
			if flags.asJSON {
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{"path": path, "servers": views}, flags)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Config: %s\n", path)
			if len(views) == 0 {
				fmt.Fprintln(out, "No MCP servers configured. Add one with 'hollis mcp add'.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tTRANSPORT\tAPPROVAL\tALLOW\tTOOLS\tTARGET\tVARIABLES")
			for _, v := range views {
				target := v.Command
				if v.Transport == "http" {
					target = v.URL
				} else if v.ArgCount > 0 {
					target = fmt.Sprintf("%s (+%d args)", v.Command, v.ArgCount)
				}
				allow := fmt.Sprintf("%d", len(v.Allow))
				if len(v.Allow) > 0 {
					allow = fmt.Sprintf("%d (%d pinned)", len(v.Allow), len(v.Pinned))
				}
				selected := "all"
				if len(v.Tools) > 0 {
					selected = strings.Join(v.Tools, ", ")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", v.Name, v.Transport, v.Approval, allow, selected, target, variableSummary(v))
			}
			return tw.Flush()
		},
	}
}

func variableSummary(v mcpServerView) string {
	var parts []string
	for _, e := range v.EnvFrom {
		parts = append(parts, fmt.Sprintf("%s<-%s:%s", e.Name, e.From, setWord(e.Set)))
	}
	if v.Auth != nil {
		parts = append(parts, fmt.Sprintf("auth %s:%s", v.Auth.Env, setWord(v.Auth.Set)))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func setWord(set bool) string {
	if set {
		return "set"
	}
	return "unset"
}

type mcpToolView struct {
	Rendered    string `json:"rendered_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SchemaBytes int    `json:"schema_bytes"`
	Validation  string `json:"validation"`
	Supported   bool   `json:"supported"`
	Filtered    bool   `json:"filtered"`
	Reason      string `json:"reason,omitempty"`
	Pointer     string `json:"pointer,omitempty"`
	Allowlisted bool   `json:"allowlisted"`
	Pin         string `json:"pin_status"`
}

// inspectServer starts one configured server, lists its tools and stops it.
// The server is attached in ask mode so a misconfigured allow list does not
// hide the listing that would explain it.
func inspectServer(ctx context.Context, name string, s mcp.Server) (mcp.ServerStatus, []mcp.Tool, error) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	inspect := s
	inspect.Approval, inspect.Allow, inspect.Pins = mcp.ApprovalAsk, nil, nil
	ctx, cancel := context.WithTimeout(ctx, mcpInspectBudget)
	defer cancel()
	m, err := connectMCP(ctx, []mcp.Attachment{{Name: name, Server: inspect}}, mcp.Options{
		Environ: mcpEnviron(), ClientVersion: version, Interaction: mcp.NonInteractive("inspection never runs tools"),
	})
	if err != nil {
		return mcp.ServerStatus{}, nil, err
	}
	defer m.Close()
	return m.Servers()[0], m.ServerTools(name), nil
}

func pinStatus(s mcp.Server, tool mcp.Tool) string {
	pin, ok := s.Pins[tool.Name]
	switch {
	case !s.Allowed(tool.Name):
		return "-"
	case !ok:
		return "unpinned"
	case pin == tool.Pin:
		return "current"
	default:
		return "changed"
	}
}

func newMCPToolsCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "tools <server>",
		Short: "Start a configured server once and list its tools",
		Long: `Start a configured server once, list its tools, and stop it.

No model is called and no tool is run. Each tool shows the name the model
would see, whether Hollis can validate its arguments locally ("local"), only
the server can ("server"), or the tool is unsupported and never offered, and
whether its allow-list pin still matches the live definition.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := mcpServerName(name); err != nil {
				return err
			}
			path, cfg, err := loadMCPConfig()
			if err != nil {
				return err
			}
			s, ok := cfg.Servers[name]
			if !ok {
				return unknownServerErr(path, name)
			}
			status, listed, err := inspectServer(cmd.Context(), name, s)
			if err != nil {
				return mcpCLIError(err)
			}
			views := make([]mcpToolView, 0, len(listed))
			for _, tool := range listed {
				views = append(views, mcpToolView{
					Rendered: tool.Rendered, Name: tool.Name, Description: tool.Description, SchemaBytes: len(tool.InputSchema),
					Validation: validationWord(tool.Tier), Supported: tool.Offered(), Reason: tool.Reason, Pointer: tool.Pointer,
					Filtered:    tool.Filtered,
					Allowlisted: s.Allowed(tool.Name), Pin: pinStatus(s, tool),
				})
			}
			if flags.asJSON {
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{
					"server": name, "transport": status.Transport, "approval": s.Mode(), "protocol_version": status.ProtocolVersion,
					"offered": status.ToolsOffered, "unsupported": status.ToolsUnsupported, "excluded": status.ToolsExcluded, "filtered": status.ToolsFiltered, "tools": views,
				}, flags)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Server %s (%s, approval %s, protocol %s)\n", name, status.Transport, s.Mode(), mcp.EscapeTerminal(status.ProtocolVersion))
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "TOOL\tMODEL NAME\tVALIDATION\tSCHEMA\tALLOW\tOFFER\tDESCRIPTION")
			for _, v := range views {
				offer := "yes"
				if v.Filtered {
					offer = "filtered"
				} else if !v.Supported {
					offer = "unsupported"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%dB\t%s\t%s\t%s\n", mcp.EscapeTerminal(v.Name), v.Rendered, v.Validation, v.SchemaBytes, v.Pin, offer, oneLine(mcp.EscapeTerminal(v.Description), 60))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			for _, v := range views {
				if v.Validation == "unsupported" {
					fmt.Fprintf(out, "unsupported: %s: %s (at %s)\n", mcp.EscapeTerminal(v.Name), v.Reason, strconv.QuoteToASCII(v.Pointer))
				}
			}
			fmt.Fprintf(out, "%d offered, %d unsupported, %d excluded by SDK, %d filtered out\n", status.ToolsOffered, status.ToolsUnsupported, status.ToolsExcluded, status.ToolsFiltered)
			return nil
		},
	}
}

func validationWord(tier mcp.Tier) string {
	switch tier {
	case mcp.TierValidated:
		return "local"
	case mcp.TierServerValidated:
		return "server"
	default:
		return "unsupported"
	}
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		return string(r[:limit-1]) + "…"
	}
	return s
}

func newMCPAddCmd(flags *rootFlags) *cobra.Command {
	var (
		command, url, dir, approval, auth string
		cmdArgs, env, envFrom, allow      []string
		headers                           []string
		tools                             []string
		callTimeout, connectTimeout       time.Duration
	)
	cmd := &cobra.Command{
		Use:   "add <name> (--command <program> | --url <url>)",
		Short: "Add an MCP server to mcp.json",
		Long: `Add an MCP server to mcp.json.

A stdio server is a program plus arguments, started directly with no shell.
An HTTP server is an https URL (plain http only for loopback). Credentials are
never stored: --env-from and --auth name parent environment variables that
are read when the server starts.

--approval auto requires at least one --allow tool. Allow-listed tools run
automatically only after 'hollis mcp allow <server> <tool>' has pinned their
reviewed definition.`,
		Example: `  hollis mcp add files --command npx --arg -y --arg @modelcontextprotocol/server-filesystem --arg ~/Documents
  hollis mcp add gh --command github-mcp --env-from GITHUB_PERSONAL_ACCESS_TOKEN=GITHUB_TOKEN
  hollis mcp add wiki --url https://mcp.deepwiki.com/mcp
  hollis mcp add search --url https://api.example.dev/mcp --auth header:X-API-Key:EXAMPLE_KEY`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := mcpServerName(name); err != nil {
				return err
			}
			s := mcp.Server{Command: command, Args: cmdArgs, URL: url, Dir: dir, Approval: approval, Allow: allow, Tools: tools}
			var err error
			if s.Env, err = keyValues("--env", env); err != nil {
				return err
			}
			if s.EnvFrom, err = keyValues("--env-from", envFrom); err != nil {
				return err
			}
			if s.Headers, err = parseHeaderFlags(headers); err != nil {
				return err
			}
			if auth != "" {
				if s.Auth, err = parseAuthFlag(auth); err != nil {
					return err
				}
			}
			if s.CallTimeoutMS, err = durationMS(cmd, "call-timeout", callTimeout); err != nil {
				return err
			}
			if s.ConnectTimeoutMS, err = durationMS(cmd, "connect-timeout", connectTimeout); err != nil {
				return err
			}
			path, err := mcpConfigPath()
			if err != nil {
				return configErr(err)
			}
			err = mcp.Update(path, func(cfg *mcp.Config) error {
				if _, exists := cfg.Servers[name]; exists {
					return usageErr(fmt.Errorf("an MCP server named %q already exists; remove it first with 'hollis mcp remove %s'", name, name))
				}
				cfg.Servers[name] = s
				return nil
			})
			if err != nil {
				return mcpCLIError(err)
			}
			return reportMCPChange(cmd, flags, path, name, "added", s)
		},
	}
	f := cmd.Flags()
	f.StringVar(&command, "command", "", "Program to start (absolute path or a name on PATH); no shell is used")
	f.StringArrayVar(&cmdArgs, "arg", nil, "One argument for --command; repeat in order")
	f.StringArrayVar(&env, "env", nil, "KEY=VALUE set in the server's environment; never a credential")
	f.StringArrayVar(&envFrom, "env-from", nil, "CHILD=PARENT: pass the parent variable PARENT to the server as CHILD")
	f.StringVar(&dir, "dir", "", "Absolute working directory for a stdio server")
	f.StringVar(&url, "url", "", "Streamable HTTP endpoint (https; http only for loopback)")
	f.StringVar(&auth, "auth", "", "bearer:VAR or header:NAME:VAR, reading the credential from environment variable VAR")
	f.StringArrayVar(&headers, "header", nil, "NAME:VALUE non-secret HTTP setting (stored literally); repeat; credentials require --auth")
	f.StringVar(&approval, "approval", "", "ask (default) or auto")
	f.StringArrayVar(&allow, "allow", nil, "Exact tool name for the allow list; repeat for more")
	f.StringArrayVar(&tools, "tool", nil, "Offer only this exact server tool name; repeat for more (default: all tools)")
	f.DurationVar(&callTimeout, "call-timeout", 0, "Per-call deadline (default 20s, ceiling 120s)")
	f.DurationVar(&connectTimeout, "connect-timeout", 0, "Connect deadline (default 10s, ceiling 60s)")
	return cmd
}

func newMCPSetCmd(flags *rootFlags) *cobra.Command {
	var tools []string
	var all bool
	cmd := &cobra.Command{
		Use:   "set <server> (--tool NAME ... | --all-tools)",
		Short: "Replace a server's offered-tool filter without changing approval",
		Long: `Replace the exact tool names offered from a configured server.

Repeat --tool for more names, or use --all-tools to remove the filter.
No server is contacted. Unknown names fail when the server next connects.
Approval mode, automatic-execution allow list and definition pins are unchanged.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := mcpServerName(name); err != nil {
				return err
			}
			if (len(tools) > 0) == all || (cmd.Flags().Changed("all-tools") && !all) {
				return usageErr(errors.New("choose one or more --tool NAME flags or --all-tools"))
			}
			path, err := mcpConfigPath()
			if err != nil {
				return configErr(err)
			}
			var updated mcp.Server
			err = mcp.Update(path, func(cfg *mcp.Config) error {
				current, ok := cfg.Servers[name]
				if !ok {
					return unknownServerErr(path, name)
				}
				current.Tools = slices.Clone(tools)
				cfg.Servers[name] = current
				updated = current
				return nil
			})
			if err != nil {
				return mcpCLIError(err)
			}
			return reportMCPChange(cmd, flags, path, name, "updated", updated)
		},
	}
	cmd.Flags().StringArrayVar(&tools, "tool", nil, "Exact server tool name to offer; replaces the filter; repeat for more")
	cmd.Flags().BoolVar(&all, "all-tools", false, "Remove the filter and offer every supported tool")
	return cmd
}

func keyValues(flag string, values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok || key == "" {
			return nil, usageErr(fmt.Errorf("%s needs KEY=VALUE (got an entry without '=')", flag))
		}
		if _, dup := out[key]; dup {
			return nil, usageErr(fmt.Errorf("%s names %s twice", flag, key))
		}
		out[key] = val
	}
	return out, nil
}

func parseHeaderFlags(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, value := range values {
		name, val, ok := strings.Cut(value, ":")
		if !ok || name == "" {
			return nil, usageErr(errors.New("--header needs NAME:VALUE; credentials require --auth"))
		}
		name = strings.ToLower(name)
		if _, duplicate := out[name]; duplicate {
			return nil, usageErr(errors.New("--header names the same header twice (case-insensitive)"))
		}
		out[name] = strings.TrimSpace(val)
	}
	return out, nil
}

func parseAuthFlag(value string) (*mcp.Auth, error) {
	parts := strings.Split(value, ":")
	switch {
	case len(parts) == 2 && parts[0] == "bearer":
		return &mcp.Auth{Type: "bearer", Env: parts[1]}, nil
	case len(parts) == 3 && parts[0] == "header":
		return &mcp.Auth{Type: "header", Name: parts[1], Env: parts[2]}, nil
	default:
		return nil, usageErr(errors.New("--auth must be bearer:VAR or header:NAME:VAR, naming an environment variable, never the credential itself"))
	}
}

func durationMS(cmd *cobra.Command, flag string, d time.Duration) (int, error) {
	if !cmd.Flags().Changed(flag) {
		return 0, nil
	}
	if d <= 0 || d%time.Millisecond != 0 {
		return 0, usageErr(fmt.Errorf("invalid --%s %s: use a positive whole number of milliseconds", flag, d))
	}
	return int(d / time.Millisecond), nil
}

func reportMCPChange(cmd *cobra.Command, flags *rootFlags, path, name, action string, s mcp.Server) error {
	view := viewServer(name, s, environLookup())
	if flags.asJSON {
		return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{"ok": true, "action": action, "path": path, "server": view}, flags)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s MCP server %s (%s, approval %s) in %s\n", capitalize(action), name, view.Transport, view.Approval, path)
	if len(view.Tools) > 0 {
		fmt.Fprintf(out, "Tool filter: %s\n", strings.Join(view.Tools, ", "))
	} else {
		fmt.Fprintln(out, "Tool filter: all")
	}
	if len(view.Allow) > 0 {
		fmt.Fprintf(out, "Allow list: %s\n", strings.Join(view.Allow, ", "))
	}
	if view.Approval == mcp.ApprovalAuto {
		for _, tool := range view.Allow {
			if !slices.Contains(view.Pinned, tool) {
				fmt.Fprintf(out, "Not pinned: %s will not run automatically until 'hollis mcp allow %s %s' reviews it\n", tool, name, tool)
			}
		}
	}
	return nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func newMCPRemoveCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <server>",
		Short: "Remove an MCP server from mcp.json",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := mcpServerName(name); err != nil {
				return err
			}
			path, err := mcpConfigPath()
			if err != nil {
				return configErr(err)
			}
			err = mcp.Update(path, func(cfg *mcp.Config) error {
				if _, ok := cfg.Servers[name]; !ok {
					return unknownServerErr(path, name)
				}
				delete(cfg.Servers, name)
				return nil
			})
			if err != nil {
				return mcpCLIError(err)
			}
			if flags.asJSON {
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{"ok": true, "action": "removed", "path": path, "server": map[string]any{"name": name}}, flags)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed MCP server %s from %s\n", name, path)
			return nil
		},
	}
}

func newMCPAllowCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "allow <server> <tool>",
		Short: "Review a tool and add it to the server's allow list",
		Long: `Review a tool and add it to the server's allow list.

Starts the server once, finds the tool, and records a pin of its exact name,
description and input schema. An "auto" server runs the tool only while the
live definition still matches the pin; if the server changes the tool, run
this command again after reviewing it with 'hollis mcp tools'. Tools whose
arguments only the server can validate are refused for "auto" servers.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, toolName := args[0], args[1]
			if err := mcpServerName(name); err != nil {
				return err
			}
			path, cfg, err := loadMCPConfig()
			if err != nil {
				return err
			}
			s, ok := cfg.Servers[name]
			if !ok {
				return unknownServerErr(path, name)
			}
			_, listed, err := inspectServer(cmd.Context(), name, s)
			if err != nil {
				return mcpCLIError(err)
			}
			idx := slices.IndexFunc(listed, func(t mcp.Tool) bool { return t.Name == toolName })
			if idx < 0 {
				return notFoundErr(fmt.Errorf("server %q does not list a tool named %q; run 'hollis mcp tools %s'", name, toolName, name))
			}
			tool := listed[idx]
			if tool.Filtered {
				return usageErr(fmt.Errorf("tool %q on server %q is excluded by its tools filter; update it with 'hollis mcp set %s --tool NAME' first", toolName, name, name))
			}
			if !tool.Offered() {
				return &cliError{code: 5, machineCode: string(mcp.CodeProtocol), err: fmt.Errorf("tool %q on server %q is unsupported (%s at %s) and is never offered to a model", toolName, name, tool.Reason, strconv.QuoteToASCII(tool.Pointer))}
			}
			if s.Mode() == mcp.ApprovalAuto && tool.Tier != mcp.TierValidated {
				return &cliError{code: 5, machineCode: string(mcp.CodeSchemaUnvalidated), err: fmt.Errorf("tool %q on server %q has a schema only the server can validate, so it cannot run automatically; keep the server in ask mode to use it", toolName, name)}
			}
			var updated mcp.Server
			err = mcp.Update(path, func(cfg *mcp.Config) error {
				current, ok := cfg.Servers[name]
				if !ok {
					return unknownServerErr(path, name)
				}
				if !current.Allowed(toolName) {
					current.Allow = append(current.Allow, toolName)
				}
				if current.Pins == nil {
					current.Pins = map[string]string{}
				}
				current.Pins[toolName] = tool.Pin
				cfg.Servers[name] = current
				updated = current
				return nil
			})
			if err != nil {
				return mcpCLIError(err)
			}
			return reportMCPChange(cmd, flags, path, name, "updated", updated)
		},
	}
}

func newMCPDenyCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "deny <server> <tool>",
		Short: "Remove a tool from the server's allow list",
		Long: `Remove a tool from the server's allow list and drop its pin.

An "auto" server whose allow list becomes empty is refused; switch it to ask
first with 'hollis mcp mode <server> ask'.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, toolName := args[0], args[1]
			if err := mcpServerName(name); err != nil {
				return err
			}
			path, err := mcpConfigPath()
			if err != nil {
				return configErr(err)
			}
			var updated mcp.Server
			err = mcp.Update(path, func(cfg *mcp.Config) error {
				current, ok := cfg.Servers[name]
				if !ok {
					return unknownServerErr(path, name)
				}
				if !current.Allowed(toolName) {
					return notFoundErr(fmt.Errorf("tool %q is not in the allow list of server %q", toolName, name))
				}
				current.Allow = slices.DeleteFunc(slices.Clone(current.Allow), func(t string) bool { return t == toolName })
				if len(current.Allow) == 0 {
					current.Allow = nil
				}
				if current.Pins != nil {
					pins := make(map[string]string, len(current.Pins))
					for k, v := range current.Pins {
						if k != toolName {
							pins[k] = v
						}
					}
					current.Pins = pins
					if len(pins) == 0 {
						current.Pins = nil
					}
				}
				cfg.Servers[name] = current
				updated = current
				return nil
			})
			if err != nil {
				return mcpCLIError(err)
			}
			return reportMCPChange(cmd, flags, path, name, "updated", updated)
		},
	}
}

func newMCPModeCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:       "mode <server> ask|auto",
		Short:     "Set a server's approval mode",
		Long:      "Set a server's approval mode. \"auto\" requires a non-empty allow list; add tools with 'hollis mcp allow <server> <tool>'.",
		Args:      cobra.ExactArgs(2),
		ValidArgs: []string{mcp.ApprovalAsk, mcp.ApprovalAuto},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, mode := args[0], args[1]
			if err := mcpServerName(name); err != nil {
				return err
			}
			if mode != mcp.ApprovalAsk && mode != mcp.ApprovalAuto {
				return usageErr(fmt.Errorf("approval mode must be %q or %q (got %q)", mcp.ApprovalAsk, mcp.ApprovalAuto, mode))
			}
			path, err := mcpConfigPath()
			if err != nil {
				return configErr(err)
			}
			var updated mcp.Server
			err = mcp.Update(path, func(cfg *mcp.Config) error {
				current, ok := cfg.Servers[name]
				if !ok {
					return unknownServerErr(path, name)
				}
				if mode == mcp.ApprovalAuto && len(current.Allow) == 0 {
					return &cliError{code: 10, machineCode: string(mcp.CodeConfig), err: fmt.Errorf("server %q has an empty allow list, so auto would run nothing; add tools first with 'hollis mcp allow %s <tool>'", name, name)}
				}
				current.Approval = mode
				cfg.Servers[name] = current
				updated = current
				return nil
			})
			if err != nil {
				return mcpCLIError(err)
			}
			return reportMCPChange(cmd, flags, path, name, "updated", updated)
		},
	}
}
