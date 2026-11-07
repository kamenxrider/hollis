// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kamenxrider/hollis/internal/mcp"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
	"github.com/spf13/cobra"
)

type mcpFlags struct {
	names          []string
	servers        []string
	timeout        time.Duration
	disabled       bool
	partialResults bool
}

func (f *mcpFlags) addTo(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&f.names, "mcp", nil, "Attach a configured MCP server on model local; repeat for more")
	cmd.Flags().StringArrayVar(&f.servers, "mcp-server", nil, "Ad-hoc MCP command with quoted arguments, or HTTP URL; always ask, no shell")
	cmd.Flags().DurationVar(&f.timeout, "mcp-timeout", 0, "Override MCP call deadlines (positive, at most 120s, within the command deadline)")
	cmd.Flags().BoolVar(&f.disabled, "no-mcp", false, "Never read MCP configuration or attach servers, even with MCP flags")
	cmd.Flags().BoolVar(&f.partialResults, "partial-results", false, "Allow disclosed partial Local MCP results; overrides the saved partial-results setting")
}

func (f *mcpFlags) enabled() bool { return !f.disabled && len(f.names)+len(f.servers) > 0 }

func (f *mcpFlags) validate(cmd *cobra.Command) error {
	if cmd.Flags().Changed("mcp-timeout") && (f.timeout <= 0 || f.timeout > 120*time.Second) {
		return usageErr(errors.New("--mcp-timeout must be positive and at most 120s"))
	}
	if !f.enabled() {
		if cmd.Flags().Changed("mcp-timeout") && !f.disabled {
			return usageErr(errors.New("--mcp-timeout requires --mcp or --mcp-server"))
		}
		return nil
	}
	if len(f.names)+len(f.servers) > mcp.MaxServers {
		return usageErr(fmt.Errorf("MCP attachment count exceeds %d", mcp.MaxServers))
	}
	seen := map[string]bool{}
	for _, name := range f.names {
		if err := mcpServerName(name); err != nil {
			return err
		}
		if seen[name] {
			return usageErr(fmt.Errorf("MCP server %q was requested more than once", name))
		}
		seen[name] = true
	}
	for _, flag := range []string{"schema", "image", "code", "generate-image", "image-style", "image-bridge", "image-reference", "output"} {
		if cmd.Flags().Changed(flag) {
			return usageErr(fmt.Errorf("MCP tools cannot be combined with --%s", flag))
		}
	}
	if stream, _ := cmd.Flags().GetBool("stream"); cmd.Flags().Changed("stream") && stream {
		return usageErr(errors.New("MCP tools are complete-only and cannot be combined with --stream"))
	}
	return nil
}

// splitMCPCommand recognizes quotes and escapes, never expansion or a shell.
func splitMCPCommand(value string) ([]string, error) {
	if len(value) > 1<<20 || !utf8.ValidString(value) {
		return nil, usageErr(errors.New("invalid or oversized --mcp-server definition"))
	}
	var words []string
	var word strings.Builder
	var quote rune
	started, escaped := false, false
	for _, r := range value {
		if unicode.IsControl(r) {
			return nil, usageErr(errors.New("--mcp-server must not contain control characters"))
		}
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			started, escaped = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			started, quote = true, r
		case unicode.IsSpace(r):
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
		default:
			started = true
			word.WriteRune(r)
		}
	}
	if quote != 0 || escaped {
		return nil, usageErr(errors.New("--mcp-server has an unfinished quote or escape"))
	}
	if started {
		words = append(words, word.String())
	}
	if len(words) == 0 || words[0] == "" {
		return nil, usageErr(errors.New("--mcp-server requires a command or URL"))
	}
	return words, nil
}

func (f *mcpFlags) attachments() ([]mcp.Attachment, error) {
	attachments := make([]mcp.Attachment, 0, len(f.names)+len(f.servers))
	if len(f.names) != 0 {
		_, cfg, err := loadMCPConfig()
		if err != nil {
			return nil, err
		}
		for _, name := range f.names {
			server, ok := cfg.Servers[name]
			if !ok {
				return nil, notFoundErr(fmt.Errorf("no configured MCP server named %q; run 'hollis mcp list'", name))
			}
			attachments = append(attachments, mcp.Attachment{Name: name, Server: server})
		}
	}
	for i, value := range f.servers {
		words, err := splitMCPCommand(value)
		if err != nil {
			return nil, err
		}
		server := mcp.Server{Approval: mcp.ApprovalAsk}
		if strings.HasPrefix(words[0], "https://") || strings.HasPrefix(words[0], "http://") {
			if len(words) != 1 {
				return nil, usageErr(errors.New("an HTTP --mcp-server definition cannot have arguments"))
			}
			server.URL = words[0]
		} else {
			server.Command, server.Args = words[0], words[1:]
		}
		name := fmt.Sprintf("adhoc-%d", i+1)
		if err := mcp.ValidateServer(name, server); err != nil {
			// Ad-hoc definitions can contain credentials in invalid inputs.
			return nil, usageErr(errors.New("invalid --mcp-server definition; use a program with arguments or a credential-free HTTP URL"))
		}
		attachments = append(attachments, mcp.Attachment{Name: name, Server: server, AdHoc: true})
	}
	return attachments, nil
}

// Tests replace terminal access, not the approval policy.
var (
	mcpTTYAvailable              = mcp.TTYAvailable
	mcpApprover     mcp.Approver = mcp.TerminalApprover{}
)

type mcpSession struct {
	options        *runner.GenerationOptions
	manager        *mcp.Manager
	offers         []runner.ToolOffer
	tools          map[string]mcp.Tool
	framer         *mcp.Framer
	calls          []mcpCallAudit
	dispositions   []mcpDisposition
	round          int
	partialResults bool
	boundResults   bool
	bounded        []boundedToolResult
	diagnostic     io.Writer
}

func (f *mcpFlags) attach(ctx context.Context, cmd *cobra.Command, flags *rootFlags, callers []runner.Tool) (*mcpSession, error) {
	if !f.enabled() {
		return nil, nil
	}
	partial := f.partialResults
	if !cmd.Flags().Changed("partial-results") {
		config, err := loadConfig()
		if err != nil {
			return nil, configErr(err)
		}
		partial = config.PartialResults != nil && *config.PartialResults
	}
	attachments, err := f.attachments()
	if err != nil {
		return nil, err
	}
	manager, err := connectMCP(ctx, attachments, mcp.Options{
		Environ: mcpEnviron(), ClientVersion: version, CallTimeout: f.timeout,
		Interaction: mcp.DetectInteractivity(flags.asJSON, flags.noInput || flags.agent, false, mcpTTYAvailable),
		Approver:    mcpApprover,
	})
	if err != nil {
		return nil, mcpCLIError(err)
	}
	session := &mcpSession{manager: manager, tools: map[string]mcp.Tool{}, calls: []mcpCallAudit{}, dispositions: []mcpDisposition{}}
	session.partialResults = partial
	session.diagnostic = cmd.ErrOrStderr()
	if flags.quiet {
		session.diagnostic = io.Discard
	}
	ok := false
	defer func() {
		if !ok {
			session.Close()
		}
	}()
	session.framer, err = mcp.NewFramer()
	if err != nil {
		return nil, transportErr(errors.New("could not initialize MCP result framing"))
	}
	seen := map[string]bool{}
	for _, tool := range callers {
		seen[tool.Name] = true
	}
	tools := manager.Tools()
	if len(callers) > runner.MaxTools || len(callers)+len(tools) > mcp.MaxToolsTotal {
		return nil, codedUsageErr("tool_count_limit", fmt.Errorf("the combined offer has %d tools (%d caller); limits are %d combined and %d caller", len(callers)+len(tools), len(callers), mcp.MaxToolsTotal, runner.MaxTools))
	}
	for _, tool := range tools {
		if seen[tool.Rendered] {
			return nil, codedUsageErr("tool_name_conflict", fmt.Errorf("tool name %q is offered by more than one source", tool.Rendered))
		}
		seen[tool.Rendered] = true
		schema := tool.InputSchema
		if tool.Tier == mcp.TierServerValidated {
			schema = json.RawMessage(tool.SchemaText)
		}
		offer, err := runner.NewMCPToolOffer(tool.Rendered, tool.Description, schema, tool.Tier == mcp.TierServerValidated)
		if err != nil {
			return nil, executionErr("mcp_protocol", "an offered MCP definition could not be admitted by the tool loop")
		}
		session.offers = append(session.offers, offer)
		session.tools[tool.Rendered] = tool
	}
	for _, status := range manager.Servers() {
		fmt.Fprintf(session.diagnostic, "MCP %s: %d offered, %d unsupported, %d excluded by SDK, %d filtered out (approval %s)\n",
			status.Name, status.ToolsOffered, status.ToolsUnsupported, status.ToolsExcluded, status.ToolsFiltered, status.Approval)
		for _, tool := range manager.ServerTools(status.Name) {
			if tool.Tier == mcp.TierUnsupported && !tool.Filtered {
				fmt.Fprintf(session.diagnostic, "MCP unsupported %s/%s: %s\n", status.Name, mcp.EscapeTerminal(tool.Name), mcp.EscapeTerminal(tool.Reason))
			}
		}
	}
	ok = true
	return session, nil
}

func (s *mcpSession) Close() {
	if s != nil && s.manager != nil {
		s.manager.Close()
	}
}

type mcpServerStatus struct {
	Name             string `json:"name"`
	Transport        string `json:"transport"`
	Approval         string `json:"approval"`
	ToolsOffered     int    `json:"tools_offered"`
	ToolsUnsupported int    `json:"tools_unsupported"`
	ToolsExcluded    int    `json:"tools_excluded"`
	ToolsFiltered    int    `json:"tools_filtered"`
}

type mcpCallAudit struct {
	ID          string          `json:"id"`
	Round       int             `json:"round"`
	Server      string          `json:"server"`
	Tool        string          `json:"tool"`
	Source      string          `json:"source"`
	Decision    mcp.Decision    `json:"decision,omitempty"`
	Arguments   json.RawMessage `json:"arguments"`
	Executed    bool            `json:"executed"`
	ResultBytes int             `json:"result_bytes"`
	IsError     bool            `json:"is_error"`
	ErrorCode   mcp.Code        `json:"error_code,omitempty"`
}

type mcpDisposition struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	State    string `json:"state"`
	Executed bool   `json:"executed"`
}

type mcpRunMetadata struct {
	Servers      []mcpServerStatus   `json:"servers"`
	ToolCalls    []mcpCallAudit      `json:"tool_calls"`
	Dispositions []mcpDisposition    `json:"dispositions"`
	Bounded      []boundedToolResult `json:"tool_results_bounded,omitempty"`
}

func (s *mcpSession) metadata() mcpRunMetadata {
	meta := mcpRunMetadata{Servers: []mcpServerStatus{}, ToolCalls: []mcpCallAudit{}, Dispositions: []mcpDisposition{}}
	if s == nil {
		return meta
	}
	for _, status := range s.manager.Servers() {
		meta.Servers = append(meta.Servers, mcpServerStatus{status.Name, status.Transport, status.Approval, status.ToolsOffered, status.ToolsUnsupported, status.ToolsExcluded, status.ToolsFiltered})
	}
	meta.ToolCalls = append(meta.ToolCalls, s.calls...)
	meta.Dispositions = append(meta.Dispositions, s.dispositions...)
	meta.Bounded = append(meta.Bounded, s.bounded...)
	return meta
}

func (meta mcpRunMetadata) addTo(data map[string]any) {
	data["mcp_servers"], data["tool_calls"], data["tool_dispositions"] = meta.Servers, meta.ToolCalls, meta.Dispositions
	if len(meta.Bounded) > 0 {
		data["tool_results_bounded"] = meta.Bounded
	}
}

func (s *mcpSession) ExecuteTools(ctx context.Context, calls []runner.ToolCall, admit func(runner.ToolResult) error) (runner.ToolExecution, error) {
	s.round++
	execution := runner.ToolExecution{}
	start := len(s.dispositions)
	for _, call := range calls {
		source, state := "caller", "pending"
		if _, ok := s.tools[call.Name]; ok {
			source, state = "mcp", "not_dispatched"
		}
		s.dispositions = append(s.dispositions, mcpDisposition{ID: call.ID, Source: source, State: state})
	}
	for i, call := range calls {
		tool, isMCP := s.tools[call.Name]
		if !isMCP {
			execution.Pending = append(execution.Pending, call)
			continue
		}
		result, err := s.manager.Call(ctx, tool.Server, tool.Name, json.RawMessage(call.Arguments))
		records := s.manager.Records()
		record := records[len(records)-1]
		s.calls = append(s.calls, mcpCallAudit{
			ID: call.ID, Round: s.round, Server: record.Server, Tool: record.Tool, Source: "mcp",
			Decision: record.Decision, Arguments: record.Arguments, Executed: record.Executed,
			ResultBytes: record.ResultBytes, IsError: record.IsError, ErrorCode: record.ErrorCode,
		})
		disposition := &s.dispositions[start+i]
		disposition.Executed = record.Executed // A dispatch attempt, not confirmed transmission.
		if err != nil {
			disposition.State = "failed"
			return execution, mcpCLIError(err)
		}
		disposition.State = "completed"
		framed, err := s.admitResult(tool, call.ID, result, admit)
		execution.Results = append(execution.Results, framed)
		if err != nil {
			return execution, err
		}
	}
	return execution, nil
}

func (s *mcpSession) run(ctx context.Context, r runner.Runner, messages []store.Message, callers []runner.Tool, choice runner.ToolChoice) (runner.ToolOutcome, error) {
	roundRunner, ok := r.(runner.ToolRoundRunner)
	if !ok {
		return runner.ToolOutcome{}, transportErr(errors.New("configured runner does not support native-local tool calling"))
	}
	s.round = 0
	_, checked := r.(interface {
		CheckToolHistory(context.Context, runner.NativeToolStep) error
	})
	// Never shorten data without the native SDK preflight.
	s.boundResults = s.partialResults && checked
	safety := 0
	if s.boundResults {
		safety = 128
	}
	callStart := len(s.calls)
	// An empty catalog never fabricates definitions or enables tool detection.
	if len(callers)+len(s.offers) == 0 && (choice == runner.ToolChoiceAuto || choice == runner.ToolChoiceNone) {
		choice = runner.ToolChoiceNone
	}
	outcome, err := runner.RunToolLoop(ctx, roundRunner, runner.ModelLocal, runner.ToolConversation{
		Tools: callers, Offers: s.offers, Choice: choice, Messages: messages, Options: s.options,
		ResultSafetyTokens: safety,
	}, s)
	var classified *cliError
	var loopError *runner.Error
	if errors.As(err, &loopError) && loopError.Kind == runner.KindContextCapacity && len(s.calls) > callStart {
		err = executionErr(string(mcp.CodeResultTooLarge), toCLIError(err).Error())
	}
	if err != nil && !errors.As(err, &classified) {
		err = toCLIError(err)
	}
	return outcome, err
}

func writeMCPFailure(cmd *cobra.Command, flags *rootFlags, session *mcpSession, err error, mixed bool) error {
	if err == nil || ErrorReported(err) || (!mixed && !flags.asJSON && !flags.agent) {
		return err
	}
	payload := map[string]any{"error": commandErrorFields(err)}
	meta := session.metadata()
	if mixed {
		payload["mcp"] = meta
		if encodeErr := writeToolDocument(cmd, payload); encodeErr != nil {
			return encodeErr
		}
		return &reportedError{err: err}
	} else {
		data := map[string]any{}
		meta.addTo(data)
		projected, projectErr := projectOutput(data, flags)
		if projectErr != nil {
			return projectErr
		}
		if flags.agent {
			payload["meta"], payload["results"] = agentMeta(), projected
		} else if fields, ok := projected.(map[string]any); ok {
			for key, value := range fields {
				payload[key] = value
			}
		}
	}
	if encodeErr := encodeJSON(cmd.OutOrStdout(), payload); encodeErr != nil {
		return encodeErr
	}
	return &reportedError{err: err}
}

func runRespondMCP(ctx context.Context, cmd *cobra.Command, flags *rootFlags, r runner.Runner, prompt string, callers []runner.Tool, choice runner.ToolChoice, options *mcpFlags, sampling ...*runner.GenerationOptions) error {
	session, err := options.attach(ctx, cmd, flags, callers)
	if err != nil {
		return writeMCPFailure(cmd, flags, nil, err, callers != nil)
	}
	defer session.Close()
	if len(sampling) > 0 {
		session.options = sampling[0]
	}
	outcome, err := session.run(ctx, r, []store.Message{{Role: "user", Content: prompt}}, callers, choice)
	if err != nil {
		return writeMCPFailure(cmd, flags, session, err, callers != nil)
	}
	if callers != nil {
		document := toolDocument(outcome)
		document["mcp"] = session.metadata()
		return writeToolDocument(cmd, document)
	}
	if flags.asJSON {
		data := map[string]any{"model_requested": "local", "model_used": outcome.Model, "response": outcome.Text}
		addRetryAttempts(ctx, data)
		if outcome.Usage != nil {
			data["usage"] = outcome.Usage
		}
		session.metadata().addTo(data)
		return printJSONFilteredTo(cmd.OutOrStdout(), data, flags)
	}
	fmt.Fprint(cmd.OutOrStdout(), humanTerminalText(cmd.OutOrStdout(), outcome.Text))
	if !strings.HasSuffix(outcome.Text, "\n") {
		fmt.Fprintln(cmd.OutOrStdout())
	}
	return nil
}
