// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	// MaxApprovalArgs bounds the rendered arguments in an approval prompt.
	// Larger arguments are refused instead of shown in part.
	MaxApprovalArgs = 32 << 10
	// AnswerTimeout is how long the prompt waits before treating silence as
	// a denial.
	AnswerTimeout = 120 * time.Second
)

// Decision is what happened to one call before execution.
type Decision string

const (
	DecisionApproved         Decision = "approved"
	DecisionAllowlisted      Decision = "allowlisted"
	DecisionDenied           Decision = "denied"
	DecisionApprovalRequired Decision = "approval_required"
	DecisionNotAllowlisted   Decision = "not_allowlisted"
	DecisionRefused          Decision = "refused"
)

// Request is what an approver is asked about. Arguments are the complete
// JSON object that would be sent.
type Request struct {
	Server    string
	Tool      string
	Arguments json.RawMessage
	// ServerValidated is true for a tier-2 tool, whose arguments Hollis did
	// not validate.
	ServerValidated bool
	// Late is true when the tool appeared after the session started.
	Late bool
}

// Approver answers one approval request. Any error, and a false answer, is a
// denial.
type Approver interface {
	Approve(ctx context.Context, prompt string, req Request) (bool, error)
}

// Interactivity says whether an approver may be asked at all. When it is
// false, ask mode refuses before rendering a prompt or reaching the server.
type Interactivity struct {
	Interactive bool
	// Reason names why the invocation cannot ask, for the refusal message.
	Reason string
}

// DetectInteractivity applies the fail-closed rules: JSON output, --no-input
// (which --agent implies), batch, and a missing controlling terminal each
// make ask mode refuse. The answer is always read from the controlling
// terminal, so piped standard input is never a reason to ask and never an
// answer.
func DetectInteractivity(jsonOutput, noInput, batch bool, ttyAvailable func() bool) Interactivity {
	switch {
	case batch:
		return NonInteractive("batch jobs are unattended")
	case noInput:
		return NonInteractive("--no-input (or --agent) is set")
	case jsonOutput:
		return NonInteractive("--json output is a machine channel")
	}
	if ttyAvailable == nil {
		ttyAvailable = TTYAvailable
	}
	if !ttyAvailable() {
		return NonInteractive("no controlling terminal is available to ask on")
	}
	return Interactivity{Interactive: true}
}

// NonInteractive builds the refusal state for one reason.
func NonInteractive(reason string) Interactivity {
	return Interactivity{Reason: reason}
}

// policy is the approval decision for one call, independent of transport.
type policy struct {
	mode        string
	allow       func(string) bool
	pins        map[string]string
	interaction Interactivity
	approver    Approver
}

// decide returns nil when the call may run, or the coded refusal. It never
// contacts the server.
func (p policy) decide(ctx context.Context, tool Tool, args json.RawMessage) (Decision, error) {
	if p.mode == ApprovalAuto {
		if tool.Late || !p.allow(tool.Name) {
			reason := "is not in the server's \"allow\" list"
			if tool.Late {
				reason = "appeared after the session started, so its allowlist entry was never reviewed against it"
			}
			return DecisionNotAllowlisted, &Error{Code: CodeNotAllowlisted, Server: tool.Server, Tool: tool.Name, Message: fmt.Sprintf(
				"the tool %s. Nothing was run. To allow it, run 'hollis mcp allow %s %s' (servers.%s.allow in mcp.json)", reason, tool.Server, tool.Name, tool.Server)}
		}
		if tool.Tier != TierValidated {
			return DecisionRefused, schemaUnvalidatedError(tool)
		}
		pin := p.pins[tool.Name]
		if pin == "" {
			return DecisionNotAllowlisted, &Error{Code: CodeNotAllowlisted, Server: tool.Server, Tool: tool.Name, Message: fmt.Sprintf(
				"the allowlist entry has no reviewed definition pin, so it cannot run automatically. Nothing was run. Run 'hollis mcp allow %s %s' to review and pin it", tool.Server, tool.Name)}
		}
		if pin != tool.Pin {
			return DecisionRefused, &Error{Code: CodeDefinitionChanged, Server: tool.Server, Tool: tool.Name, Message: fmt.Sprintf(
				"the tool's name, description or schema changed since it was allowlisted. Nothing was run. Review it with 'hollis mcp tools %s', then run 'hollis mcp allow %s %s' to pin the new definition", tool.Server, tool.Server, tool.Name)}
		}
		return DecisionAllowlisted, nil
	}

	if !p.interaction.Interactive {
		reason := p.interaction.Reason
		if reason == "" {
			reason = "this invocation is not interactive"
		}
		return DecisionApprovalRequired, &Error{Code: CodeApprovalRequired, Server: tool.Server, Tool: tool.Name, Message: fmt.Sprintf(
			"needs approval, and %s. Nothing was run. Approve it in an interactive run, add the tool to the server's \"allow\" list with automatic approval, or rerun with --no-mcp", reason)}
	}
	prompt, err := RenderPrompt(Request{Server: tool.Server, Tool: tool.Name, Arguments: args, ServerValidated: tool.Tier == TierServerValidated, Late: tool.Late})
	if err != nil {
		return DecisionRefused, err
	}
	if p.approver == nil {
		return DecisionDenied, &Error{Code: CodeDenied, Server: tool.Server, Tool: tool.Name, Message: "no approver is available. Nothing was run"}
	}
	ok, err := p.approver.Approve(ctx, prompt, Request{Server: tool.Server, Tool: tool.Name, Arguments: args, ServerValidated: tool.Tier == TierServerValidated, Late: tool.Late})
	if err != nil || !ok {
		return DecisionDenied, &Error{Code: CodeDenied, Server: tool.Server, Tool: tool.Name, Message: "the call was not approved. Nothing was run and the run stopped", Err: err}
	}
	return DecisionApproved, nil
}

func schemaUnvalidatedError(tool Tool) *Error {
	return &Error{Code: CodeSchemaUnvalidated, Server: tool.Server, Tool: tool.Name, Message: fmt.Sprintf(
		"is allowlisted for automatic approval, but its schema can only be validated by the server. Nothing was run. Switch the server to ask ('hollis mcp mode %s ask') or remove the entry ('hollis mcp deny %s %s')", tool.Server, tool.Server, tool.Name)}
}

// RenderPrompt lays out the approval prompt. The arguments are shown in full,
// pretty-printed from the exact bytes that would be sent, with characters that
// could drive a terminal escaped. Arguments over MaxApprovalArgs are refused.
func RenderPrompt(req Request) (string, error) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, req.Arguments, "", "  "); err != nil {
		return "", &Error{Code: CodeInvalidArguments, Server: req.Server, Tool: req.Tool, Message: "arguments are not valid JSON"}
	}
	shown := EscapeTerminal(pretty.String())
	if len(shown) > MaxApprovalArgs {
		return "", &Error{Code: CodeApprovalArgsLarge, Server: req.Server, Tool: req.Tool, Message: fmt.Sprintf(
			"the arguments are %d bytes when displayed; the approval prompt shows at most %d and never a fragment. Nothing was run", len(shown), MaxApprovalArgs)}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "MCP tool call — server %s, tool %s\n", quoteTerminal(req.Server), quoteTerminal(req.Tool))
	b.WriteString("Arguments:\n")
	b.WriteString(shown)
	b.WriteString("\n")
	if req.ServerValidated {
		b.WriteString("Hollis did not validate these arguments; the server will.\n")
	}
	if req.Late {
		b.WriteString("This tool appeared after the session started.\n")
	}
	b.WriteString("\nRun this call? [y/N] ")
	return b.String(), nil
}

func quoteTerminal(value string) string {
	return EscapeTerminal(fmt.Sprintf("%q", value))
}

// EscapeTerminal escapes control, C1, bidi and zero-width characters, keeping
// newlines and tabs.
func EscapeTerminal(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || isInvisible(r) || r == '\u2028' || r == '\u2029':
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Affirmative reports whether an answer approves: "y" or "yes", any case,
// surrounding whitespace ignored. Everything else is a denial.
func Affirmative(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

// TerminalApprover asks on the controlling terminal. It reads the answer only
// from the terminal it opened, never from standard input, so piped input can
// never answer a prompt.
type TerminalApprover struct {
	// Open returns the terminal. Nil opens /dev/tty.
	Open func() (io.ReadWriteCloser, error)
	// Timeout overrides AnswerTimeout when positive.
	Timeout time.Duration
}

// OpenTTY opens the controlling terminal for reading and writing.
func OpenTTY() (io.ReadWriteCloser, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}

// TTYAvailable reports whether a controlling terminal can be opened.
func TTYAvailable() bool {
	tty, err := OpenTTY()
	if err != nil {
		return false
	}
	_ = tty.Close()
	return true
}

func (a TerminalApprover) Approve(ctx context.Context, prompt string, _ Request) (bool, error) {
	open := a.Open
	if open == nil {
		open = OpenTTY
	}
	tty, err := open()
	if err != nil {
		return false, fmt.Errorf("open terminal: %w", err)
	}
	defer tty.Close()
	if _, err := io.WriteString(tty, prompt); err != nil {
		return false, err
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = AnswerTimeout
	}
	type answer struct {
		line string
		err  error
	}
	answers := make(chan answer, 1)
	go func() {
		line, err := bufio.NewReader(io.LimitReader(tty, 4096)).ReadString('\n')
		answers <- answer{line: line, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case got := <-answers:
		if got.err != nil {
			return false, got.err
		}
		return Affirmative(got.line), nil
	case <-timer.C:
		_, _ = io.WriteString(tty, "\nNo answer; the call was not run.\n")
		return false, context.DeadlineExceeded
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
