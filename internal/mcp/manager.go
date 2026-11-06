// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package mcp connects Hollis to MCP tool servers as a client.
//
// The official SDK is used as the JSON-RPC codec and nothing more. Hollis
// owns everything the safety contract depends on: the child process and its
// environment, the HTTP client and its TLS and redirect rules, every
// deadline (including Connect, which the SDK can overrun), the size caps,
// tool classification, and the approval decision before any call reaches a
// server.
//
// A Manager lives for one command. It attaches the servers it was asked for,
// offers their tools as a snapshot, executes one call at a time, and closes
// every session and kills every process group before the command exits.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// maxListPages bounds tools/list cursor pages per listing.
	maxListPages = 10
	// maxListBytes bounds one server's accumulated tools/list payload.
	maxListBytes = 4 << 20
	// minBudget is the smallest remaining command budget worth starting an
	// operation in; less is refused as a timeout.
	minBudget = time.Second
	// abandonWait bounds how long Hollis waits for an SDK call to return
	// after its transport has been killed. The call is abandoned after it.
	abandonWait = 2 * time.Second
	// sessionCloseWait bounds the SDK's session close, which for HTTP may
	// send a DELETE.
	sessionCloseWait = 6 * time.Second
	// rejectedByTransport is the SDK's code for a request its transport
	// failed to deliver; it says nothing about the server's protocol.
	rejectedByTransport = -32005
)

// Attachment is one server to attach.
type Attachment struct {
	Name   string
	Server Server
	// AdHoc servers come from the command line: always ask, no allowlist.
	AdHoc bool
}

// Options configure one Manager.
type Options struct {
	// Environ is the parent environment the stdio child environment is
	// scrubbed from. Nil uses os.Environ().
	Environ []string
	// ClientVersion is sent as the client implementation version.
	ClientVersion string
	// Interaction says whether ask mode may prompt.
	Interaction Interactivity
	// Approver answers ask-mode prompts when Interaction allows it.
	Approver Approver
	// CallTimeout overrides every server's call_timeout_ms when positive.
	CallTimeout time.Duration
}

// ServerStatus summarizes one attached server. It carries no secret and no
// server-controlled text other than the negotiated protocol version.
type ServerStatus struct {
	Name             string
	Transport        string
	Approval         string
	ProtocolVersion  string
	ToolsOffered     int
	ToolsUnsupported int
	// ToolsFiltered counts listed tools excluded by the configured filter.
	ToolsFiltered int
	// ToolsExcluded counts tools the SDK itself dropped from tools/list.
	ToolsExcluded int
	// StderrBytes counts bytes a stdio server wrote to stderr. The bytes are
	// never surfaced.
	StderrBytes int64
}

// CallRecord is the audit trail for one requested call.
type CallRecord struct {
	Server    string
	Tool      string
	Decision  Decision
	Arguments json.RawMessage
	// Executed is true once the call was sent to the server.
	Executed    bool
	ResultBytes int
	IsError     bool
	// ErrorCode is set when the call did not produce a result.
	ErrorCode Code
}

// Manager owns the attached sessions of one invocation.
type Manager struct {
	opts     Options
	sessions []*session
	byName   map[string]*session

	// callMu serializes calls: one call in flight overall.
	callMu sync.Mutex

	recMu   sync.Mutex
	records []CallRecord

	closed    atomic.Bool
	closeOnce sync.Once
}

type session struct {
	name   string
	cfg    Server
	adHoc  bool
	link   link
	client *sdk.ClientSession
	logs   *excludedCounter

	protocolVersion string

	// mu guards tools and order, which a re-list extends.
	mu    sync.Mutex
	tools map[string]Tool
	order []string

	failed    atomic.Bool
	closeOnce sync.Once
}

// Attach connects every requested server. It is all or nothing: if one
// server fails, every session that did attach is closed and the first
// failure in request order is returned.
func Attach(ctx context.Context, attachments []Attachment, opts Options) (*Manager, error) {
	if opts.Environ == nil {
		opts.Environ = os.Environ()
	}
	m := &Manager{opts: opts, byName: map[string]*session{}}
	for _, a := range attachments {
		if _, dup := m.byName[a.Name]; dup {
			return nil, newError(CodeConfig, a.Name, "", "the server is requested more than once")
		}
		m.byName[a.Name] = nil
	}
	results := make([]*session, len(attachments))
	errs := make([]error, len(attachments))
	var wg sync.WaitGroup
	for i, a := range attachments {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = attachOne(ctx, a, opts)
		}()
	}
	wg.Wait()
	for i := range attachments {
		if results[i] != nil {
			m.sessions = append(m.sessions, results[i])
			m.byName[results[i].name] = results[i]
		}
	}
	for _, err := range errs {
		if err != nil {
			m.Close()
			return nil, err
		}
	}
	m.limitSchemaText()
	if err := m.checkNamespace(); err != nil {
		m.Close()
		return nil, err
	}
	return m, nil
}

func attachOne(parent context.Context, a Attachment, opts Options) (*session, error) {
	cfg := a.Server
	if a.AdHoc {
		cfg.Approval, cfg.Allow, cfg.Pins = ApprovalAsk, nil, nil
	}
	if err := ValidateServer(a.Name, cfg); err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.ConnectTimeoutMS) * time.Millisecond
	if timeout == 0 {
		timeout = DefaultConnTimeout * time.Millisecond
	}
	ctx, cancel, err := budget(parent, timeout, a.Name, "", "connect")
	if err != nil {
		return nil, err
	}
	defer cancel()

	var l link
	if cfg.URL != "" {
		l, err = newHTTPLink(a.Name, cfg, lookupIn(opts.Environ))
	} else {
		l, err = startStdio(a.Name, cfg, opts.Environ)
	}
	if err != nil {
		return nil, err
	}
	s := &session{name: a.Name, cfg: cfg, adHoc: a.AdHoc, link: l, logs: &excludedCounter{}, tools: map[string]Tool{}}
	client := sdk.NewClient(&sdk.Implementation{Name: "hollis", Version: clientVersion(opts.ClientVersion)}, &sdk.ClientOptions{
		Logger:         slog.New(s.logs),
		Capabilities:   &sdk.ClientCapabilities{},
		MultiRoundTrip: &sdk.MultiRoundTripOptions{Disabled: true},
	})
	type connected struct {
		cs  *sdk.ClientSession
		err error
	}
	done := make(chan connected, 1)
	go func() {
		cs, err := client.Connect(ctx, l.transport(), nil)
		done <- connected{cs, err}
	}()
	var got connected
	select {
	case got = <-done:
	case <-ctx.Done():
		// The SDK's Connect can outlive its context; killing the transport
		// is what makes the deadline real.
		l.kill()
		select {
		case got = <-done:
			if got.cs != nil {
				_ = got.cs.Close()
			}
		case <-time.After(abandonWait):
		}
		l.close()
		return nil, deadlineError(parent, a.Name, "", "connect", timeout)
	}
	if got.err != nil {
		cerr := s.classify(got.err, "connect")
		l.close()
		if ctx.Err() != nil {
			return nil, deadlineError(parent, a.Name, "", "connect", timeout)
		}
		return nil, cerr
	}
	s.client = got.cs
	if init := got.cs.InitializeResult(); init != nil {
		s.protocolVersion = init.ProtocolVersion
		if init.Capabilities == nil || init.Capabilities.Tools == nil {
			s.close()
			return nil, newError(CodeProtocol, a.Name, "", "the server does not offer tools")
		}
	}
	listed, err := runBounded(ctx, s, func(ctx context.Context) ([]*sdk.Tool, error) { return s.list(ctx) })
	if err != nil {
		s.close()
		if ctx.Err() != nil {
			return nil, deadlineError(parent, a.Name, "", "connect", timeout)
		}
		return nil, err
	}
	if err := s.adopt(listed, false); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func clientVersion(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

func lookupIn(environ []string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		for i := len(environ) - 1; i >= 0; i-- {
			if value, ok := strings.CutPrefix(environ[i], name+"="); ok {
				return value, true
			}
		}
		return "", false
	}
}

// budget derives an operation context bounded by both the operation's own
// timeout and the caller's remaining budget, refusing to start when less
// than minBudget remains.
func budget(parent context.Context, timeout time.Duration, server, tool, op string) (context.Context, context.CancelFunc, error) {
	if err := parent.Err(); err != nil {
		return nil, nil, &Error{Code: CodeTimeout, Server: server, Tool: tool, Message: op + " was not started: the command was cancelled or its deadline passed", Err: err}
	}
	if deadline, ok := parent.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < minBudget {
			return nil, nil, &Error{Code: CodeTimeout, Server: server, Tool: tool, Message: fmt.Sprintf("%s was not started: %s of the command's --timeout remains, less than the %s minimum", op, remaining.Round(time.Millisecond), minBudget)}
		}
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	return ctx, cancel, nil
}

func deadlineError(parent context.Context, server, tool, op string, timeout time.Duration) *Error {
	if parent.Err() != nil {
		return &Error{Code: CodeTimeout, Server: server, Tool: tool, Message: op + " did not finish within the command's --timeout"}
	}
	return &Error{Code: CodeTimeout, Server: server, Tool: tool, Message: fmt.Sprintf("%s did not finish within %s", op, timeout)}
}

// list follows nextCursor itself, because the SDK returns one page.
func (s *session) list(ctx context.Context) ([]*sdk.Tool, error) {
	var all []*sdk.Tool
	cursor := ""
	total := 0
	for page := 1; ; page++ {
		if page > maxListPages {
			return nil, newError(CodeProtocol, s.name, "", "tools/list still returned a cursor after %d pages", maxListPages)
		}
		params := &sdk.ListToolsParams{}
		if cursor != "" {
			params.Cursor = cursor
		}
		result, err := s.client.ListTools(ctx, params)
		if err != nil {
			return nil, s.classify(err, "tools/list")
		}
		for _, tool := range result.Tools {
			encoded, _ := json.Marshal(tool)
			total += len(encoded)
		}
		if total > maxListBytes {
			return nil, newError(CodeProtocol, s.name, "", "tools/list returned more than %d bytes", maxListBytes)
		}
		all = append(all, result.Tools...)
		if len(all) > MaxToolsPerServer {
			return nil, newError(CodeProtocol, s.name, "", "the server lists more than %d tools; the limit is %d", len(all)-1, MaxToolsPerServer)
		}
		if result.NextCursor == "" {
			return all, nil
		}
		cursor = result.NextCursor
	}
}

// adopt classifies a listing. At attach it becomes the snapshot; on a
// re-list only names absent from the snapshot are added, marked late, and an
// existing name keeps its attach-time definition.
func (s *session) adopt(listed []*sdk.Tool, late bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, raw := range listed {
		if seen[raw.Name] {
			return newError(CodeProtocol, s.name, "", "tools/list names %q more than once", EscapeTerminal(raw.Name))
		}
		seen[raw.Name] = true
		if _, known := s.tools[raw.Name]; known && late {
			continue
		}
		schema, err := json.Marshal(raw.InputSchema)
		if err != nil || raw.InputSchema == nil {
			schema = []byte("null")
		}
		tool := classify(s.name, raw.Name, raw.Description, schema)
		tool.Late = late
		tool.Filtered = !s.cfg.Selected(tool.Name)
		if !late && !tool.Filtered && s.cfg.Mode() == ApprovalAuto && s.cfg.Allowed(tool.Name) && tool.Tier == TierServerValidated {
			return schemaUnvalidatedError(tool)
		}
		s.tools[tool.Name] = tool
		s.order = append(s.order, tool.Name)
	}
	if !late {
		for _, name := range s.cfg.Tools {
			if !seen[name] {
				return newError(CodeConfig, s.name, name, "configured tools filter names %q, but tools/list did not return it; check 'hollis mcp tools' or update the filter with 'hollis mcp set %s --tool NAME' (or --all-tools)", name, s.name)
			}
		}
	}
	return nil
}

func (m *Manager) limitSchemaText() {
	remaining := maxSchemaTextTotal
	// Preserve the offered snapshot before allocating any space to late tools.
	for _, late := range []bool{false, true} {
		for _, s := range m.sessions {
			s.mu.Lock()
			for _, name := range s.order {
				t := s.tools[name]
				if t.Filtered || t.Tier != TierServerValidated || t.Late != late {
					continue
				}
				if len(t.SchemaText) <= remaining {
					remaining -= len(t.SchemaText)
					continue
				}
				t.Tier, t.SchemaText = TierUnsupported, ""
				t.Reason = fmt.Sprintf("the schema text budget for server-validated tools (%d bytes per invocation) is exhausted", maxSchemaTextTotal)
				s.tools[name] = t
			}
			s.mu.Unlock()
		}
	}
}

func (m *Manager) checkNamespace() error {
	seen := map[string]Tool{}
	total := 0
	for _, s := range m.sessions {
		for _, tool := range s.snapshot(true) {
			total++
			if prior, ok := seen[tool.Rendered]; ok {
				return newError(CodeProtocol, tool.Server, tool.Name, "renders to %q, which collides with server %q tool %q; neither is offered", tool.Rendered, prior.Server, EscapeTerminal(prior.Name))
			}
			seen[tool.Rendered] = tool
		}
	}
	if total > MaxToolsTotal {
		return newError(CodeProtocol, "", "", "the attached servers offer %d tools; the limit is %d per invocation", total, MaxToolsTotal)
	}
	return nil
}

func (s *session) snapshot(offeredOnly bool) []Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Tool, 0, len(s.order))
	for _, name := range s.order {
		tool := s.tools[name]
		if tool.Late || (offeredOnly && !tool.Offered()) {
			continue
		}
		out = append(out, tool)
	}
	return out
}

func (s *session) lookup(name string) (Tool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tool, ok := s.tools[name]
	return tool, ok
}

// Tools returns the offered snapshot in request order, then each server's
// listing order. Late tools are never offered.
func (m *Manager) Tools() []Tool {
	var out []Tool
	for _, s := range m.sessions {
		out = append(out, s.snapshot(true)...)
	}
	return out
}

// ServerTools returns every tool a server listed at attach, unsupported ones
// included, for inspection.
func (m *Manager) ServerTools(server string) []Tool {
	if s := m.byName[server]; s != nil {
		return s.snapshot(false)
	}
	return nil
}

// Servers summarizes the attached servers in request order.
func (m *Manager) Servers() []ServerStatus {
	out := make([]ServerStatus, 0, len(m.sessions))
	for _, s := range m.sessions {
		status := ServerStatus{
			Name: s.name, Transport: s.cfg.Transport(), Approval: s.cfg.Mode(),
			ProtocolVersion: s.protocolVersion, ToolsExcluded: s.logs.count(),
			StderrBytes: s.link.facts().stderrBytes,
		}
		for _, tool := range s.snapshot(false) {
			if tool.Filtered {
				status.ToolsFiltered++
			} else if tool.Offered() {
				status.ToolsOffered++
			} else {
				status.ToolsUnsupported++
			}
		}
		out = append(out, status)
	}
	return out
}

// Lookup maps a model-facing name back to its tool.
func (m *Manager) Lookup(rendered string) (Tool, bool) {
	for _, tool := range m.Tools() {
		if tool.Rendered == rendered {
			return tool, true
		}
	}
	return Tool{}, false
}

// Records returns the audit trail of every requested call so far.
func (m *Manager) Records() []CallRecord {
	m.recMu.Lock()
	defer m.recMu.Unlock()
	return append([]CallRecord(nil), m.records...)
}

func (m *Manager) record(r CallRecord) {
	m.recMu.Lock()
	defer m.recMu.Unlock()
	m.records = append(m.records, r)
}

// Call runs one tool call through validation, approval and execution.
//
// Every error is an *Error. Only a CodeTimeout error leaves the run able to
// continue (the loop may tell the model the call failed); every other code
// stops the run. A refused call never reaches the server.
func (m *Manager) Call(ctx context.Context, server, tool string, arguments json.RawMessage) (Result, error) {
	m.callMu.Lock()
	defer m.callMu.Unlock()
	rec := CallRecord{Server: server, Tool: tool, Arguments: append(json.RawMessage(nil), arguments...)}
	result, err := m.call(ctx, server, tool, arguments, &rec)
	if err != nil {
		rec.ErrorCode = CodeOf(err)
	} else {
		rec.ResultBytes, rec.IsError = result.Bytes, result.IsError
	}
	m.record(rec)
	return result, err
}

func (m *Manager) call(ctx context.Context, server, name string, arguments json.RawMessage, rec *CallRecord) (Result, error) {
	if m.closed.Load() {
		return Result{}, newError(CodeUnavailable, server, name, "the MCP sessions are closed")
	}
	s := m.byName[server]
	if s == nil {
		return Result{}, newError(CodeUnknownTool, server, name, "no such attached server")
	}
	if s.failed.Load() {
		return Result{}, newError(CodeUnavailable, server, name, "the session was closed after an earlier failure")
	}
	// Enforce selection independently of the offered snapshot, before approval
	// or re-listing, even for invented names and non-interactive requests.
	if !s.cfg.Selected(name) {
		rec.Decision = DecisionRefused
		return Result{}, newError(CodeUnknownTool, server, name, "the tool is excluded by this server's tools filter")
	}
	// Ask mode without a way to ask is refused before any server round trip,
	// including the re-list below.
	if s.cfg.Mode() == ApprovalAsk && !m.opts.Interaction.Interactive {
		decision, err := m.policy(s).decide(ctx, Tool{Server: server, Name: name}, arguments)
		rec.Decision = decision
		return Result{}, err
	}
	tool, ok := s.lookup(name)
	if !ok {
		if err := m.relist(ctx, s); err != nil {
			return Result{}, err
		}
		if tool, ok = s.lookup(name); !ok {
			return Result{}, newError(CodeUnknownTool, server, name, "the server does not offer this tool")
		}
	}
	if !tool.Offered() {
		return Result{}, newError(CodeUnknownTool, server, name, "the tool is not offered: %s", tool.Reason)
	}
	if err := tool.ValidateArguments(arguments); err != nil {
		rec.Decision = DecisionRefused
		return Result{}, err
	}
	decision, err := m.policy(s).decide(ctx, tool, arguments)
	rec.Decision = decision
	if err != nil {
		return Result{}, err
	}
	return m.execute(ctx, s, tool, arguments, rec)
}

func (m *Manager) policy(s *session) policy {
	return policy{mode: s.cfg.Mode(), allow: s.cfg.Allowed, pins: s.cfg.Pins, interaction: m.opts.Interaction, approver: m.opts.Approver}
}

func (m *Manager) callTimeout(s *session) time.Duration {
	if m.opts.CallTimeout > 0 {
		return m.opts.CallTimeout
	}
	if s.cfg.CallTimeoutMS > 0 {
		return time.Duration(s.cfg.CallTimeoutMS) * time.Millisecond
	}
	return DefaultCallTimeout * time.Millisecond
}

func (m *Manager) relist(parent context.Context, s *session) error {
	timeout := m.callTimeout(s)
	ctx, cancel, err := budget(parent, timeout, s.name, "", "tools/list")
	if err != nil {
		return err
	}
	defer cancel()
	listed, err := runBounded(ctx, s, func(ctx context.Context) ([]*sdk.Tool, error) { return s.list(ctx) })
	if err != nil {
		if ctx.Err() != nil {
			return deadlineError(parent, s.name, "", "tools/list", timeout)
		}
		return err
	}
	if err := s.adopt(listed, true); err != nil {
		return err
	}
	m.limitSchemaText()
	return nil
}

func (m *Manager) execute(parent context.Context, s *session, tool Tool, arguments json.RawMessage, rec *CallRecord) (Result, error) {
	timeout := m.callTimeout(s)
	ctx, cancel, err := budget(parent, timeout, s.name, tool.Name, "the call")
	if err != nil {
		return Result{}, err
	}
	defer cancel()
	// Arguments stay raw so the server receives the exact bytes that were
	// approved; a decode to map[string]any would round large integers.
	params := &sdk.CallToolParams{Name: tool.Name, Arguments: json.RawMessage(arguments)}
	rec.Executed = true
	result, err := runBounded(ctx, s, func(ctx context.Context) (*sdk.CallToolResult, error) {
		return s.client.CallTool(ctx, params)
	})
	if err != nil {
		if ctx.Err() != nil {
			s.fail()
			return Result{}, deadlineError(parent, s.name, tool.Name, "the call", timeout)
		}
		classified := s.classify(err, "tools/call")
		classified.Tool = tool.Name
		s.fail()
		return Result{}, classified
	}
	if result.NeedsInput() {
		s.fail()
		return Result{}, newError(CodeProtocol, s.name, tool.Name, "the server asked for further input, which this release does not support")
	}
	return mapResult(s.name, tool.Name, result)
}

// runBounded runs op and returns at the deadline even if the SDK does not:
// on expiry the transport is killed, which ends the SDK call, and the call is
// abandoned if it still has not returned.
func runBounded[T any](ctx context.Context, s *session, op func(context.Context) (T, error)) (T, error) {
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		value, err := op(ctx)
		done <- outcome{value, err}
	}()
	select {
	case got := <-done:
		return got.value, got.err
	case <-ctx.Done():
		s.link.kill()
		select {
		case <-done:
		case <-time.After(abandonWait):
		}
		var zero T
		return zero, ctx.Err()
	}
}

// classify maps an SDK or transport failure to a code without quoting its
// message, which can carry server-controlled text.
func (s *session) classify(err error, op string) *Error {
	facts := s.link.facts()
	wrap := func(code Code, format string, args ...any) *Error {
		e := newError(code, s.name, "", format, args...)
		e.Err = err
		return e
	}
	var wire *jsonrpc.Error
	switch {
	case facts.redirected:
		return wrap(CodeProtocol, "%s: the server answered with a redirect, which Hollis never follows", op)
	case facts.httpStatus == 401 || facts.httpStatus == 403:
		return wrap(CodeAuth, "%s: the server rejected the request (HTTP %d); check the credential named in the server's auth settings", op, facts.httpStatus)
	case facts.bodyTooBig:
		return wrap(CodeResultTooLarge, "%s: an HTTP response exceeded %d bytes. Nothing was truncated", op, MaxHTTPBody)
	case facts.tlsFailed:
		return wrap(CodeUnavailable, "%s: the TLS connection failed certificate verification", op)
	case facts.sendFailed:
		return wrap(CodeUnavailable, "%s: the server could not be reached", op)
	case strings.Contains(err.Error(), "exceeded the configured maximum line length"):
		return wrap(CodeProtocol, "%s: a message from the server exceeded %d bytes", op, MaxFrameBytes)
	case strings.Contains(err.Error(), "event") && strings.Contains(err.Error(), "exceed"):
		return wrap(CodeProtocol, "%s: an event from the server exceeded %d bytes", op, MaxFrameBytes)
	case errors.As(err, &wire) && wire.Code != rejectedByTransport:
		return wrap(CodeProtocol, "%s: the server answered with JSON-RPC error %d", op, wire.Code)
	case facts.httpStatus >= 300:
		return wrap(CodeUnavailable, "%s: the server answered HTTP %d", op, facts.httpStatus)
	case facts.exited:
		return wrap(CodeUnavailable, "%s: the server process exited (status %d)", op, facts.exitCode)
	case errors.Is(err, sdk.ErrConnectionClosed) || errors.Is(err, os.ErrClosed) || strings.Contains(err.Error(), "EOF"):
		return wrap(CodeUnavailable, "%s: the server closed the connection", op)
	case strings.Contains(err.Error(), "unsupported protocol version"):
		return wrap(CodeProtocol, "%s: the server negotiated a protocol version this client does not support", op)
	default:
		return wrap(CodeUnavailable, "%s failed", op)
	}
}

func (s *session) fail() {
	s.failed.Store(true)
	s.close()
}

func (s *session) close() {
	s.closeOnce.Do(func() {
		if s.client != nil {
			done := make(chan struct{})
			go func() {
				_ = s.client.Close()
				close(done)
			}()
			// The SDK's close waits for in-flight requests. A stdio child is
			// given one grace period before its process group is killed,
			// which ends those requests; an HTTP session gets the SDK's own
			// bounded DELETE.
			wait := sessionCloseWait
			if s.cfg.Transport() == "stdio" {
				wait = stopGrace
			}
			select {
			case <-done:
			case <-time.After(wait):
				s.link.kill()
				select {
				case <-done:
				case <-time.After(abandonWait):
				}
			}
		}
		s.link.close()
	})
}

// Close ends every session and kills every stdio process group. It is safe
// to call concurrently with Call and more than once; an in-flight call fails.
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.closed.Store(true)
		var wg sync.WaitGroup
		for _, s := range m.sessions {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.close()
			}()
		}
		wg.Wait()
	})
}

// excludedCounter is the SDK's logger. It never prints; it counts the tools
// the SDK silently drops from tools/list so the drop can be reported.
type excludedCounter struct {
	n atomic.Int64
}

func (c *excludedCounter) Enabled(context.Context, slog.Level) bool { return true }

func (c *excludedCounter) Handle(_ context.Context, record slog.Record) error {
	if strings.HasPrefix(record.Message, "excluding") && strings.Contains(record.Message, "tools/list") {
		c.n.Add(1)
	}
	return nil
}

func (c *excludedCounter) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *excludedCounter) WithGroup(string) slog.Handler      { return c }
func (c *excludedCounter) count() int                         { return int(c.n.Load()) }
