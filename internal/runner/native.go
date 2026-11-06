// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kamenxrider/hollis/internal/subproc"
)

// NativeProtocol and NativeVersion are the matched helper contract this CLI
// speaks. Protocol 3 adds capability advertisement and named refusals, not new
// model features. Older request formats remain accepted by the helper, but
// strict version identity rejects mismatched CLI/helper pairs in either direction.
const NativeProtocol = 4
const NativeVersion = "0.7.0"
const NativeMaxPromptBytes = 128 << 10
const nativeMaxRequestBytes = 1 << 20
const nativeMaxOutputBytes = 8 << 20

// ValidProtocol reports whether n is a usable helper protocol pin: a positive
// integer no greater than 99. Plugins, the packager and the runtime-record
// producer enforce the same bound, so a pin outside it is an invalid receipt
// rather than a protocol era to guess at.
func ValidProtocol(n int) bool { return n >= 1 && n <= 99 }

// platformProbeTimeout bounds the sw_vers platform probe independently of the
// invoking call's deadline. Variable so offline fixtures can shorten it.
var platformProbeTimeout = 5 * time.Second

// helperProbeTimeout bounds the non-generative --protocol-version probe that
// guards a guided-output request. Variable so offline fixtures can shorten it.
var helperProbeTimeout = 5 * time.Second

// swVersPath is the platform probe binary. Variable so offline fixtures can
// substitute a stand-in without invoking the real probe.
var swVersPath = "/usr/bin/sw_vers"

// LocalStatus describes a non-generative availability check, not inference proof.
// ContextSize is the measured context window, present only where the helper
// reports it; absent signals stay absent rather than zero-filled.
type LocalStatus struct {
	Available   bool   `json:"available"`
	Reason      string `json:"reason,omitempty"`
	Protocol    int    `json:"protocol"`
	Version     string `json:"version"`
	ContextSize int    `json:"context_size,omitempty"`
	// Capabilities is the protocol-3 advertisement. Nil means the helper
	// reported none, which is not the same as a helper that reported nothing
	// supported: both refuse, but only the second can name a reason.
	Capabilities *Capabilities `json:"capabilities,omitempty"`
}

// Capabilities is the protocol-3 capability advertisement. Each block stands on
// its own: an absent block means the helper does not offer that capability, and
// a present block may still say supported:false with a fixed reason. Nothing
// here is guessed — a zero value is never read as a capability.
type Capabilities struct {
	Extract    *ExtractCapability `json:"extract,omitempty"`
	Images     *ImageCapability   `json:"images,omitempty"`
	Tools      *ToolCapability    `json:"tools,omitempty"`
	Vision     *bool              `json:"vision,omitempty"`
	MacOSMajor *int               `json:"macos_major,omitempty"`
}

// ImageCapability reports native attachment support and its limits. MaxSidePX
// is a longest-side pixel length, not a pixel count.
type ImageCapability struct {
	Supported bool     `json:"supported"`
	Reason    string   `json:"reason,omitempty"`
	MaxImages int      `json:"max_images,omitempty"`
	Formats   []string `json:"formats,omitempty"`
	MaxSidePX int      `json:"max_side_px,omitempty"`
}

// ToolCapability reports tool-calling support. ToolCallingMode is the narrower
// promise: whether the helper honors the tool_calling request member at all. A
// request that asks for a mode the helper cannot apply must be refused, never
// served as a plain completion that silently dropped it.
type ToolCapability struct {
	Executable        bool   `json:"executable"`
	TurnHandoff       bool   `json:"turn_handoff"`
	Supported         bool   `json:"supported"`
	Reason            string `json:"reason,omitempty"`
	ToolCallingMode   bool   `json:"tool_calling_mode"`
	NativeDefinitions bool   `json:"native_definitions"`
	HybridTools       bool   `json:"hybrid_tools"`
}

// capabilityReasons are the fixed reason identifiers the protocol defines. Only
// these may reach user-facing text: a reason is a protocol token, never
// diagnostic text echoed from a helper. A capability this build simply does not
// implement is absent from the advertisement instead of carrying a reason.
var capabilityReasons = map[string]bool{
	"macos_26":        true,
	"no_vision":       true,
	"not_implemented": true,
	"no_tool_calling": true,
}

func fixedReason(reason string) string {
	if capabilityReasons[reason] {
		return reason
	}
	return ""
}

// capabilityRefusal names the capability a request needed and, when the helper
// gave a fixed reason, that reason. The remedy is the same either way.
func capabilityRefusal(capability, reason string) *Error {
	err := nativeError(KindUnsupportedCapability, "")
	err.Capability, err.CapabilityReason = capability, fixedReason(reason)
	err.Err = errors.New(err.CapabilityRefusalMessage())
	return err
}

// CapabilityRefusalMessage renders only closed-set identifiers, never helper
// diagnostics. CLI and HTTP share this wording even when the error is wrapped.
func (e *Error) CapabilityRefusalMessage() string {
	capability := "a capability this request requires"
	if e.Capability == "tools" || e.Capability == "images" || e.Capability == "extract" {
		capability = e.Capability
	}
	message := "native helper does not support " + capability
	if reason := fixedReason(e.CapabilityReason); reason != "" {
		message += " (" + reason + ")"
	}
	return message + "; install the matching bundle"
}

// decodeCapabilities reads the advertisement leniently. Unknown members are
// ignored so a later capability flag needs no protocol bump, while the values
// this client does read are still range-checked.
func decodeCapabilities(raw json.RawMessage) (*Capabilities, error) {
	var caps Capabilities
	if err := json.Unmarshal(raw, &caps); err != nil {
		return nil, nativeError(KindNativeProtocol, "invalid native capabilities")
	}
	if caps.Images != nil {
		if caps.Images.MaxImages < 0 || caps.Images.MaxSidePX < 0 {
			return nil, nativeError(KindNativeProtocol, "invalid native capabilities")
		}
		caps.Images.Reason = fixedReason(caps.Images.Reason)
	}
	if caps.Tools != nil {
		caps.Tools.Reason = fixedReason(caps.Tools.Reason)
	}
	if caps.Extract != nil && (caps.Extract.MaxPages < 0 || caps.Extract.MaxTextBytes < 0) {
		return nil, nativeError(KindNativeProtocol, "invalid native capabilities")
	}
	if caps.MacOSMajor != nil && *caps.MacOSMajor <= 0 {
		return nil, nativeError(KindNativeProtocol, "invalid native capabilities")
	}
	return &caps, nil
}

// requireAdvertised refuses a request whose capability the helper did not
// advertise. The status event arrives before the helper touches the model, so
// this costs no inference, and reading it from that same process rather than a
// cached pre-spawn probe means the answer cannot go stale against a replaced
// helper.
func requireAdvertised(status LocalStatus, opts nativeOptions) *Error {
	if opts.ToolStep != nil {
		if tools := status.Capabilities.tools(); tools != nil && tools.Supported && tools.Executable && tools.TurnHandoff {
			return nil
		}
		return capabilityRefusal("tools", status.Capabilities.toolsReason())
	}
	if opts.Source != "" {
		return requireExtract(status, opts)
	}
	if len(opts.Images) > 0 {
		return requireImages(status)
	}
	if opts.ToolCalling == "" {
		return nil
	}
	if tools := status.Capabilities.tools(); tools != nil && tools.Supported && tools.ToolCallingMode {
		if (!opts.HybridTools || tools.HybridTools) && (len(opts.ToolDefinitions) == 0 || tools.NativeDefinitions) {
			return nil
		}
	}
	return capabilityRefusal("tools", status.Capabilities.toolsReason())
}

func (c *Capabilities) tools() *ToolCapability {
	if c == nil {
		return nil
	}
	return c.Tools
}

func (c *Capabilities) toolsReason() string {
	if tools := c.tools(); tools != nil {
		return tools.Reason
	}
	return ""
}

type NativeStatusRunner interface {
	NativeStatus(context.Context) (LocalStatus, error)
}

// NativeRunner invokes only the helper beside the resolved Hollis executable.
// Fields used to substitute fake processes remain private to provider-free tests.
type NativeRunner struct {
	Timeout       time.Duration
	helperPath    string
	platformCheck func(context.Context) error
	// probed remembers successfully negotiated file identities. Replacements
	// and in-place changes must be probed again, including in a running server.
	mu     sync.Mutex
	probed map[string]os.FileInfo
}

func NewNative() *NativeRunner {
	return &NativeRunner{Timeout: DefaultTimeout}
}

func (r *NativeRunner) helper(ctx context.Context) (string, error) {
	check := nativePlatform
	if r.platformCheck != nil {
		check = r.platformCheck
	}
	if err := check(ctx); err != nil {
		return "", err
	}
	path := r.helperPath
	if path == "" {
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			return "", err
		}
		path = filepath.Join(filepath.Dir(executable), "hollis-native")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", errors.New("native local helper is missing or not executable; install the matching Hollis Apple Silicon bundle")
	}
	return path, nil
}

func nativePlatform(ctx context.Context) error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nativeDiagnosticError(KindLocalUnavailable, "platform_unsupported", "native local requires Apple Silicon and macOS 27 or later")
	}
	// The probe is bounded by its own deadline as well as the caller's: a wedged
	// sw_vers must not hold a status or completion call open.
	result, err := subproc.Run(ctx, subproc.Spec{
		Name:      swVersPath,
		Args:      []string{"-productVersion"},
		Timeout:   platformProbeTimeout,
		StdoutCap: 4096,
	})
	if err != nil || result.WaitErr != nil {
		return nativeDiagnosticError(KindLocalUnavailable, "platform_unknown", "could not determine macOS version for native local")
	}
	major, err := strconv.Atoi(strings.Split(strings.TrimSpace(string(result.Stdout)), ".")[0])
	if err != nil {
		return nativeDiagnosticError(KindLocalUnavailable, "platform_unknown", "could not determine macOS version for native local")
	}
	if major < 27 {
		return nativeDiagnosticError(KindLocalUnavailable, "platform_unsupported", "native local requires macOS 27 or later")
	}
	return nil
}

func (r *NativeRunner) Run(ctx context.Context, model Model, prompt string) (string, Model, error) {
	result, err := r.RunComplete(ctx, model, prompt)
	return result.Text, model, err
}
func (r *NativeRunner) RunComplete(ctx context.Context, model Model, prompt string) (Completion, error) {
	result, _, err := r.invoke(ctx, model, "complete", prompt, nil, nil, nativeOptions{})
	return result, err
}

// RunCompleteWithOptions submits one prompt with the protocol-3 request members
// the caller needs. Tool rounds use it; an ordinary request uses RunComplete and
// sends none of them.
func (r *NativeRunner) RunCompleteWithOptions(ctx context.Context, model Model, prompt string, opts nativeOptions) (Completion, error) {
	result, _, err := r.invoke(ctx, model, "complete", prompt, nil, nil, opts)
	return result, err
}
func (r *NativeRunner) Stream(ctx context.Context, model Model, prompt string, emit func(string) error) (Completion, error) {
	if emit == nil {
		return Completion{}, nativeError(KindUsage, "stream callback is required")
	}
	result, _, err := r.invoke(ctx, model, "stream", prompt, emit, nil, nativeOptions{})
	return result, err
}

// RunSchema requests guided output. The schema must already be parsed: there is
// no path here that forwards an unvalidated schema to the helper.
func (r *NativeRunner) RunSchema(ctx context.Context, model Model, prompt string, schema *Schema) (Completion, error) {
	if schema == nil {
		return Completion{Model: model}, nativeError(KindUsage, "guided output requires a schema")
	}
	result, _, err := r.invoke(ctx, model, "schema", prompt, nil, schema, nativeOptions{})
	return result, err
}

func (r *NativeRunner) NativeStatus(ctx context.Context) (LocalStatus, error) {
	_, status, err := r.invoke(ctx, ModelLocal, "status", "", nil, nil, nativeOptions{})
	return status, err
}

func nativeError(kind Kind, message string) *Error {
	return &Error{Kind: kind, ExitCode: -1, Err: errors.New(message)}
}

func nativeDiagnosticError(kind Kind, reason, message string) *Error {
	err := nativeError(kind, message)
	err.NativeReason = reason
	return err
}

func nativeContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nativeError(KindTimeout, "native local request timed out")
	}
	if ctx.Err() != nil {
		return nativeError(KindContextCanceled, "native local request canceled")
	}
	return nil
}

// negotiate asks the resolved helper which protocol it speaks before any
// operation spawns a request. Every operation is probed, `status` included: a
// helper that disagrees about the era is a component fault, and serving an
// older helper's status would report a usable route that cannot answer the
// request that follows. The probe is the non-generative --protocol-version
// command; its output never travels further than the parsed integer, and the
// result is cached per unchanged file identity so repeated calls probe once.
func (r *NativeRunner) negotiate(ctx context.Context, path string) error {
	// An already-expired deadline belongs to the caller, not to the probe.
	if err := nativeContextError(ctx); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nativeError(KindNativeFailed, "could not inspect native helper")
	}
	r.mu.Lock()
	known, cached := r.probed[path]
	r.mu.Unlock()
	if cached && sameHelperFile(known, info) {
		return nil
	}
	timeout := helperProbeTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, runErr := subproc.Run(probeCtx, subproc.Spec{
		Name:      path,
		Args:      []string{"--protocol-version"},
		StdoutCap: 4096,
		StderrCap: -1,
	})
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		// No model request happened, so the generic "native local request
		// timed out" wording (and the CLI's raise-your-timeout hint) would
		// describe something that never occurred. The probe has its own
		// deadline and its own sentence.
		re := nativeError(KindTimeout, "native helper did not answer --protocol-version within the probe deadline")
		re.ProbeTimeout = true
		return re
	}
	if probeCtx.Err() != nil {
		return nativeError(KindContextCanceled, "native local request canceled")
	}
	if runErr != nil || !res.Started || res.WaitErr != nil {
		return nativeError(KindNativeFailed, "native helper could not report its supported protocol")
	}
	reported, convErr := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	if convErr != nil || !ValidProtocol(reported) {
		return nativeError(KindNativeFailed, "native helper reported an unsupported protocol version")
	}
	if reported != NativeProtocol {
		// A paired helper that speaks another era is a version mismatch, not a
		// bad invocation: the caller asked for a supported operation and the
		// installed component cannot serve it. Both directions map to
		// native_protocol/exit 5, so the exit code does not depend on which
		// side of the version the helper sits.
		return helperMismatch()
	}
	after, err := os.Stat(path)
	if err != nil || !sameHelperFile(info, after) {
		return helperMismatch()
	}
	r.mu.Lock()
	if r.probed == nil {
		r.probed = map[string]os.FileInfo{}
	}
	r.probed[path] = info
	r.mu.Unlock()
	return nil
}

func sameHelperFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// helperMismatch is the component-mismatch refusal: the helper answered
// validly, in another protocol. The remedy is the matching bundle.
func helperMismatch() *Error {
	return nativeDiagnosticError(KindNativeProtocol, "helper_mismatch", "native helper does not match this Hollis version; install the matching bundle")
}

// GuidedOutputRefusal is the schema-surface spelling of the same component
// mismatch. It is exported so CLI and server tests can pin the exit-code
// contract against the value the transport raises; the sentence is shared with
// every other operation now that all of them negotiate first.
func GuidedOutputRefusal() *Error { return helperMismatch() }

type nativeRequest struct {
	GenerationOptions  *GenerationOptions  `json:"generation_options,omitempty"`
	Protocol           int                 `json:"protocol"`
	Operation          string              `json:"operation"`
	Prompt             string              `json:"prompt,omitempty"`
	Schema             json.RawMessage     `json:"schema,omitempty"`
	ToolCalling        string              `json:"tool_calling,omitempty"`
	ToolInstructions   string              `json:"tool_instructions,omitempty"`
	ToolDefinitions    []ToolDefinition    `json:"tool_definitions,omitempty"`
	RawToolDefinitions []RawToolDefinition `json:"raw_tool_definitions,omitempty"`
	HybridTools        bool                `json:"hybrid_tools,omitempty"`
	// Source, Pages and Media belong to extract alone.
	Source *nativeSource  `json:"source,omitempty"`
	Pages  []int          `json:"pages,omitempty"`
	Media  string         `json:"media,omitempty"`
	Images []nativeSource `json:"images,omitempty"`
}

// nativeOptions carries the request members beyond prompt and schema. The zero
// value is a plain generation request: every member is omitted unless a caller
// asked for it.
type nativeOptions struct {
	ToolStep          *NativeToolStep
	GenerationOptions *GenerationOptions
	// ToolCalling is "allowed", "disallowed", or empty. It is refused outright
	// when the helper did not advertise that it applies the mode, because a
	// helper that ignores it would serve a plain answer over a tool request.
	ToolCalling        string
	ToolInstructions   string
	ToolDefinitions    []ToolDefinition
	RawToolDefinitions []RawToolDefinition
	HybridTools        bool
	// Source is the staged absolute path of an extract request, with its page
	// selection and media hint.
	Source string
	Pages  []int
	Media  string
	Images []nativeSource
}

func (o nativeOptions) validate() error {
	if o.ToolStep != nil {
		if o.ToolCalling != "" || o.hasToolMetadata() || o.Source != "" || len(o.Images) > 0 || o.GenerationOptions != nil {
			return nativeError(KindUsage, "native tool steps cannot mix legacy generation fields")
		}
		return o.ToolStep.validate()
	}
	if err := o.GenerationOptions.Validate(); err != nil {
		return err
	}
	switch o.ToolCalling {
	case "", "allowed", "disallowed":
	default:
		return nativeError(KindUsage, "native tool_calling accepts only allowed or disallowed")
	}
	if o.ToolInstructions == "" && len(o.ToolDefinitions) == 0 && len(o.RawToolDefinitions) == 0 && !o.HybridTools {
		return nil
	}
	instructions := toolInstructions
	if o.HybridTools {
		instructions = hybridToolInstructions
	}
	count := len(o.ToolDefinitions) + len(o.RawToolDefinitions)
	if o.ToolCalling != "allowed" || o.ToolInstructions != instructions || count == 0 || count > MaxToolOffers || (!o.HybridTools && (len(o.RawToolDefinitions) > 0 || count > MaxTools)) {
		return nativeError(KindUsage, "tool metadata requires allowed mode, fixed instructions and bounded source-aware offers")
	}
	seen := map[string]bool{}
	callers, rawBytes := 0, 0
	for _, tool := range o.ToolDefinitions {
		if !toolNamePattern.MatchString(tool.Name) || seen[tool.Name] || (tool.Source != "mcp" && strings.TrimSpace(tool.Description) == "") || !utf8.ValidString(tool.Description) {
			return nativeError(KindUsage, "invalid native tool definition")
		}
		seen[tool.Name] = true
		switch tool.Source {
		case "":
			callers++
		case "mcp":
			if !o.HybridTools {
				return nativeError(KindUsage, "MCP source requires hybrid capability")
			}
		default:
			return nativeError(KindUsage, "invalid native tool source")
		}
		if _, err := ParseSchema(tool.Parameters); err != nil {
			return nativeError(KindUsage, "invalid native tool parameters")
		}
	}
	if callers > MaxTools {
		return nativeError(KindUsage, "too many caller tool definitions")
	}
	for _, tool := range o.RawToolDefinitions {
		if tool.Source != "mcp" || seen[tool.Name] || len(tool.Schema) > MaxRawToolSchemaBytes {
			return nativeError(KindUsage, "invalid raw tool source or duplicate name")
		}
		if _, err := NewMCPToolOffer(tool.Name, tool.Description, json.RawMessage(tool.Schema), true); err != nil {
			return nativeError(KindUsage, "invalid raw tool definition")
		}
		rawBytes += len(tool.Schema)
		if rawBytes > MaxRawToolSchemasBytes {
			return nativeError(KindUsage, "raw tool schemas exceed aggregate byte limit")
		}
		seen[tool.Name] = true
	}
	return nil
}

func (o nativeOptions) hasToolMetadata() bool {
	return len(o.ToolDefinitions) > 0 || len(o.RawToolDefinitions) > 0 || o.HybridTools
}

type nativeUsage struct {
	InputTokens     *int `json:"input_tokens"`
	OutputTokens    *int `json:"output_tokens"`
	ReasoningTokens *int `json:"reasoning_tokens"`
}

func (u *nativeUsage) valid() bool {
	return u != nil && u.InputTokens != nil && u.OutputTokens != nil && u.ReasoningTokens != nil && *u.InputTokens >= 0 && *u.OutputTokens >= 0 && *u.ReasoningTokens >= 0 && *u.ReasoningTokens <= *u.OutputTokens
}

// NativeFailureInfo contains identifiers and integers only, never SDK descriptions.
type NativeFailureInfo struct {
	SwiftType        string `json:"swift_type"`
	Domain           string `json:"domain"`
	Code             *int   `json:"code"`
	CallbackCaptured *bool  `json:"callback_captured"`
}

func (info *NativeFailureInfo) retryable() bool {
	if info == nil || info.Code == nil {
		return false
	}
	if info.Domain == "FoundationModels.LanguageModelError" && *info.Code == -1 {
		return true
	}
	manager := info.Domain == "ModelManagerError" || strings.HasSuffix(info.Domain, ".ModelManagerError") ||
		info.SwiftType == "ModelManagerError" || strings.HasSuffix(info.SwiftType, ".ModelManagerError")
	return manager && *info.Code == 1012
}

func nativeErrorIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 192 {
		return false
	}
	for _, c := range []byte(value) {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

type nativeEvent struct {
	Budget        map[string]int     `json:"budget,omitempty"`
	ErrorInfo     *NativeFailureInfo `json:"error_info,omitempty"`
	CheckedTokens *int               `json:"input_tokens,omitempty"`
	RequestID     string             `json:"request_id,omitempty"`
	Step          *int               `json:"step,omitempty"`
	Calls         []NativeToolCall   `json:"calls,omitempty"`
	Protocol      int                `json:"protocol"`
	Version       string             `json:"version"`
	Model         Model              `json:"model"`
	Event         string             `json:"event"`
	Available     *bool              `json:"available,omitempty"`
	Reason        string             `json:"reason,omitempty"`
	Text          *string            `json:"text,omitempty"`
	Usage         *nativeUsage       `json:"usage,omitempty"`
	Kind          Kind               `json:"kind,omitempty"`
	Capability    string             `json:"capability,omitempty"`
	// ContextSize is the measured context window a protocol-2 status reports.
	ContextSize *int `json:"context_size,omitempty"`
	// Capabilities is the protocol-3 advertisement, kept as raw JSON so the
	// strict event decoder above never descends into it: a capability added
	// inside the subtree is additive data, not a protocol error. It is read
	// leniently by decodeCapabilities, which still validates what it uses.
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
	// Structured is the guided-output object. It is accepted only on a
	// terminal complete event for a schema request, and only after this
	// package re-validates it against the requested schema.
	Structured json.RawMessage `json:"structured,omitempty"`
	// Extraction members: media, page_count, pages and text_bytes on an extract
	// completion; page, page_count, limit and actual on a failure.
	Media     string       `json:"media,omitempty"`
	PageCount *int         `json:"page_count,omitempty"`
	Pages     []nativePage `json:"pages,omitempty"`
	TextBytes *int         `json:"text_bytes,omitempty"`
	Page      *int         `json:"page,omitempty"`
	Limit     *int         `json:"limit,omitempty"`
	Actual    *int         `json:"actual,omitempty"`
}

// invoke retries one busy tool decision only before any callback was captured.
// It never truncates or repairs output. Complete events must
// be followed by clean EOF and a successful process exit before success is returned.
func (r *NativeRunner) invoke(parent context.Context, model Model, operation, prompt string, emit func(string) error, schema *Schema, opts nativeOptions) (Completion, LocalStatus, error) {
	result := Completion{Model: model}
	status := LocalStatus{}
	fail := func(err error) (Completion, LocalStatus, error) { return Completion{Model: model}, status, err }
	if model != ModelLocal {
		return fail(nativeError(KindUsage, "native backend only supports model local"))
	}
	if operation == "schema" && schema == nil {
		return fail(nativeError(KindUsage, "guided output requires a schema"))
	}
	if err := opts.validate(); err != nil {
		return fail(err)
	}
	if (operation == "extract") != (opts.Source != "") || (operation == "extract" && !filepath.IsAbs(opts.Source)) {
		return fail(nativeError(KindUsage, "native extract requires one staged absolute source"))
	}
	if len(opts.Images) > 0 {
		if (operation != "complete" && operation != "stream") || schema != nil || opts.ToolCalling != "" ||
			len(opts.Images) != NativeMaxImages || !filepath.IsAbs(opts.Images[0].Path) {
			return fail(nativeError(KindUsage, "native images require one staged absolute image on plain generation"))
		}
	}
	if opts.hasToolMetadata() && (operation != "complete" || schema != nil || len(opts.Images) > 0 || opts.Source != "") {
		return fail(nativeError(KindUsage, "native definitions require complete generation"))
	}
	if operation != "status" && operation != "extract" && operation != "tool_step" && operation != "tool_check" {
		if strings.TrimSpace(prompt) == "" {
			return fail(nativeError(KindEmptyPrompt, "empty prompt"))
		}
		if !utf8.ValidString(prompt) {
			return fail(nativeError(KindUsage, "native prompt must be valid UTF-8"))
		}
		size := (ToolRoundRequest{Prompt: prompt, Instructions: opts.ToolInstructions, Definitions: opts.ToolDefinitions, RawDefinitions: opts.RawToolDefinitions}).inputBytes()
		if size > NativeMaxPromptBytes {
			if !opts.hasToolMetadata() {
				return fail(nativeError(KindContextCapacity, "rendered conversation exceeds Hollis's 128 KiB input limit; no messages were removed"))
			}
			failure := nativeError(KindContextCapacity, "rendered conversation and tool metadata exceed Hollis's 128 KiB input limit; no messages were removed")
			failure.PromptBytes = size
			return fail(failure)
		}
	}
	var requestSchema json.RawMessage
	if schema != nil {
		requestSchema = schema.Bytes()
	}
	wire := nativeRequest{Protocol: NativeProtocol, Operation: operation, Prompt: prompt, Schema: requestSchema, ToolCalling: opts.ToolCalling, ToolInstructions: opts.ToolInstructions, ToolDefinitions: opts.ToolDefinitions, RawToolDefinitions: opts.RawToolDefinitions, HybridTools: opts.HybridTools, Images: opts.Images}
	if opts.Source != "" {
		wire.Source, wire.Pages, wire.Media = &nativeSource{Path: opts.Source}, opts.Pages, opts.Media
	}
	request, err := json.Marshal(wire)
	if opts.ToolStep != nil {
		request, err = json.Marshal(opts.ToolStep)
	} else {
		wire.GenerationOptions = opts.GenerationOptions
		request, err = json.Marshal(wire)
	}
	if err != nil {
		return fail(nativeError(KindTransport, "could not encode native request"))
	}
	if len(request)+1 > nativeMaxRequestBytes {
		return fail(nativeError(KindContextCapacity, "encoded native request exceeds the 1 MiB limit; nothing was truncated"))
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if deadline, ok := parent.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := nativeContextError(ctx); err != nil {
		return fail(err)
	}
	// Old helpers must never receive a metadata generation request. This
	// bounded status request shares the deadline and performs no tokenization.
	if opts.hasToolMetadata() || opts.ToolStep != nil {
		_, advertised, err := r.invoke(ctx, model, "status", "", nil, nil, nativeOptions{})
		if err != nil {
			return fail(err)
		}
		if !advertised.Available {
			return fail(nativeError(KindLocalUnavailable, "native local model is unavailable"))
		}
		if err := requireAdvertised(advertised, opts); err != nil {
			return fail(err)
		}
	}
	path, err := r.helper(ctx)
	if err != nil {
		if ctxErr := nativeContextError(ctx); ctxErr != nil {
			return fail(ctxErr)
		}
		var nativeErr *Error
		if errors.As(err, &nativeErr) {
			return fail(err)
		}
		return fail(nativeError(KindLocalUnavailable, err.Error()))
	}
	if err := r.negotiate(ctx, path); err != nil {
		return fail(err)
	}
	// The helper's stderr can contain framework diagnostics. Do not propagate or
	// accumulate it: protocol errors provide deliberately prompt-free diagnostics.
	ready, terminal := false, false
	budgetSeen := false
	previous := ""
	total := 0
	handle := func(line []byte) error {
		total += len(line)
		if total > 64*nativeMaxOutputBytes {
			return nativeError(KindNativeProtocol, "native output exceeded protocol limit")
		}
		if terminal {
			return nativeError(KindNativeProtocol, "native helper emitted data after completion")
		}
		if !utf8.Valid(line) {
			return nativeError(KindNativeProtocol, "native event is not valid UTF-8")
		}
		if _, err := decodeToolDocument(line); err != nil {
			return nativeError(KindNativeProtocol, "native event is not strict JSON")
		}
		var event nativeEvent
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			return nativeError(KindNativeProtocol, "invalid native event")
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return nativeError(KindNativeProtocol, "extra native event data")
		}
		if event.Protocol <= 0 || event.Version == "" || event.Model == "" || event.Event == "" {
			return nativeError(KindNativeProtocol, "incomplete native event identity")
		}
		if event.Protocol != NativeProtocol || event.Version != NativeVersion || event.Model != ModelLocal {
			return nativeDiagnosticError(KindNativeProtocol, "helper_mismatch", "native helper version, protocol or model mismatch; install the matching bundle")
		}
		if opts.ToolStep != nil {
			if event.RequestID != opts.ToolStep.RequestID {
				return nativeError(KindNativeProtocol, "native tool request identity mismatch")
			}
		} else if event.RequestID != "" {
			return nativeError(KindNativeProtocol, "unexpected native request identity")
		}
		if (event.Step != nil || len(event.Calls) > 0) && event.Event != "tool_calls" {
			return nativeError(KindNativeProtocol, "unexpected native tool batch")
		}
		if event.CheckedTokens != nil && event.Event != "checked" {
			return nativeError(KindNativeProtocol, "unexpected native preflight counts")
		}
		if event.Budget != nil && event.Event != "budget" {
			return nativeError(KindNativeProtocol, "unexpected native budget")
		}
		if info := event.ErrorInfo; info != nil {
			if event.Event != "error" || !nativeErrorIdentifier(info.SwiftType) ||
				!nativeErrorIdentifier(info.Domain) || info.Code == nil || info.CallbackCaptured == nil {
				return nativeError(KindNativeProtocol, "invalid native error classification")
			}
		}
		if event.Capability != "" && (event.Event != "error" || event.Kind != KindUnsupportedCapability) {
			return nativeError(KindNativeProtocol, "unexpected native capability refusal")
		}
		// Signals the design pins to one place in the sequence: the context
		// window belongs to status, and a structured object belongs to a
		// terminal guided completion. Anything else is a protocol error, never
		// a value to be picked up wherever it appears.
		if event.ContextSize != nil && event.Event != "status" {
			return nativeError(KindNativeProtocol, "unexpected native context size")
		}
		if len(event.Capabilities) != 0 && event.Event != "status" {
			return nativeError(KindNativeProtocol, "unexpected native capabilities")
		}
		if len(event.Structured) != 0 && event.Event != "complete" {
			return nativeError(KindNativeProtocol, "unexpected structured output")
		}
		extractData := event.PageCount != nil || len(event.Pages) != 0 || event.TextBytes != nil || event.Media != ""
		failureData := event.Page != nil || event.Limit != nil || event.Actual != nil
		if (extractData || failureData) && operation != "extract" {
			return nativeError(KindNativeProtocol, "unexpected native extraction data")
		}
		if (len(event.Pages) != 0 || event.TextBytes != nil) && event.Event != "complete" {
			return nativeError(KindNativeProtocol, "unexpected native extraction data")
		}
		if failureData && event.Event != "error" {
			return nativeError(KindNativeProtocol, "unexpected native extraction data")
		}
		switch event.Event {
		case "budget":
			if !ready || budgetSeen || opts.ToolStep == nil || os.Getenv("HOLLIS_DEBUG_BUDGET") != "1" ||
				event.Text != nil || event.Usage != nil || event.Kind != "" || !validNativeBudget(event.Budget) {
				return nativeError(KindNativeProtocol, "invalid native budget")
			}
			budgetSeen = true
			slog.Info("native_budget", "counts", event.Budget)
		case "checked":
			if !ready || operation != "tool_check" || event.CheckedTokens == nil || *event.CheckedTokens < 0 ||
				event.Text != nil || event.Usage != nil || event.Kind != "" {
				return nativeError(KindNativeProtocol, "invalid native history preflight")
			}
			terminal = true
		case "tool_calls":
			if !ready || operation != "tool_step" || opts.ToolStep == nil || event.Step == nil || *event.Step != opts.ToolStep.Step ||
				event.Text != nil || event.Usage != nil || event.Kind != "" || len(event.Structured) > 0 ||
				len(event.Calls) == 0 || len(event.Calls) > MaxToolCallsPerRound {
				return nativeError(KindNativeProtocol, "invalid native tool handoff")
			}
			seen := map[string]bool{}
			allowed := map[string]bool{}
			for _, tool := range opts.ToolStep.Tools {
				allowed[tool.Name] = true
			}
			for _, call := range event.Calls {
				if !validNativeCallID(call.ID) || seen[call.ID] || !allowed[call.Name] {
					return nativeError(KindNativeProtocol, "invalid native tool call identity")
				}
				seen[call.ID] = true
			}
			result.NativeCalls = event.Calls
			terminal = true
		case "status":
			if ready || event.Available == nil {
				return nativeError(KindNativeProtocol, "invalid native status sequence")
			}
			// Build and fully validate the candidate before it becomes the
			// reported status. A rejected event must not leave a half-filled
			// status behind: the failure result carries it, and a diagnostic
			// caller must never see available:true from an event we refused.
			candidate := LocalStatus{Available: *event.Available, Reason: event.Reason, Protocol: event.Protocol, Version: event.Version}
			if event.ContextSize != nil {
				if *event.ContextSize <= 0 {
					return nativeError(KindNativeProtocol, "invalid native context size")
				}
				candidate.ContextSize = *event.ContextSize
			}
			if len(event.Capabilities) != 0 {
				capabilities, err := decodeCapabilities(event.Capabilities)
				if err != nil {
					return err
				}
				candidate.Capabilities = capabilities
			}
			// A request that needs a capability waits for the advertisement
			// before it proceeds: the helper has not touched the model yet, so
			// the refusal below costs no inference.
			if err := requireAdvertised(candidate, opts); err != nil {
				return err
			}
			ready = true
			status = candidate
			if operation == "status" {
				terminal = true
			} else if operation != "extract" && !status.Available {
				// Extraction makes no model call, so an unavailable model
				// does not stop it; the generation that follows reports it.
				return nativeError(KindLocalUnavailable, "native local model is unavailable: "+event.Reason)
			}
		case "snapshot":
			if !ready || operation != "stream" || event.Text == nil {
				return nativeError(KindNativeProtocol, "unexpected native snapshot")
			}
			if !strings.HasPrefix(*event.Text, previous) {
				return nativeError(KindNativeProtocol, "native model revised earlier streamed text; response incomplete")
			}
			delta := (*event.Text)[len(previous):]
			if delta != "" {
				if err := emit(delta); err != nil {
					return err
				}
			}
			previous = *event.Text
		case "complete":
			if operation == "tool_check" {
				return nativeError(KindNativeProtocol, "history preflight returned a generation terminal")
			}
			if ready && operation == "extract" {
				extraction, err := readExtraction(event, opts)
				if err != nil {
					return err
				}
				result.extraction = extraction
				terminal = true
				return nil
			}
			if !ready || operation == "status" || event.Text == nil || *event.Text == "" {
				return nativeError(KindNativeProtocol, "invalid native completion")
			}
			if operation == "stream" {
				if *event.Text != previous || event.Usage != nil {
					return nativeError(KindNativeProtocol, "stream completion does not match snapshots or includes unqualified usage")
				}
			} else if !event.Usage.valid() {
				return nativeError(KindNativeProtocol, "native complete response lacks valid measured usage")
			}
			if operation == "schema" {
				// The helper's SDK decode is necessary but not trusted: the
				// returned object is re-validated here against the same
				// requested subset. No retry, repair or fallback.
				if len(event.Structured) == 0 {
					return nativeError(KindNativeProtocol, "native guided response lacks structured output")
				}
				if err := schema.Validate(event.Structured); err != nil {
					return nativeError(KindNativeProtocol, "returned payload does not match the requested schema")
				}
				result.Structured = event.Structured
			} else if len(event.Structured) != 0 {
				return nativeError(KindNativeProtocol, "unexpected structured output")
			}
			result.Text = *event.Text
			if event.Usage != nil {
				result.Usage = &Usage{*event.Usage.InputTokens, *event.Usage.OutputTokens, *event.Usage.ReasoningTokens}
			}
			terminal = true
		case "error":
			if info := event.ErrorInfo; info != nil {
				slog.Info("native_model_error", "kind", event.Kind, "swift_type", info.SwiftType,
					"domain", info.Domain, "code", *info.Code, "callback_captured", *info.CallbackCaptured)
			}
			if event.Media != "" && event.Kind != KindExtractMediaMismatch {
				return nativeError(KindNativeProtocol, "unexpected native extraction data")
			}
			if (event.PageCount != nil || event.Page != nil) && event.Kind != KindPageOutOfRange {
				return nativeError(KindNativeProtocol, "unexpected native extraction data")
			}
			switch event.Kind {
			case KindModelBusy:
				if !ready || operation == "status" || operation == "extract" || event.ErrorInfo == nil {
					return nativeError(KindNativeProtocol, "invalid native busy failure")
				}
				failure := nativeError(KindModelBusy, "the native local model is temporarily busy")
				failure.NativeFailure = event.ErrorInfo
				return failure
			case KindInvalidImage, KindUnsupportedOS:
				if len(opts.Images) == 0 || event.Limit != nil || event.Actual != nil {
					return nativeError(KindNativeProtocol, "unexpected native image failure")
				}
				return nativeError(event.Kind, "native image request failed: "+string(event.Kind))
			case KindUnsupportedMedia:
				if len(opts.Images) > 0 {
					return nativeError(event.Kind, "native images support PNG and JPEG only")
				}
				if operation != "extract" {
					return nativeError(KindNativeProtocol, "unexpected native media failure")
				}
				return extractFailure(event)
			case KindInvalidDocument, KindExtractNoText, KindExtractMediaMismatch, KindPageOutOfRange:
				if operation != "extract" {
					return nativeError(KindNativeProtocol, "unrecognized native failure")
				}
				return extractFailure(event)
			case KindContextCapacity:
				if operation == "extract" {
					return extractFailure(event)
				}
				if event.Limit != nil || event.Actual != nil {
					return nativeError(KindNativeProtocol, "unexpected native extraction data")
				}
				return nativeError(event.Kind, "local model context capacity exceeded; shorten the conversation explicitly or choose another model; no messages were removed")
			case KindRequestDeclined, KindRateLimited, KindLocalUnavailable, KindNativeFailed:
				failure := nativeError(event.Kind, "native local request failed: "+string(event.Kind))
				failure.NativeFailure = event.ErrorInfo
				return failure
			case KindNativeProtocol:
				// Kept for a helper that reports the shared code for a payload
				// fault; it is not reused for capability refusals.
				return nativeError(event.Kind, "native local request failed: "+string(event.Kind))
			case KindUnsupportedCapability:
				if (event.Capability != "tools" && event.Capability != "images") || fixedReason(event.Reason) == "" {
					return nativeError(KindNativeProtocol, "invalid native capability refusal")
				}
				return capabilityRefusal(event.Capability, event.Reason)
			default:
				return nativeError(KindNativeProtocol, "unrecognized native failure")
			}
		default:
			return nativeError(KindNativeProtocol, "unknown native event")
		}
		return nil
	}
	// The shared lifecycle owns the output pipe, kills the group when the
	// protocol is rejected or the deadline expires, and closes the read end so
	// a descendant retaining an inherited pipe cannot hold the scan.
	var args []string
	if opts.ToolStep != nil {
		args = []string{"--native-tools"}
	} else if opts.hasToolMetadata() {
		// A replaced 0.6.0 helper rejects this argument before model access,
		// rather than silently ignoring metadata JSON it does not understand.
		args = []string{"--native-definitions"}
		if opts.HybridTools {
			args = []string{"--hybrid-tools"}
		}
	}
	spec := subproc.Spec{
		Name:      path,
		Args:      args,
		Stdin:     append(request, '\n'),
		StderrCap: -1,
		LineCap:   nativeMaxOutputBytes,
	}
	res, runErr := subproc.Scan(ctx, spec, handle)
	retried := false
	var busy *Error
	if operation == "tool_step" && errors.As(res.EmitErr, &busy) && busy.Kind == KindModelBusy &&
		busy.NativeFailure.retryable() && busy.NativeFailure.CallbackCaptured != nil &&
		!*busy.NativeFailure.CallbackCaptured && len(result.NativeCalls) == 0 && ctx.Err() == nil {
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return fail(nativeContextError(ctx))
		case <-timer.C:
		}
		slog.Info("native_model_retry", "kind", busy.Kind, "swift_type", busy.NativeFailure.SwiftType,
			"domain", busy.NativeFailure.Domain, "code", *busy.NativeFailure.Code)
		retried = true
		ready, terminal, previous, total = false, false, "", 0
		budgetSeen = false
		result, status = Completion{Model: model}, LocalStatus{}
		res, runErr = subproc.Scan(ctx, spec, handle)
	}
	if !res.Started {
		// A child that never started because the governing deadline expired is
		// a timeout, not a transport failure. Check the context before the
		// missing StartErr: an expired run reports no start error, and the
		// stale pipe message would blame the helper for the caller's deadline.
		if ctxErr := nativeContextError(ctx); ctxErr != nil {
			return fail(ctxErr)
		}
		if res.StartErr == nil {
			return fail(nativeError(KindTransport, "could not open native output pipe"))
		}
		return fail(nativeError(KindTransport, "could not launch native helper"))
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fail(nativeError(KindTimeout, "native local request timed out"))
		}
		return fail(nativeError(KindContextCanceled, "native local request canceled"))
	}
	if res.EmitErr != nil {
		return fail(res.EmitErr)
	}
	if res.LineOverflow || res.ReadErr != nil || runErr != nil {
		return fail(nativeError(KindNativeProtocol, "could not read native output"))
	}
	if res.WaitErr != nil {
		return fail(nativeError(KindNativeFailed, "native helper exited unsuccessfully"))
	}
	if !terminal {
		return fail(nativeError(KindNativeProtocol, "native helper ended without completion"))
	}
	if operation == "extract" && result.extraction == nil {
		return fail(nativeError(KindNativeProtocol, "native helper ended without an extraction"))
	}
	if retried {
		// The failed attempt supplied no usage, so a combined count is unknown.
		result.Usage = nil
	}
	return result, status, nil
}

// RoutedRunner adds native local without changing ShortcutRunner or auto policy.
type RoutedRunner struct {
	*ShortcutRunner
	Native *NativeRunner
}

func NewRouted() *RoutedRunner { return &RoutedRunner{New(), NewNative()} }
func (r *RoutedRunner) Run(ctx context.Context, model Model, prompt string) (string, Model, error) {
	if model == ModelLocal {
		return r.Native.Run(ctx, model, prompt)
	}
	return r.ShortcutRunner.Run(ctx, model, prompt)
}
func (r *RoutedRunner) RunComplete(ctx context.Context, model Model, prompt string) (Completion, error) {
	if model == ModelLocal {
		return r.Native.RunComplete(ctx, model, prompt)
	}
	text, used, err := r.ShortcutRunner.Run(ctx, model, prompt)
	return Completion{Text: text, Model: used}, err
}
func (r *RoutedRunner) RunWithFallback(ctx context.Context, model Model, prompt string) (string, Model, Fallback, error) {
	if model == ModelLocal {
		text, used, err := r.Native.Run(ctx, model, prompt)
		return text, used, Fallback{}, err
	}
	return r.ShortcutRunner.RunWithFallback(ctx, model, prompt)
}
func (r *RoutedRunner) RunWithImages(ctx context.Context, model Model, prompt string, images []string) (string, Model, error) {
	if model == ModelLocal {
		return r.Native.RunWithImages(ctx, model, prompt, images)
	}
	return r.ShortcutRunner.RunWithImages(ctx, model, prompt, images)
}
func (r *RoutedRunner) Stream(ctx context.Context, model Model, prompt string, emit func(string) error) (Completion, error) {
	if model != ModelLocal {
		return Completion{}, nativeError(KindUsage, "streaming is supported only for model local")
	}
	return r.Native.Stream(ctx, model, prompt, emit)
}
func (r *RoutedRunner) RunSchema(ctx context.Context, model Model, prompt string, schema *Schema) (Completion, error) {
	// Guided output is a native-only capability. Shortcut routes have no schema
	// operation, so the refusal is a usage error before anything spawns.
	if model != ModelLocal {
		return Completion{Model: model}, nativeError(KindUsage, "guided output is supported only for model local")
	}
	return r.Native.RunSchema(ctx, model, prompt, schema)
}

// ExtractPDF always uses the native helper: extraction is on-device for every
// route, and the generation that follows may go to any model.
func (r *RoutedRunner) ExtractPDF(ctx context.Context, data []byte, pages []int) (Extraction, error) {
	return r.Native.ExtractPDF(ctx, data, pages)
}

func (r *RoutedRunner) NativeStatus(ctx context.Context) (LocalStatus, error) {
	return r.Native.NativeStatus(ctx)
}

// ShortcutTransport exposes configuration and discovery of existing bridges.
func (r *RoutedRunner) ShortcutTransport() *ShortcutRunner { return r.ShortcutRunner }
